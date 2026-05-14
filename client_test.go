package zoomsdk

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
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
