package zoomsdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestErrorTypesRenderMessages(t *testing.T) {
	if (&ConfigError{Field: "base_url", Message: "must use https"}).Error() != "base_url: must use https" {
		t.Fatal("expected config error to include field")
	}
	if (&ConfigError{Message: "plain message"}).Error() != "plain message" {
		t.Fatal("expected config error without field to return message")
	}
	if (&ValidationError{Message: "bad payload"}).Error() != "bad payload" {
		t.Fatal("expected validation error message")
	}
}

func TestSettingsValidationAndDiscoveryHelpers(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*Settings)
		wantText string
	}{
		{
			name: "query",
			mutate: func(settings *Settings) {
				settings.BaseURL = "https://api.zoom.us/v2?bad=1"
			},
			wantText: "must not include a query string",
		},
		{
			name: "fragment",
			mutate: func(settings *Settings) {
				settings.OAuthURL = "https://zoom.us#bad"
			},
			wantText: "must not include a fragment",
		},
		{
			name: "negative skew",
			mutate: func(settings *Settings) {
				settings.TokenSkewSeconds = -1
			},
			wantText: "token_skew_seconds",
		},
		{
			name: "negative retries",
			mutate: func(settings *Settings) {
				settings.MaxRetries = -1
			},
			wantText: "max_retries",
		},
		{
			name: "bad backoff base",
			mutate: func(settings *Settings) {
				settings.BackoffBaseSeconds = 0
			},
			wantText: "backoff_base_seconds",
		},
		{
			name: "bad backoff max",
			mutate: func(settings *Settings) {
				settings.BackoffMaxSeconds = 0
			},
			wantText: "backoff_max_seconds",
		},
		{
			name: "bad timeout",
			mutate: func(settings *Settings) {
				settings.TimeoutSeconds = 0
			},
			wantText: "timeout_seconds",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := DefaultSettings()
			tt.mutate(&settings)
			err := settings.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.wantText) {
				t.Fatalf("expected %q in error, got %v", tt.wantText, err)
			}
		})
	}

	validated, err := validateHTTPSURL("https://api.zoom.us/v2/", "base_url")
	if err != nil || validated != "https://api.zoom.us/v2" {
		t.Fatalf("unexpected validated URL: %q %v", validated, err)
	}

	if discoverProjectRoot("/definitely/not/here") == "" {
		t.Fatal("expected discoverProjectRoot to return a path")
	}
}

func TestLoadSettingsFromEnvironmentRejectsBadIntegerAndReadsDotenv(t *testing.T) {
	t.Setenv("ZOOM_TOKEN_SKEW_SECONDS", "bad")
	if _, err := LoadSettingsFromEnvironment(false); err == nil {
		t.Fatal("expected invalid token skew to fail")
	}
	t.Setenv("ZOOM_TOKEN_SKEW_SECONDS", "")
	if err := os.Unsetenv("ZOOM_TOKEN_SKEW_SECONDS"); err != nil {
		t.Fatalf("unset env: %v", err)
	}

	tmp := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})

	goModPath := filepath.Join(tmp, "go.mod")
	if err := os.WriteFile(goModPath, []byte("module example.com/test\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ".env"), []byte("ZOOM_ACCOUNT_ID=acct-dotenv\nZOOM_CLIENT_ID=client-dotenv\n"), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	settings, err := LoadSettingsFromEnvironment(true)
	if err != nil {
		t.Fatalf("load settings with .env: %v", err)
	}
	if settings.AccountID != "acct-dotenv" || settings.ClientID != "client-dotenv" {
		t.Fatalf("expected dotenv values, got %#v", settings)
	}
}

func TestLoggerHelpers(t *testing.T) {
	if ConfigureLogging() == nil {
		t.Fatal("expected configured logger")
	}
	if NewLogger(nil) == nil {
		t.Fatal("expected nil-writer logger fallback")
	}
	var nilLogger *Logger
	nilLogger.Log("INFO", "ignored", nil)

	buffer := &bytes.Buffer{}
	logger := NewLogger(buffer)
	logger.Log("INFO", "message", map[string]any{"skip": nil, "event": "evt"})
	output := buffer.String()
	if strings.Contains(output, `"skip"`) {
		t.Fatalf("expected nil fields to be omitted: %s", output)
	}
}

func TestTokenManagerErrorPaths(t *testing.T) {
	if err := (&TokenResponse{AccessToken: "token", TokenType: "bearer", ExpiresIn: 0}).Validate(); err == nil {
		t.Fatal("expected expires_in validation failure")
	}
	if err := (&TokenResponse{AccessToken: "", TokenType: "bearer", ExpiresIn: 60}).Validate(); err == nil {
		t.Fatal("expected access token validation failure")
	}

	settings := DefaultSettings()
	manager := NewTokenManager(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("boom")
	})}, settings, "", DefaultLogger())
	manager.accountID = "acct"
	manager.clientID = "client"
	manager.clientSecret = "secret"
	if _, err := manager.GetAccessToken(context.Background()); err == nil {
		t.Fatal("expected transport failure")
	}

	manager = NewTokenManager(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Body:       io.NopCloser(strings.NewReader("nope")),
			Header:     http.Header{},
		}, nil
	})}, settings, "", DefaultLogger())
	manager.accountID = "acct"
	manager.clientID = "client"
	manager.clientSecret = "secret"
	if _, err := manager.GetAccessToken(context.Background()); err == nil || !strings.Contains(err.Error(), "status 401") {
		t.Fatalf("expected status error, got %v", err)
	}

	manager = NewTokenManager(&http.Client{}, settings, "", DefaultLogger())
	if _, err := manager.GetAccessToken(context.Background()); err == nil || !strings.Contains(err.Error(), "credentials are missing") {
		t.Fatalf("expected missing credentials error, got %v", err)
	}

	manager = NewTokenManager(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("{")),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		}, nil
	})}, settings, "", DefaultLogger())
	manager.accountID = "acct"
	manager.clientID = "client"
	manager.clientSecret = "secret"
	if _, err := manager.GetAccessToken(context.Background()); err == nil {
		t.Fatal("expected invalid JSON token response to fail")
	}
}

