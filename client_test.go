package zoomsdk

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"
)

func TestRenderPathRejectsMissingParameters(t *testing.T) {
	if _, err := renderPath("/users/{userId}", nil); err == nil {
		t.Fatal("expected unresolved path placeholders to fail")
	}
}

func TestBuildHeadersPreservesSDKAuthorization(t *testing.T) {
	client := &Client{
		tokenManager: NewTokenManager(&http.Client{}, DefaultSettings(), "test-token", DefaultLogger()),
	}
	headers, err := client.buildHeaders(context.Background(), map[string]string{
		"Authorization": "Bearer attacker",
		"X-Test":        "value",
	})
	if err != nil {
		t.Fatalf("unexpected buildHeaders error: %v", err)
	}
	if headers["Authorization"] != "Bearer test-token" {
		t.Fatalf("unexpected authorization header: %s", headers["Authorization"])
	}
	if headers["X-Test"] != "value" {
		t.Fatal("expected custom header to be preserved")
	}
}

func TestNewClientConstructsSDK(t *testing.T) {
	settings := DefaultSettings()
	client, err := NewClient(settings, "test-token")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if client.SDK == nil {
		t.Fatal("expected SDK to be initialized")
	}
	if client.DefaultAccountID() != settings.AccountID {
		t.Fatalf("unexpected default account id: %s", client.DefaultAccountID())
	}
	if nilClient := (*Client)(nil); nilClient.DefaultAccountID() != "" {
		t.Fatal("nil client should not return an account id")
	}
}

func TestClientRequestRejectsUnmarshalableJSONBody(t *testing.T) {
	settings := DefaultSettings()
	settings.BaseURL = "https://api.zoom.us/v2"
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatal("request should fail before transport")
		return nil, nil
	})}
	client := &Client{
		settings:       settings,
		httpClient:     httpClient,
		logger:         DefaultLogger(),
		schemaRegistry: &SchemaRegistry{},
		tokenManager:   NewTokenManager(httpClient, settings, "test-token", DefaultLogger()),
	}
	_, err := client.Request(context.Background(), "POST", "/users", RequestOptions{JSONBody: make(chan int)})
	if err == nil || !strings.Contains(err.Error(), "unsupported type") {
		t.Fatalf("expected JSON marshal error, got %v", err)
	}
}

func TestClientRequestValidatesResponse(t *testing.T) {
	registry := &SchemaRegistry{
		operations: []SchemaOperation{
			{
				SchemaName:   "Users",
				Method:       "GET",
				TemplatePath: "/users/{userId}",
				PathRegex:    templatePathToRegex("/users/{userId}"),
				Responses: map[string]any{
					"200": map[string]any{
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"id": map[string]any{"type": "string"},
									},
									"required": []any{"id"},
								},
							},
						},
					},
				},
				Spec: map[string]any{},
			},
		},
		validator: &SchemaValidator{tools: &OpenAPITools{}},
	}
	settings := DefaultSettings()
	settings.BaseURL = "https://api.zoom.us/v2"
	settings.OAuthURL = "https://zoom.us"
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-token" {
			t.Fatalf("unexpected authorization header: %s", auth)
		}
		if r.URL.String() != "https://api.zoom.us/v2/users/me" {
			t.Fatalf("unexpected request URL: %s", r.URL.String())
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewBufferString(`{"id":"123"}`)),
		}, nil
	})}
	client := &Client{
		settings:        settings,
		httpClient:      httpClient,
		logger:          DefaultLogger(),
		schemaRegistry:  registry,
		webhookRegistry: &WebhookRegistry{validator: &SchemaValidator{tools: &OpenAPITools{}}},
		tokenManager:    NewTokenManager(httpClient, settings, "test-token", DefaultLogger()),
	}
	value, err := client.Request(context.Background(), "GET", "/users/{userId}", RequestOptions{PathParams: map[string]string{"userId": "me"}})
	if err != nil {
		t.Fatalf("unexpected request error: %v", err)
	}
	payload, ok := value.(map[string]any)
	if !ok || payload["id"] != "123" {
		t.Fatalf("unexpected payload: %#v", value)
	}
}

