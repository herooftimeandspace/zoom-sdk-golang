package zoomsdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client executes validated Zoom API requests and exposes the generated SDK.
type Client struct {
	settings        Settings
	defaultAccount  string
	httpClient      *http.Client
	logger          *Logger
	schemaRegistry  *SchemaRegistry
	webhookRegistry *WebhookRegistry
	tokenManager    *TokenManager
	SDK             *SDK
}

// RequestOptions controls one low-level request invocation.
type RequestOptions struct {
	PathParams map[string]string
	Query      map[string]any
	JSONBody   any
	Headers    map[string]string
	Timeout    time.Duration
}

// NewClient constructs a client from validated settings.
func NewClient(settings Settings, accessToken string) (*Client, error) {
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	schemaRegistry, err := NewSchemaRegistry("")
	if err != nil {
		return nil, err
	}
	webhookRegistry, err := NewWebhookRegistry("")
	if err != nil {
		return nil, err
	}
	httpClient := &http.Client{Timeout: time.Duration(settings.TimeoutSeconds * float64(time.Second))}
	logger := DefaultLogger()
	client := &Client{
		settings:        settings,
		defaultAccount:  settings.AccountID,
		httpClient:      httpClient,
		logger:          logger,
		schemaRegistry:  schemaRegistry,
		webhookRegistry: webhookRegistry,
		tokenManager:    NewTokenManager(httpClient, settings, accessToken, logger),
	}
	sdk, err := NewSDK(client)
	if err != nil {
		return nil, err
	}
	client.SDK = sdk
	return client, nil
}

// NewClientFromEnvironment constructs a client from environment variables.
func NewClientFromEnvironment(loadDotenv bool, accessToken string) (*Client, error) {
	settings, err := LoadSettingsFromEnvironment(loadDotenv)
	if err != nil {
		return nil, err
	}
	return NewClient(settings, accessToken)
}

// DefaultAccountID returns the account id used for account-scoped SDK calls.
func (c *Client) DefaultAccountID() string {
	if c == nil {
		return ""
	}
	if c.defaultAccount != "" {
		return c.defaultAccount
	}
	return c.settings.AccountID
}

// Request executes one authenticated validated request.
func (c *Client) Request(ctx context.Context, method string, path string, options RequestOptions) (any, error) {
	actualPath, err := renderPath(path, options.PathParams)
	if err != nil {
		return nil, err
	}
	baseURL := c.baseURLForRequest(method, actualPath)
	requestURL, err := buildURL(baseURL, actualPath, options.Query)
	if err != nil {
		return nil, err
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = time.Duration(c.settings.TimeoutSeconds * float64(time.Second))
	}
	headers, err := c.buildHeaders(ctx, options.Headers)
	if err != nil {
		return nil, err
	}

	var bodyBytes []byte
	if options.JSONBody != nil {
		bodyBytes, err = json.Marshal(options.JSONBody)
		if err != nil {
			return nil, err
		}
		headers["Content-Type"] = "application/json"
	}

	normalizedMethod := strings.ToUpper(method)
	var lastErr error
	for attempt := 0; attempt <= c.settings.MaxRetries; attempt++ {
		requestCtx, cancel := context.WithTimeout(ctx, timeout)
		request, reqErr := http.NewRequestWithContext(requestCtx, normalizedMethod, requestURL, bytes.NewReader(bodyBytes))
		if reqErr != nil {
			cancel()
			return nil, reqErr
		}
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		startedAt := time.Now()
		c.logger.Log("INFO", "Sending Zoom API request.", map[string]any{
			"event":         "request_attempt",
			"method":        normalizedMethod,
			"url":           requestURL,
			"path":          actualPath,
			"retry_attempt": attempt,
		})
		response, reqErr := c.httpClient.Do(request)
		cancel()
		if reqErr != nil {
			lastErr = reqErr
			if attempt < c.settings.MaxRetries && isRetriableMethod(normalizedMethod) {
				c.logRetry(normalizedMethod, requestURL, actualPath, attempt+1, reqErr.Error(), 0)
				time.Sleep(c.calculateBackoff(attempt))
				continue
			}
			return nil, reqErr
		}
		payload, parseErr := c.handleResponse(normalizedMethod, actualPath, requestURL, response, startedAt)
		if parseErr == nil {
			return payload, nil
		}
		lastErr = parseErr
		if attempt < c.settings.MaxRetries && isRetriableStatus(response.StatusCode) && isRetriableMethod(normalizedMethod) {
			c.logRetry(normalizedMethod, requestURL, actualPath, attempt+1, fmt.Sprintf("HTTP %d", response.StatusCode), response.StatusCode)
			time.Sleep(c.calculateBackoff(attempt))
			continue
		}
		return nil, parseErr
	}
	return nil, lastErr
}