func TestSchemaHelpersAndValidationBranches(t *testing.T) {
	tools := &OpenAPITools{}
	spec := map[string]any{
		"components": map[string]any{
			"schemas": map[string]any{
				"Base": map[string]any{
					"type":       "object",
					"properties": map[string]any{"id": map[string]any{"type": "string"}},
					"required":   []any{"id"},
				},
			},
		},
	}

	resolved, err := tools.ResolveSchema(spec, map[string]any{
		"$ref": "#/components/schemas/Base",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
	})
	if err != nil {
		t.Fatalf("resolve schema: %v", err)
	}
	prepared, err := tools.PrepareSchema(spec, resolved.(map[string]any))
	if err != nil {
		t.Fatalf("prepare schema: %v", err)
	}
	if err := (&SchemaValidator{tools: tools}).ValidatePayload(spec, prepared, map[string]any{"id": "1"}, "ctx"); err != nil {
		t.Fatalf("unexpected prepared schema validation error: %v", err)
	}

	allOfSchema := map[string]any{
		"allOf": []any{
			map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string"},
				},
				"required": []any{"id"},
			},
			map[string]any{
				"type": "object",
				"properties": map[string]any{
					"status": map[string]any{
						"type": "string",
						"enum": []any{"active"},
					},
				},
			},
		},
	}
	normalizedAllOf := tools.NormalizePayloadForSchema(map[string]any{"id": "1", "status": ""}, allOfSchema).(map[string]any)
	if _, ok := normalizedAllOf["status"]; ok {
		t.Fatalf("expected empty optional enum to be dropped in allOf payload: %#v", normalizedAllOf)
	}

	oneOfSchema := map[string]any{
		"oneOf": []any{
			map[string]any{"type": "string"},
			map[string]any{"type": "integer"},
		},
	}
	if normalized := tools.NormalizePayloadForSchema(12, oneOfSchema); normalized != 12 {
		t.Fatalf("expected oneOf payload to stay matched: %#v", normalized)
	}

	anyOfSchema := map[string]any{
		"anyOf": []any{
			map[string]any{"type": "boolean"},
			map[string]any{"type": "string"},
		},
	}
	if normalized := tools.NormalizePayloadForSchema(true, anyOfSchema); normalized != true {
		t.Fatalf("expected anyOf payload to stay matched: %#v", normalized)
	}

	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"name":  map[string]any{"type": "string"},
			"count": map[string]any{"type": "integer"},
			"ratio": map[string]any{"type": "number"},
			"flag":  map[string]any{"type": "boolean"},
			"tags": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
			"kind": map[string]any{
				"enum": []any{"alpha", "beta"},
			},
		},
		"required": []any{"name", "count", "ratio", "flag", "tags"},
	}
	errors := []string{}
	validateAgainstSchema(schema, map[string]any{
		"name":  7,
		"count": "bad",
		"ratio": "bad",
		"flag":  "bad",
		"tags":  []any{1},
		"kind":  "gamma",
		"extra": true,
	}, "$", &errors)
	if len(errors) < 6 {
		t.Fatalf("expected multiple validation errors, got %#v", errors)
	}

	errors = []string{}
	validateAgainstSchema(map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "nope", "$", &errors)
	validateAgainstSchema(map[string]any{"oneOf": []any{map[string]any{"type": "string"}}}, 7, "$", &errors)
	validateAgainstSchema(map[string]any{"anyOf": []any{map[string]any{"type": "boolean"}}}, "bad", "$", &errors)
	if len(errors) < 3 {
		t.Fatalf("expected array/oneOf/anyOf errors, got %#v", errors)
	}

	if normalizeTypeName("long") != "integer" || normalizeTypeName("BuildingCode") != "string" || normalizeTypeName("custom") != "custom" {
		t.Fatal("unexpected type normalization")
	}
	merged := mergeSchemaBranch(
		map[string]any{
			"type":       "object",
			"properties": map[string]any{"id": map[string]any{"type": "string"}},
			"required":   []any{"id"},
			"oneOf":      []any{},
		},
		map[string]any{
			"properties": map[string]any{"name": map[string]any{"type": "string"}},
			"required":   []any{"name"},
		},
		"oneOf",
	)
	if len(merged["required"].([]any)) != 2 {
		t.Fatalf("expected merged required fields, got %#v", merged["required"])
	}
	if !shouldDropEmptyOptionalEnumValue("status", "", map[string]any{"type": "string", "enum": []any{"active"}}, map[string]bool{}) {
		t.Fatal("expected optional enum drop")
	}
	if shouldDropEmptyOptionalEnumValue("status", "", map[string]any{"type": "string", "enum": []any{"", "active"}}, map[string]bool{}) {
		t.Fatal("expected explicit empty enum value to be preserved")
	}
	if len(requiredNameSet([]any{"id", 1})) != 1 || !enumContains([]any{1, "two"}, "two") {
		t.Fatal("expected helper functions to work")
	}

	clone := cloneMap(map[string]any{"nested": map[string]any{"value": "x"}}).(map[string]any)
	clone["nested"].(map[string]any)["value"] = "y"
	if specTitle(map[string]any{}, "fallback.json") != "fallback.json" {
		t.Fatal("expected fallback title")
	}
	if firstServerURL(map[string]any{}) != "" {
		t.Fatal("expected empty server URL")
	}
	if !templatePathToRegex("/users/{userId}").MatchString("/users/me") {
		t.Fatal("expected path regex match")
	}
	if len(extractParameters([]any{map[string]any{"name": "userId"}, "bad"})) != 1 {
		t.Fatal("expected only object parameters to be extracted")
	}
	if stringValue(1) != "" {
		t.Fatal("expected non-string value to map to empty string")
	}
	if clone["nested"].(map[string]any)["value"] != "y" {
		t.Fatal("expected clone mutation to succeed")
	}

	if _, err := tools.ResolveRef(spec, "https://example.com/ref"); err == nil {
		t.Fatal("expected non-local ref to fail")
	}
	if _, err := tools.ResolveRef(spec, "#/components/schemas/Missing"); err == nil {
		t.Fatal("expected missing ref to fail")
	}
	if _, err := tools.PrepareSchema(spec, map[string]any{"$ref": "#/components/schemas/Missing"}); err == nil {
		t.Fatal("expected prepare schema with bad ref to fail")
	}
	if tools.PickJSONMedia(map[string]any{"text/plain": "nope"}) != nil {
		t.Fatal("expected non-JSON media selection to return nil")
	}
	if normalized := tools.NormalizePayloadForSchema("value", map[string]any{"type": "object"}); normalized != "value" {
		t.Fatalf("expected invalid object payload to pass through unchanged: %#v", normalized)
	}
	if normalized := tools.NormalizePayloadForSchema("value", map[string]any{"type": "array"}); normalized != "value" {
		t.Fatalf("expected invalid array payload to pass through unchanged: %#v", normalized)
	}
}

