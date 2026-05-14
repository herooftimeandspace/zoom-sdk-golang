package zoomsdk

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestTokenResponseValidateRejectsBadTokenType(t *testing.T) {
	response := TokenResponse{AccessToken: "token", TokenType: "mac", ExpiresIn: 3600}
	if err := response.Validate(); err == nil {
		t.Fatal("expected invalid token type to fail")
	}
}

func TestTokenManagerFetchesAndCachesToken(t *testing.T) {
	calls := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://zoom.us/oauth/token?account_id=acct&grant_type=account_credentials" {
			t.Fatalf("unexpected token URL: %s", r.URL.String())
		}
		username, password, ok := r.BasicAuth()
		if !ok || username != "client" || password != "secret" {
			t.Fatalf("unexpected basic auth: %q %q %v", username, password, ok)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewBufferString(`{"access_token":"fresh-token","token_type":"bearer","expires_in":3600}`)),
		}, nil
	})}

	settings := DefaultSettings()
	manager := NewTokenManager(httpClient, settings, "", DefaultLogger())
	manager.accountID = "acct"
	manager.clientID = "client"
	manager.clientSecret = "secret"

	token, err := manager.GetAccessToken(context.Background())
	if err != nil {
		t.Fatalf("unexpected token fetch error: %v", err)
	}
	if token != "fresh-token" {
		t.Fatalf("unexpected token: %s", token)
	}
	token, err = manager.GetAccessToken(context.Background())
	if err != nil {
		t.Fatalf("unexpected cached token error: %v", err)
	}
	if token != "fresh-token" || calls != 1 {
		t.Fatalf("expected cached token reuse, token=%s calls=%d", token, calls)
	}
}

func TestTokenManagerRejectsSoonExpiringToken(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewBufferString(`{"access_token":"fresh-token","token_type":"bearer","expires_in":1}`)),
		}, nil
	})}

	settings := DefaultSettings()
	settings.TokenSkewSeconds = 60
	manager := NewTokenManager(httpClient, settings, "", DefaultLogger())
	manager.accountID = "acct"
	manager.clientID = "client"
	manager.clientSecret = "secret"

	if _, err := manager.GetAccessToken(context.Background()); err == nil {
		t.Fatal("expected soon-expiring token to fail")
	}
}
