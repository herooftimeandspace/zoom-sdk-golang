package zoomsdk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenAPIToolsPickJSONMedia(t *testing.T) {
	tools := &OpenAPITools{}
	content := map[string]any{
		"text/plain":            map[string]any{},
		"application/scim+json": map[string]any{"schema": map[string]any{"type": "object"}},
	}
	media := tools.PickJSONMedia(content)
	if media == nil {
		t.Fatal("expected JSON-like media to be selected")
	}
}

func TestOpenAPIToolsResolveRefFallsBackToWebhooks(t *testing.T) {
	tools := &OpenAPITools{}
	spec := map[string]any{
		"webhooks": map[string]any{
			"meeting.started": map[string]any{
				"post": map[string]any{
					"requestBody": map[string]any{},
				},
			},
		},
	}
	value, err := tools.ResolveRef(spec, "#/paths/meeting.started/post/requestBody")
	if err != nil {
		t.Fatalf("unexpected ref resolution error: %v", err)
	}
	if _, ok := value.(map[string]any); !ok {
		t.Fatalf("unexpected resolved value: %#v", value)
	}
}

func TestOpenAPIToolsNormalizeSchemaAddsSyntheticRequiredProperties(t *testing.T) {
	tools := &OpenAPITools{}
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{},
		"required":   []any{"id"},
	}
	normalized := tools.NormalizeSchema(schema).(map[string]any)
	properties := normalized["properties"].(map[string]any)
	if _, ok := properties["id"]; !ok {
		t.Fatal("expected required property to be synthesized")
	}
}

func TestOpenAPIToolsNormalizePayloadDropsEmptyOptionalEnum(t *testing.T) {
	tools := &OpenAPITools{}
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"status": map[string]any{
				"type": "string",
				"enum": []any{"active", "inactive"},
			},
		},
	}
	payload := map[string]any{"status": ""}
	normalized := tools.NormalizePayloadForSchema(payload, schema).(map[string]any)
	if _, ok := normalized["status"]; ok {
		t.Fatal("expected empty optional enum value to be dropped")
	}
}

func TestSchemaRegistryValidateResponseAndWebhookRegistryValidate(t *testing.T) {
	tmp := t.TempDir()
	endpointsDir := filepath.Join(tmp, "endpoints", "accounts")
	webhooksDir := filepath.Join(tmp, "webhooks", "workplace")
	if err := os.MkdirAll(endpointsDir, 0o755); err != nil {
		t.Fatalf("mkdir endpoints: %v", err)
	}
	if err := os.MkdirAll(webhooksDir, 0o755); err != nil {
		t.Fatalf("mkdir webhooks: %v", err)
	}

	endpointSpec := map[string]any{
		"openapi": "3.0.0",
		"info":    map[string]any{"title": "Users"},
		"servers": []any{map[string]any{"url": "https://api.zoom.us/v2"}},
		"paths": map[string]any{
			"/users/{userId}": map[string]any{
				"get": map[string]any{
					"operationId": "getUser",
					"responses": map[string]any{
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
				},
			},
		},
	}
	webhookSpec := map[string]any{
		"openapi": "3.1.0",
		"info":    map[string]any{"title": "Meetings"},
		"webhooks": map[string]any{
			"meeting.started": map[string]any{
				"post": map[string]any{
					"operationId": "meetingStartedWebhook",
					"requestBody": map[string]any{
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"event": map[string]any{"type": "string"},
									},
									"required": []any{"event"},
								},
							},
						},
					},
				},
			},
		},
	}
	writeJSON := func(path string, payload map[string]any) {
		content, _ := json.Marshal(payload)
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatalf("write json: %v", err)
		}
	}
	writeJSON(filepath.Join(endpointsDir, "Users.json"), endpointSpec)
	writeJSON(filepath.Join(webhooksDir, "Meetings.json"), webhookSpec)

	schemaRegistry, err := NewSchemaRegistry(tmp)
	if err != nil {
		t.Fatalf("new schema registry: %v", err)
	}
	if err := schemaRegistry.ValidateResponse("GET", "/users/me", 200, map[string]any{"id": "123"}); err != nil {
		t.Fatalf("unexpected response validation error: %v", err)
	}
	if err := schemaRegistry.ValidateResponse("GET", "/users/me", 200, map[string]any{}); err == nil {
		t.Fatal("expected invalid response payload to fail")
	}

	webhookRegistry, err := NewWebhookRegistry(tmp)
	if err != nil {
		t.Fatalf("new webhook registry: %v", err)
	}
	if err := webhookRegistry.ValidateWebhook("meeting.started", map[string]any{"event": "meeting.started"}, "", ""); err != nil {
		t.Fatalf("unexpected webhook validation error: %v", err)
	}
}