func TestClientRequestRawBodySkipsSchemaValidationButKeepsRequestBehavior(t *testing.T) {
	settings := DefaultSettings()
	settings.BaseURL = "https://proxy.zoom.test/v2"
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-token" {
			t.Fatalf("unexpected authorization header: %s", auth)
		}
		if r.URL.String() != "https://proxy.zoom.test/v2/users/me" {
			t.Fatalf("unexpected request URL: %s", r.URL.String())
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"message":"shape changed"}`)),
		}, nil
	})}
	client := &Client{
		settings:   settings,
		httpClient: httpClient,
		logger:     DefaultLogger(),
		schemaRegistry: &SchemaRegistry{
			operations: []SchemaOperation{{
				SchemaName:   "Users",
				Method:       "GET",
				TemplatePath: "/users/{userId}",
				PathRegex:    templatePathToRegex("/users/{userId}"),
				Responses: map[string]any{"200": map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{
					"type":       "object",
					"properties": map[string]any{"id": map[string]any{"type": "string"}},
					"required":   []any{"id"},
				}}}}},
				Spec:      map[string]any{},
				ServerURL: "https://api.zoom.us/v2",
			}},
			validator: &SchemaValidator{tools: &OpenAPITools{}},
		},
		tokenManager: NewTokenManager(httpClient, settings, "test-token", DefaultLogger()),
	}

	body, err := client.RequestRawBody(context.Background(), "GET", "/users/{userId}", RequestOptions{PathParams: map[string]string{"userId": "me"}})
	if err != nil {
		t.Fatalf("raw body request: %v", err)
	}
	if string(body) != `{"message":"shape changed"}` {
		t.Fatalf("raw body = %s", body)
	}
}

func TestClientRequestRejectsOversizedSuccessfulResponse(t *testing.T) {
	settings := DefaultSettings()
	settings.BaseURL = "https://api.zoom.us/v2"
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(strings.Repeat("x", int(maxResponseBodyBytes)+1))),
		}, nil
	})}
	client := &Client{
		settings:   settings,
		httpClient: httpClient,
		logger:     DefaultLogger(),
		schemaRegistry: &SchemaRegistry{
			operations: []SchemaOperation{{
				SchemaName:   "Users",
				Method:       "GET",
				TemplatePath: "/users",
				PathRegex:    templatePathToRegex("/users"),
				Responses:    map[string]any{"200": map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object"}}}}},
				Spec:         map[string]any{},
			}},
			validator: &SchemaValidator{tools: &OpenAPITools{}},
		},
		tokenManager: NewTokenManager(httpClient, settings, "test-token", DefaultLogger()),
	}

	if _, err := client.Request(context.Background(), "GET", "/users", RequestOptions{}); err == nil || !strings.Contains(err.Error(), "response body exceeds") {
		t.Fatalf("expected bounded response error, got %v", err)
	}
	if _, err := client.RequestRawBody(context.Background(), "GET", "/users", RequestOptions{}); err == nil || !strings.Contains(err.Error(), "response body exceeds") {
		t.Fatalf("expected bounded raw response error, got %v", err)
	}
}

func TestSDKOperationRawBodyForwardsRequestOptionsAndHandlesNoContent(t *testing.T) {
	settings := DefaultSettings()
	settings.BaseURL = "https://proxy.zoom.test/v2"
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("request method = %q", r.Method)
		}
		if r.URL.String() != "https://proxy.zoom.test/v2/users/me?include=profile" {
			t.Fatalf("request URL = %q", r.URL.String())
		}
		if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("X-Test") != "value" {
			t.Fatalf("request headers = %#v", r.Header)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if string(body) != `{"enabled":true}` {
			t.Fatalf("request body = %s", body)
		}
		return &http.Response{
			StatusCode: http.StatusNoContent,
			Header:     http.Header{},
			Body:       http.NoBody,
		}, nil
	})}
	client := &Client{
		settings:     settings,
		httpClient:   httpClient,
		logger:       DefaultLogger(),
		tokenManager: NewTokenManager(httpClient, settings, "test-token", DefaultLogger()),
	}
	operation := &SDKOperation{client: client, HTTPMethod: http.MethodPost, Path: "/users/{userId}"}

	body, err := operation.RawBody(
		context.Background(),
		map[string]string{"userId": "me"},
		map[string]any{"include": "profile"},
		map[string]any{"enabled": true},
		map[string]string{"X-Test": "value"},
	)
	if err != nil {
		t.Fatalf("raw body operation: %v", err)
	}
	if body != nil {
		t.Fatalf("204 raw body = %q, want nil", body)
	}
}

func TestClientRequestRawBodyRetriesTransportAndStatusFailures(t *testing.T) {
	settings := DefaultSettings()
	settings.BaseURL = "https://proxy.zoom.test/v2"
	settings.MaxRetries = 2
	settings.BackoffBaseSeconds = 0
	settings.BackoffMaxSeconds = 0
	attempts := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		switch attempts {
		case 1:
			return nil, errors.New("temporary transport failure")
		case 2:
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{}, Body: http.NoBody}, nil
		default:
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("ok"))}, nil
		}
	})}
	client := &Client{
		settings:     settings,
		httpClient:   httpClient,
		logger:       DefaultLogger(),
		tokenManager: NewTokenManager(httpClient, settings, "test-token", DefaultLogger()),
	}

	body, err := client.RequestRawBody(context.Background(), http.MethodGet, "/users", RequestOptions{})
	if err != nil {
		t.Fatalf("retried raw request: %v", err)
	}
	if string(body) != "ok" || attempts != 3 {
		t.Fatalf("raw body = %q after %d attempts", body, attempts)
	}
}

func TestClientRequestRawBodyRejectsInvalidInputsAndResponses(t *testing.T) {
	settings := DefaultSettings()
	settings.BaseURL = "https://proxy.zoom.test/v2"
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{}, Body: http.NoBody}, nil
	})}
	client := &Client{
		settings:     settings,
		httpClient:   httpClient,
		logger:       DefaultLogger(),
		tokenManager: NewTokenManager(httpClient, settings, "test-token", DefaultLogger()),
	}

	if _, err := client.RequestRawBody(context.Background(), http.MethodGet, "/users/{userId}", RequestOptions{}); err == nil || !strings.Contains(err.Error(), "unresolved path") {
		t.Fatalf("expected unresolved path error, got %v", err)
	}
	if _, err := client.RequestRawBody(context.Background(), http.MethodPost, "/users", RequestOptions{JSONBody: func() {}}); err == nil || !strings.Contains(err.Error(), "unsupported type") {
		t.Fatalf("expected JSON encoding error, got %v", err)
	}
	if _, err := client.RequestRawBody(context.Background(), http.MethodPost, "/users", RequestOptions{}); err == nil || !strings.Contains(err.Error(), "status 400") {
		t.Fatalf("expected terminal status error, got %v", err)
	}
	if _, err := readBoundedResponseBody(iotest.ErrReader(errors.New("read failed"))); err == nil || !strings.Contains(err.Error(), "read failed") {
		t.Fatalf("expected response read error, got %v", err)
	}
}
