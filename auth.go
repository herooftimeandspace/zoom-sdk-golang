package zoomsdk

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// TokenResponse is the validated Zoom OAuth token payload.
type TokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// Validate normalizes and validates the token response.
func (t *TokenResponse) Validate() error {
	t.TokenType = strings.ToLower(strings.TrimSpace(t.TokenType))
	if t.TokenType != "bearer" {
		return &ValidationError{Message: "Zoom OAuth token_type must be bearer"}
	}
	if t.ExpiresIn <= 0 {
		return &ValidationError{Message: "expires_in must be greater than 0"}
	}
	if strings.TrimSpace(t.AccessToken) == "" {
		return &ValidationError{Message: "access_token must not be empty"}
	}
	return nil
}

// TokenManager acquires, caches, and refreshes OAuth tokens.
type TokenManager struct {
	httpClient   *http.Client
	oauthURL     string
	accountID    string
	clientID     string
	clientSecret string
	tokenSkew    int
	override     string
	logger       *Logger

	mu        sync.Mutex
	cached    string
	expiresAt time.Time
}

// NewTokenManager constructs a token manager.
func NewTokenManager(httpClient *http.Client, settings Settings, accessToken string, logger *Logger) *TokenManager {
	return &TokenManager{
		httpClient:   httpClient,
		oauthURL:     strings.TrimRight(settings.OAuthURL, "/"),
		accountID:    settings.AccountID,
		clientID:     settings.ClientID,
		clientSecret: settings.ClientSecret,
		tokenSkew:    settings.TokenSkewSeconds,
		override:     accessToken,
		logger:       logger,
	}
}

// GetAccessToken returns a valid access token, refreshing only when needed.
func (m *TokenManager) GetAccessToken(ctx context.Context) (string, error) {
	if m.override != "" {
		return m.override, nil
	}
	if token := m.cachedToken(); token != "" {
		return token, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cached != "" && time.Now().Before(m.expiresAt) {
		return m.cached, nil
	}

	token, expiry, err := m.fetchToken(ctx)
	if err != nil {
		return "", err
	}
	m.cached = token.AccessToken
	m.expiresAt = expiry
	if m.logger != nil {
		m.logger.Log("INFO", "Acquired Zoom access token.", map[string]any{
			"event":            "token_acquired",
			"method":           http.MethodPost,
			"path":             "/oauth/token",
			"url":              m.oauthURL + "/oauth/token",
			"status_code":      200,
			"token_expires_at": expiry.UTC().Format(time.RFC3339),
		})
	}
	return m.cached, nil
}

func (m *TokenManager) cachedToken() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cached != "" && time.Now().Before(m.expiresAt) {
		return m.cached
	}
	return ""
}

func (m *TokenManager) fetchToken(ctx context.Context) (TokenResponse, time.Time, error) {
	if m.accountID == "" || m.clientID == "" || m.clientSecret == "" {
		return TokenResponse{}, time.Time{}, &ValidationError{Message: "Zoom OAuth credentials are missing"}
	}

	form := url.Values{}
	form.Set("grant_type", "account_credentials")
	form.Set("account_id", m.accountID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.oauthURL+"/oauth/token?"+form.Encode(), nil)
	if err != nil {
		return TokenResponse{}, time.Time{}, err
	}
	req.SetBasicAuth(m.clientID, m.clientSecret)

	resp, err := m.httpClient.Do(req)
	if err != nil {
		if m.logger != nil {
			m.logger.Log("ERROR", "Failed to acquire Zoom access token.", map[string]any{
				"event":         "token_acquisition_failed",
				"method":        http.MethodPost,
				"path":          "/oauth/token",
				"url":           m.oauthURL + "/oauth/token",
				"error_type":    "transport",
				"error_message": err.Error(),
			})
		}
		return TokenResponse{}, time.Time{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return TokenResponse{}, time.Time{}, fmt.Errorf("token request failed with status %d", resp.StatusCode)
	}

	var token TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return TokenResponse{}, time.Time{}, err
	}
	if err := token.Validate(); err != nil {
		return TokenResponse{}, time.Time{}, err
	}
	expiry := time.Now().Add(time.Duration(token.ExpiresIn-m.tokenSkew) * time.Second)
	if !expiry.After(time.Now()) {
		return TokenResponse{}, time.Time{}, &ValidationError{Message: "Zoom OAuth token expires too soon after applying token_skew_seconds"}
	}
	return token, expiry, nil
}