func TestRegistryLoadersAndLookupBranches(t *testing.T) {
	tmp := t.TempDir()
	endpointsDir := filepath.Join(tmp, "endpoints", "accounts")
	masterDir := filepath.Join(tmp, "master_accounts", "admin")
	webhooksDir := filepath.Join(tmp, "webhooks", "events")
	for _, dir := range []string{endpointsDir, masterDir, webhooksDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	writeJSONFile := func(path string, payload map[string]any) {
		content, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal json: %v", err)
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}
	}

	endpointSpec := map[string]any{
		"info":    map[string]any{"title": "Users"},
		"servers": []any{map[string]any{"url": "https://alt.zoom.us/v2"}},
		"paths": map[string]any{
			"/users/{userId}": map[string]any{
				"get": map[string]any{
					"operationId": "getUser",
					"parameters": []any{
						map[string]any{"name": "userId", "in": "path"},
					},
					"responses": map[string]any{
						"default": map[string]any{
							"content": map[string]any{
								"application/vnd.zoom+json": map[string]any{
									"schema": map[string]any{
										"type":       "object",
										"properties": map[string]any{"id": map[string]any{"type": "string"}},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	webhookSpec := map[string]any{
		"info": map[string]any{"title": "Events"},
		"webhooks": map[string]any{
			"meeting.started": map[string]any{
				"post": map[string]any{
					"operationId": "meetingStartedA",
					"requestBody": map[string]any{
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type":       "object",
									"properties": map[string]any{"event": map[string]any{"type": "string"}},
									"required":   []any{"event"},
								},
							},
						},
					},
				},
			},
		},
	}
	writeJSONFile(filepath.Join(endpointsDir, "Users.json"), endpointSpec)
	writeJSONFile(filepath.Join(masterDir, "Users.json"), endpointSpec)
	writeJSONFile(filepath.Join(webhooksDir, "Events.json"), webhookSpec)
	writeJSONFile(filepath.Join(endpointsDir, "Ignored.json"), map[string]any{
		"info": map[string]any{"title": "Ignored"},
		"paths": map[string]any{
			"/ignored": []any{"not-a-map"},
		},
	})

	loadedPaths, err := loadPathOperations(os.DirFS(tmp), "endpoints/accounts/Users.json")
	if err != nil || len(loadedPaths) != 1 {
		t.Fatalf("loadPathOperations failed: %v %#v", err, loadedPaths)
	}
	if loadedPaths[0].ServerURL != "https://alt.zoom.us/v2" || len(loadedPaths[0].Parameters) != 1 {
		t.Fatalf("unexpected loaded path operation: %#v", loadedPaths[0])
	}

	loadedHooks, err := loadWebhookOperations(os.DirFS(tmp), "webhooks/events/Events.json")
	if err != nil || len(loadedHooks) != 1 {
		t.Fatalf("loadWebhookOperations failed: %v %#v", err, loadedHooks)
	}

	registry, err := NewSchemaRegistry(tmp)
	if err != nil {
		t.Fatalf("new schema registry: %v", err)
	}
	if len(registry.operations) != 2 {
		t.Fatalf("expected endpoint + master-account operations, got %d", len(registry.operations))
	}
	if registry.BaseURLForRequest("GET", "/users/me", "https://fallback.example") != "https://alt.zoom.us/v2" {
		t.Fatal("expected operation server URL")
	}
	if registry.BaseURLForRequest("POST", "/missing", "https://fallback.example") != "https://fallback.example" {
		t.Fatal("expected fallback base URL")
	}
	if _, err := registry.FindOperation("POST", "/missing"); err == nil {
		t.Fatal("expected missing operation error")
	}
	if err := registry.ValidateResponse("GET", "/users/me", 200, map[string]any{"id": "123"}); err != nil {
		t.Fatalf("expected default response schema to validate: %v", err)
	}
	if err := registry.ValidateResponse("GET", "/users/me", 204, nil); err == nil {
		t.Fatal("expected undocumented status to fail")
	}

	webhooks, err := NewWebhookRegistry(tmp)
	if err != nil {
		t.Fatalf("new webhook registry: %v", err)
	}
	webhooks.operations = append(webhooks.operations, WebhookOperation{
		SchemaName:    "AltEvents",
		EventName:     "meeting.started",
		OperationID:   "meetingStartedB",
		RequestSchema: loadedHooks[0].RequestSchema,
		Spec:          webhookSpec,
	})
	if err := webhooks.ValidateWebhook("unknown", map[string]any{}, "", ""); err == nil {
		t.Fatal("expected unknown webhook to fail")
	}
	if err := webhooks.ValidateWebhook("meeting.started", map[string]any{"event": "meeting.started"}, "", ""); err == nil {
		t.Fatal("expected ambiguous webhook lookup to fail")
	}
	if err := webhooks.ValidateWebhook("meeting.started", map[string]any{"event": "meeting.started"}, "Events", ""); err != nil {
		t.Fatalf("expected narrowed webhook validation to succeed: %v", err)
	}

	if _, err := loadJSON(os.DirFS(tmp), "missing.json"); err == nil {
		t.Fatal("expected missing JSON file to fail")
	}
	invalidJSON := filepath.Join(tmp, "invalid.json")
	if err := os.WriteFile(invalidJSON, []byte("{"), 0o644); err != nil {
		t.Fatalf("write invalid json: %v", err)
	}
	if _, err := loadJSON(os.DirFS(tmp), "invalid.json"); err == nil {
		t.Fatal("expected invalid JSON payload to fail")
	}

	emptyWebhookSpec := filepath.Join(webhooksDir, "Empty.json")
	writeJSONFile(emptyWebhookSpec, map[string]any{
		"info": map[string]any{"title": "EmptyHooks"},
		"webhooks": map[string]any{
			"meeting.ended": map[string]any{
				"post": map[string]any{
					"requestBody": map[string]any{
						"content": map[string]any{
							"text/plain": map[string]any{},
						},
					},
				},
			},
		},
	})
	emptyHooks, err := loadWebhookOperations(os.DirFS(tmp), "webhooks/events/Empty.json")
	if err != nil {
		t.Fatalf("load empty webhook operations: %v", err)
	}
	if len(emptyHooks) != 0 {
		t.Fatalf("expected empty webhook operation set, got %#v", emptyHooks)
	}
	noSchemaWebhookSpec := filepath.Join(webhooksDir, "NoSchema.json")
	writeJSONFile(noSchemaWebhookSpec, map[string]any{
		"info": map[string]any{"title": "NoSchemaHooks"},
		"webhooks": map[string]any{
			"meeting.created": map[string]any{
				"post": map[string]any{
					"requestBody": map[string]any{
						"content": map[string]any{
							"application/json": map[string]any{},
						},
					},
				},
			},
		},
	})
	noSchemaHooks, err := loadWebhookOperations(os.DirFS(tmp), "webhooks/events/NoSchema.json")
	if err != nil {
		t.Fatalf("load no-schema webhook operations: %v", err)
	}
	if len(noSchemaHooks) != 0 {
		t.Fatalf("expected webhook with missing schema to be skipped, got %#v", noSchemaHooks)
	}

	op := &SchemaOperation{
		Method:       "GET",
		TemplatePath: "/users/{userId}",
		Responses: map[string]any{
			"200": "bad",
			"default": map[string]any{
				"content": map[string]any{
					"text/plain": map[string]any{},
				},
			},
		},
	}
	if schema, err := pickResponseSchema(&OpenAPITools{}, op, 200); err != nil || schema != nil {
		t.Fatalf("expected invalid 200 response mapping to return nil schema, got %#v %v", schema, err)
	}
	if schema, err := pickResponseSchema(&OpenAPITools{}, op, 404); err != nil || schema != nil {
		t.Fatalf("expected non-JSON default response mapping to return nil schema, got %#v %v", schema, err)
	}

	noSchemaRegistry := &SchemaRegistry{
		operations: []SchemaOperation{{
			Method:       "GET",
			TemplatePath: "/health",
			PathRegex:    templatePathToRegex("/health"),
			Responses: map[string]any{
				"200": map[string]any{
					"content": map[string]any{
						"text/plain": map[string]any{},
					},
				},
			},
			Spec: map[string]any{},
		}},
		validator: &SchemaValidator{tools: &OpenAPITools{}},
	}
	if err := noSchemaRegistry.ValidateResponse("GET", "/health", 200, "ok"); err != nil {
		t.Fatalf("expected nil response schema to bypass validation: %v", err)
	}
	if err := noSchemaRegistry.ValidateResponse("GET", "/missing", 200, "ok"); err == nil {
		t.Fatal("expected missing operation response validation to fail")
	}
}

func TestClientConstructorsAndRequestBranches(t *testing.T) {
	settings := DefaultSettings()
	client, err := NewClient(settings, "token")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if client.SDK == nil {
		t.Fatal("expected SDK to be initialized")
	}

	t.Setenv("ZOOM_BASE_URL", settings.BaseURL)
	t.Setenv("ZOOM_OAUTH_URL", settings.OAuthURL)
	t.Setenv("ZOOM_TOKEN_SKEW_SECONDS", "60")
	fromEnv, err := NewClientFromEnvironment(false, "token")
	if err != nil {
		t.Fatalf("new client from env: %v", err)
	}
	if fromEnv.SDK == nil {
		t.Fatal("expected SDK from env")
	}
	badSettings := settings
	badSettings.BaseURL = "http://api.zoom.us/v2"
	if _, err := NewClient(badSettings, "token"); err == nil {
		t.Fatal("expected invalid settings to fail new client")
	}
	t.Setenv("ZOOM_BASE_URL", "http://api.zoom.us/v2")
	if _, err := NewClientFromEnvironment(false, "token"); err == nil {
		t.Fatal("expected invalid environment settings to fail new client from env")
	}
	t.Setenv("ZOOM_BASE_URL", settings.BaseURL)

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
								},
							},
						},
					},
					"204": map[string]any{},
				},
				Spec:      map[string]any{},
				ServerURL: "https://api.zoom.us/v2",
			},
		},
		validator: &SchemaValidator{tools: &OpenAPITools{}},
	}
	webhooks := &WebhookRegistry{
		operations: []WebhookOperation{
			{
				EventName:     "meeting.started",
				RequestSchema: map[string]any{"type": "object", "properties": map[string]any{"event": map[string]any{"type": "string"}}, "required": []any{"event"}},
				Spec:          map[string]any{},
			},
		},
		validator: &SchemaValidator{tools: &OpenAPITools{}},
	}
	callCount := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		callCount++
		switch callCount {
		case 1:
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       io.NopCloser(strings.NewReader(`{"error":"retry"}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		case 2:
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"id":"123"}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		default:
			return &http.Response{
				StatusCode: http.StatusNoContent,
				Body:       io.NopCloser(strings.NewReader("")),
				Header:     http.Header{},
			}, nil
		}
	})}
	testClient := &Client{
		settings:        settings,
		httpClient:      httpClient,
		logger:          DefaultLogger(),
		schemaRegistry:  registry,
		webhookRegistry: webhooks,
		tokenManager:    NewTokenManager(httpClient, settings, "token", DefaultLogger()),
	}
	testClient.settings.MaxRetries = 1
	testClient.settings.BackoffBaseSeconds = 0.0001
	testClient.settings.BackoffMaxSeconds = 0.0001

	value, err := testClient.Request(context.Background(), "GET", "/users/{userId}", RequestOptions{
		PathParams: map[string]string{"userId": "me"},
		Query:      map[string]any{"page_size": 30},
	})
	if err != nil {
		t.Fatalf("request after retry: %v", err)
	}
	if value.(map[string]any)["id"] != "123" {
		t.Fatalf("unexpected request payload: %#v", value)
	}
	if _, err := testClient.Request(context.Background(), "GET", "/users/{userId}", RequestOptions{
		PathParams: map[string]string{"userId": "me"},
	}); err != nil {
		t.Fatalf("expected 204 path to validate: %v", err)
	}
	if err := testClient.ValidateWebhook("meeting.started", map[string]any{"event": "meeting.started"}, "", ""); err != nil {
		t.Fatalf("validate webhook through client: %v", err)
	}

	if !isRetriableStatus(http.StatusTooManyRequests) || isRetriableStatus(http.StatusCreated) {
		t.Fatal("unexpected retriable status logic")
	}
	if !isRetriableMethod(http.MethodGet) || isRetriableMethod(http.MethodPost) {
		t.Fatal("unexpected retriable method logic")
	}
	if backoff := testClient.calculateBackoff(5); backoff <= 0 || backoff > 2*time.Millisecond {
		t.Fatalf("unexpected backoff duration: %v", backoff)
	}

	badJSONClient := &Client{
		settings: settings,
		logger:   DefaultLogger(),
		schemaRegistry: &SchemaRegistry{
			operations: []SchemaOperation{{
				Method:       "GET",
				TemplatePath: "/users/{userId}",
				PathRegex:    templatePathToRegex("/users/{userId}"),
				Responses: map[string]any{"200": map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id": map[string]any{"type": "string"},
					},
					"required": []any{"id"},
				}}}}},
				Spec: map[string]any{},
			}},
			validator: &SchemaValidator{tools: &OpenAPITools{}},
		},
	}
	if _, err := badJSONClient.handleResponse("GET", "/users/me", "https://api.zoom.us/v2/users/me", &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("{")),
		Header:     http.Header{},
	}, time.Now()); err == nil {
		t.Fatal("expected invalid JSON response to fail")
	}
	if _, err := badJSONClient.handleResponse("GET", "/users/me", "https://api.zoom.us/v2/users/me", &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"wrong":true}`)),
		Header:     http.Header{},
	}, time.Now()); err == nil {
		t.Fatal("expected schema validation to fail")
	}
	if _, err := badJSONClient.handleResponse("GET", "/users/me", "https://api.zoom.us/v2/users/me", &http.Response{
		StatusCode: http.StatusBadGateway,
		Body:       io.NopCloser(strings.NewReader("bad gateway")),
		Header:     http.Header{},
	}, time.Now()); err == nil {
		t.Fatal("expected non-2xx response to fail")
	}

	transportFailureClient := &Client{
		settings: settings,
		httpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return nil, errors.New("network down")
		})},
		logger:         DefaultLogger(),
		schemaRegistry: registry,
		tokenManager:   NewTokenManager(&http.Client{}, settings, "token", DefaultLogger()),
	}
	transportFailureClient.settings.MaxRetries = 0
	if _, err := transportFailureClient.Request(context.Background(), "GET", "/users/{userId}", RequestOptions{
		PathParams: map[string]string{"userId": "me"},
	}); err == nil {
		t.Fatal("expected transport failure")
	}

	bodyFailureClient := &Client{
		settings:        settings,
		httpClient:      httpClient,
		logger:          DefaultLogger(),
		schemaRegistry:  registry,
		webhookRegistry: webhooks,
		tokenManager:    NewTokenManager(httpClient, settings, "token", DefaultLogger()),
	}
	if _, err := bodyFailureClient.Request(context.Background(), "POST", "/users/{userId}", RequestOptions{
		PathParams: map[string]string{"userId": "me"},
		JSONBody:   map[string]any{"invalid": math.NaN()},
	}); err == nil {
		t.Fatal("expected unsupported JSON body to fail")
	}

	if _, err := buildURL("://bad", "/users", nil); err == nil {
		t.Fatal("expected invalid base URL to fail")
	}

	headerFailureClient := &Client{
		tokenManager: NewTokenManager(&http.Client{}, settings, "", DefaultLogger()),
	}
	if _, err := headerFailureClient.buildHeaders(context.Background(), nil); err == nil {
		t.Fatal("expected buildHeaders to fail without token credentials")
	}

	emptyBodyClient := &Client{
		settings: settings,
		logger:   DefaultLogger(),
		schemaRegistry: &SchemaRegistry{
			operations: []SchemaOperation{{
				Method:       "GET",
				TemplatePath: "/users/{userId}",
				PathRegex:    templatePathToRegex("/users/{userId}"),
				Responses:    map[string]any{"200": map[string]any{"content": map[string]any{"text/plain": map[string]any{}}}},
				Spec:         map[string]any{},
			}},
			validator: &SchemaValidator{tools: &OpenAPITools{}},
		},
	}
	if _, err := emptyBodyClient.handleResponse("GET", "/users/me", "https://api.zoom.us/v2/users/me", &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     http.Header{},
	}, time.Now()); err != nil {
		t.Fatalf("expected empty body response to validate as nil payload: %v", err)
	}
}