func (c *Client) baseURLForRequest(method string, actualPath string) string {
	if c.settings.BaseURL != DefaultSettings().BaseURL {
		return c.settings.BaseURL
	}
	return c.schemaRegistry.BaseURLForRequest(method, actualPath, c.settings.BaseURL)
}

// ValidateWebhook validates a webhook payload.
func (c *Client) ValidateWebhook(eventName string, payload any, schemaName string, operationID string) error {
	return c.webhookRegistry.ValidateWebhook(eventName, payload, schemaName, operationID)
}

func (c *Client) buildHeaders(ctx context.Context, custom map[string]string) (map[string]string, error) {
	token, err := c.tokenManager.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	headers := map[string]string{
		"Accept":        "application/json",
		"Authorization": "Bearer " + token,
	}
	for key, value := range custom {
		if strings.EqualFold(key, "Authorization") {
			continue
		}
		headers[key] = value
	}
	return headers, nil
}

func (c *Client) handleResponse(method string, actualPath string, requestURL string, response *http.Response, startedAt time.Time) (any, error) {
	defer response.Body.Close()
	c.logger.Log("INFO", "Received Zoom API response.", map[string]any{
		"event":       "response_received",
		"method":      method,
		"url":         requestURL,
		"path":        actualPath,
		"status_code": response.StatusCode,
		"duration_ms": time.Since(startedAt).Milliseconds(),
		"request_id":  response.Header.Get("x-request-id"),
		"trace_id":    response.Header.Get("x-zm-trackingid"),
	})
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, fmt.Errorf("request failed with status %d", response.StatusCode)
	}
	if response.StatusCode == http.StatusNoContent {
		return nil, c.schemaRegistry.ValidateResponse(method, actualPath, response.StatusCode, nil)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, c.schemaRegistry.ValidateResponse(method, actualPath, response.StatusCode, nil)
	}
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		c.logger.Log("ERROR", "Response body was not valid JSON.", map[string]any{
			"event":         "invalid_json_response",
			"method":        method,
			"url":           requestURL,
			"path":          actualPath,
			"status_code":   response.StatusCode,
			"error_type":    "json_decode",
			"error_message": err.Error(),
		})
		return nil, err
	}
	if err := c.schemaRegistry.ValidateResponse(method, actualPath, response.StatusCode, payload); err != nil {
		c.logger.Log("ERROR", "Response schema validation failed.", map[string]any{
			"event":         "schema_validation_failed",
			"method":        method,
			"url":           requestURL,
			"path":          actualPath,
			"status_code":   response.StatusCode,
			"error_type":    "schema",
			"error_message": err.Error(),
		})
		return nil, err
	}
	return payload, nil
}

func (c *Client) logRetry(method string, requestURL string, actualPath string, retryAttempt int, reason string, statusCode int) {
	fields := map[string]any{
		"event":         "request_retry",
		"method":        method,
		"url":           requestURL,
		"path":          actualPath,
		"retry_attempt": retryAttempt,
		"error_type":    "retry",
		"error_message": reason,
	}
	if statusCode != 0 {
		fields["status_code"] = statusCode
	}
	c.logger.Log("WARNING", "Retrying Zoom API request after a retriable failure.", fields)
}

func renderPath(path string, params map[string]string) (string, error) {
	actualPath := path
	if !strings.HasPrefix(actualPath, "/") {
		actualPath = "/" + actualPath
	}
	for key, value := range params {
		actualPath = strings.ReplaceAll(actualPath, "{"+key+"}", url.PathEscape(value))
	}
	if strings.Contains(actualPath, "{") || strings.Contains(actualPath, "}") {
		return "", fmt.Errorf("unresolved path parameters remain in path: %s", actualPath)
	}
	return actualPath, nil
}

func buildURL(baseURL string, path string, query map[string]any) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/") + path)
	if err != nil {
		return "", err
	}
	values := parsed.Query()
	for key, value := range query {
		values.Set(key, fmt.Sprintf("%v", value))
	}
	parsed.RawQuery = values.Encode()
	return parsed.String(), nil
}

func isRetriableStatus(status int) bool {
	switch status {
	case 429, 500, 502, 503, 504:
		return true
	default:
		return false
	}
}

func isRetriableMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodDelete, http.MethodPut:
		return true
	default:
		return false
	}
}

func (c *Client) calculateBackoff(attempt int) time.Duration {
	multiplier := float64(uint(1) << uint(attempt))
	sleep := c.settings.BackoffBaseSeconds * float64(time.Second) * multiplier
	maximum := c.settings.BackoffMaxSeconds * float64(time.Second)
	if sleep > maximum {
		sleep = maximum
	}
	jitter := rand.New(rand.NewSource(time.Now().UnixNano())).Float64()*0.5 + 0.75
	return time.Duration(sleep * jitter)
}