func TestConstructorAndRegistryFailureBranches(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module example.com/bad\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "internal", "parity", "schemas", "endpoints"), 0o755); err != nil {
		t.Fatalf("mkdir endpoints: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "internal", "parity", "schemas", "webhooks"), 0o755); err != nil {
		t.Fatalf("mkdir webhooks: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "internal", "parity", "schemas", "endpoints", "bad.json"), []byte("{"), 0o644); err != nil {
		t.Fatalf("write bad endpoint schema: %v", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	if _, err := NewSchemaRegistry(filepath.Join(tmp, "internal", "parity", "schemas")); err == nil {
		t.Fatal("expected explicit invalid endpoint schema root to fail")
	}

	if err := os.Remove(filepath.Join(tmp, "internal", "parity", "schemas", "endpoints", "bad.json")); err != nil {
		t.Fatalf("remove bad endpoint schema: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "internal", "parity", "schemas", "webhooks", "bad.json"), []byte("{"), 0o644); err != nil {
		t.Fatalf("write bad webhook schema: %v", err)
	}
	if _, err := NewWebhookRegistry(filepath.Join(tmp, "internal", "parity", "schemas")); err == nil {
		t.Fatal("expected explicit invalid webhook schema root to fail")
	}
	if _, err := NewClient(DefaultSettings(), "token"); err != nil {
		t.Fatalf("expected new client to use embedded schemas instead of cwd fixtures: %v", err)
	}
}

func TestSDKOperationHelpers(t *testing.T) {
	requests := 0
	settings := DefaultSettings()
	registry := &SchemaRegistry{
		operations: []SchemaOperation{
			{
				Method:       "GET",
				TemplatePath: "/users",
				PathRegex:    templatePathToRegex("/users"),
				Responses: map[string]any{
					"200": map[string]any{
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"users": map[string]any{
											"type": "array",
											"items": map[string]any{
												"type":       "object",
												"properties": map[string]any{"id": map[string]any{"type": "string"}},
											},
										},
										"next_page_token": map[string]any{"type": "string"},
									},
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
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		body := `{"users":[{"id":"1"}],"next_page_token":"token-2","total_records":2}`
		if strings.Contains(r.URL.RawQuery, "next_page_token=token-2") {
			body = `{"users":[{"id":"2"}],"total_records":2}`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		}, nil
	})}
	client := &Client{
		settings:       settings,
		httpClient:     httpClient,
		logger:         DefaultLogger(),
		schemaRegistry: registry,
		tokenManager:   NewTokenManager(httpClient, settings, "token", DefaultLogger()),
	}
	operation := &SDKOperation{
		client:      client,
		Path:        "/users",
		HTTPMethod:  "GET",
		OperationID: "listUsers",
	}
	raw, err := operation.Raw(context.Background(), nil, map[string]any{"page_size": 1}, nil, nil)
	if err != nil {
		t.Fatalf("raw request: %v", err)
	}
	if raw.(map[string]any)["next_page_token"] != "token-2" {
		t.Fatalf("unexpected raw payload: %#v", raw)
	}
	pages, err := operation.Paginate(context.Background(), nil, map[string]any{"page_size": 1}, nil)
	if err != nil {
		t.Fatalf("paginate: %v", err)
	}
	if len(pages) != 2 || requests < 3 {
		t.Fatalf("expected two pages and repeated requests, pages=%d requests=%d", len(pages), requests)
	}
	items, err := operation.IterAll(context.Background(), nil, map[string]any{"page_size": 1}, nil)
	if err != nil {
		t.Fatalf("iter all: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected flattened items, got %#v", items)
	}
	if operation.DebugDescribeOperation() == "" {
		t.Fatal("expected debug operation description")
	}
	if exportName("phone-users list") != "PhoneUsersList" {
		t.Fatalf("unexpected export name: %s", exportName("phone-users list"))
	}
	if firstString(map[string]any{"a": "value"}, "b", "a") != "value" {
		t.Fatal("expected firstString fallback")
	}
	if firstInt(map[string]any{"a": json.Number("2")}, "a") != 2 {
		t.Fatal("expected json.Number to parse")
	}
	if len(stringSlice([]any{"a", 1, "b"})) != 2 {
		t.Fatal("expected string slice conversion")
	}
	if stringSlice("bad") != nil {
		t.Fatal("expected nil string slice for invalid input")
	}
	if len(stringSlice([]string{"a", "b"})) != 2 {
		t.Fatal("expected []string conversion to pass through")
	}
	if firstInt(map[string]any{"a": 2}, "a") != 2 {
		t.Fatal("expected int passthrough")
	}
	if firstInt(map[string]any{"a": 3.0}, "a") != 3 {
		t.Fatal("expected float to convert to int")
	}
	if firstInt(map[string]any{"a": "bad"}, "a") != 0 {
		t.Fatal("expected invalid int conversion to fall back to zero")
	}
	if GoName("") != "" {
		t.Fatal("expected empty GoName output")
	}
	if exportName("") != "" {
		t.Fatal("expected empty export name")
	}

	failingOperation := &SDKOperation{
		client:      &Client{schemaRegistry: registry, tokenManager: NewTokenManager(httpClient, settings, "token", DefaultLogger()), httpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) { return nil, errors.New("boom") })}, logger: DefaultLogger(), settings: settings},
		Path:        "/users",
		HTTPMethod:  "GET",
		OperationID: "listUsers",
	}
	if _, err := failingOperation.IterAll(context.Background(), nil, nil, nil); err == nil {
		t.Fatal("expected iter all to fail when paginate fails")
	}

	if page := buildSDKPage("plain-string"); page.Value != "plain-string" || page.Items != nil {
		t.Fatalf("expected passthrough sdk page for non-object payload: %#v", page)
	}
	if items := preferredCollection(map[string]any{"total_records": 2}); items != nil {
		t.Fatalf("expected nil preferred collection, got %#v", items)
	}
}

func TestMiscCoverageBranches(t *testing.T) {
	if _, err := GoldenPublicSurface(); err != nil {
		t.Fatalf("expected vendored golden surface to load: %v", err)
	}

	if _, err := validateHTTPSURL("https://", "base_url"); err == nil {
		t.Fatal("expected missing hostname to fail")
	}
	if _, err := validateHTTPSURL("://bad", "base_url"); err == nil {
		t.Fatal("expected malformed URL to fail")
	}

	tmp := t.TempDir()
	envPath := filepath.Join(tmp, ".env")
	if err := os.WriteFile(envPath, []byte("quoted='value'\n# comment\nINVALID\n"), 0o644); err != nil {
		t.Fatalf("write env file: %v", err)
	}
	values := map[string]string{}
	if err := loadDotenvFile(envPath, values); err != nil {
		t.Fatalf("load dotenv: %v", err)
	}
	if values["quoted"] != "value" {
		t.Fatalf("expected trimmed quoted value, got %#v", values)
	}

	validator := &SchemaValidator{tools: &OpenAPITools{}}
	err := validator.ValidatePayload(map[string]any{}, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"a": map[string]any{"type": "string"},
			"b": map[string]any{"type": "string"},
			"c": map[string]any{"type": "string"},
			"d": map[string]any{"type": "string"},
			"e": map[string]any{"type": "string"},
			"f": map[string]any{"type": "string"},
		},
		"required": []any{"a", "b", "c", "d", "e", "f"},
	}, map[string]any{}, "ctx")
	if err == nil || !strings.Contains(err.Error(), "ctx:") {
		t.Fatalf("expected limited validation error output, got %v", err)
	}

	if shouldDropEmptyOptionalEnumValue("status", "", map[string]any{"type": "integer", "enum": []any{"1"}}, map[string]bool{}) {
		t.Fatal("expected non-string property schema to skip drop")
	}
	if shouldDropEmptyOptionalEnumValue("status", "", map[string]any{"type": "string", "enum": []any{"active"}}, map[string]bool{"status": true}) {
		t.Fatal("expected required enum field to skip drop")
	}
	if shouldDropEmptyOptionalEnumValue("status", "active", map[string]any{"type": "string", "enum": []any{"active"}}, map[string]bool{}) {
		t.Fatal("expected non-empty value to skip drop")
	}
	if shouldDropEmptyOptionalEnumValue("status", "", map[string]any{"type": "string"}, map[string]bool{}) {
		t.Fatal("expected missing enum to skip drop")
	}

	rendered, err := renderPath("users/{userId}", map[string]string{"userId": "me"})
	if err != nil || rendered != "/users/me" {
		t.Fatalf("expected renderPath to add leading slash, got %q %v", rendered, err)
	}
	if boolValue(false) {
		t.Fatal("expected boolValue false passthrough")
	}

	transportClient := &Client{
		settings: DefaultSettings(),
		httpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return nil, errors.New("still down")
		})},
		logger: DefaultLogger(),
		schemaRegistry: &SchemaRegistry{
			operations: []SchemaOperation{{
				Method:       "GET",
				TemplatePath: "/users/{userId}",
				PathRegex:    templatePathToRegex("/users/{userId}"),
				Responses:    map[string]any{"200": map[string]any{}},
				Spec:         map[string]any{},
			}},
			validator: &SchemaValidator{tools: &OpenAPITools{}},
		},
		tokenManager: NewTokenManager(&http.Client{}, DefaultSettings(), "token", DefaultLogger()),
	}
	transportClient.settings.MaxRetries = 1
	transportClient.settings.BackoffBaseSeconds = 0.0001
	transportClient.settings.BackoffMaxSeconds = 0.0001
	if _, err := transportClient.Request(context.Background(), "GET", "/users/{userId}", RequestOptions{
		PathParams: map[string]string{"userId": "me"},
	}); err == nil {
		t.Fatal("expected retried transport failure")
	}
}
