package zoomsdk

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestGoldenPublicSurfaceLoadsAndSDKBuildsInventory(t *testing.T) {
	golden, err := GoldenPublicSurface()
	if err != nil {
		t.Fatalf("load golden: %v", err)
	}
	if len(golden) != 6882 {
		t.Fatalf("expected vendored golden surface count to match Python, got %d", len(golden))
	}

	client := &Client{}
	sdk, err := NewSDK(client)
	if err != nil {
		t.Fatalf("new sdk: %v", err)
	}
	if _, ok := sdk.Operation("users.list"); !ok {
		t.Fatal("expected users.list to exist in generated inventory")
	}
	if len(sdk.Inventory()) == 0 {
		t.Fatal("expected non-empty inventory")
	}
	metadata := sdk.operations["users.get"].Metadata()
	if metadata.HTTPMethod != "GET" || metadata.Path != "/users/{userId}" || metadata.OperationID != "user" {
		t.Fatalf("unexpected users.get metadata: %#v", metadata)
	}
}

func TestBuildSDKPageAndHelpers(t *testing.T) {
	page := buildSDKPage(map[string]any{
		"users":           []any{map[string]any{"id": "1"}, map[string]any{"id": "2"}},
		"next_page_token": "token-2",
		"total_records":   2.0,
	})
	if page.NextPageToken != "token-2" {
		t.Fatalf("unexpected next page token: %s", page.NextPageToken)
	}
	if len(page.Items) != 2 {
		t.Fatalf("unexpected items: %#v", page.Items)
	}
	if page.TotalRecords != 2 {
		t.Fatalf("unexpected total records: %d", page.TotalRecords)
	}
	if GoName("phone.users.get") != "PhoneUsersGet" {
		t.Fatalf("unexpected GoName output: %s", GoName("phone.users.get"))
	}
}

func TestSDKDiscoveryHelpersReturnStableOperations(t *testing.T) {
	sdk := &SDK{
		operations: map[string]*SDKOperation{
			"users.get":       {Namespace: []string{"users"}},
			"phone.users.get": {Namespace: []string{"phone", "users"}},
			"users.list":      {Namespace: []string{"users"}},
		},
	}
	namespaces := sdk.Namespaces()
	if strings.Join(namespaces, ",") != "phone,users" {
		t.Fatalf("unexpected namespaces: %#v", namespaces)
	}
	phoneOperations := sdk.NamespaceOperations("phone")
	if len(phoneOperations) != 1 || phoneOperations[0] != "phone.users.get" {
		t.Fatalf("unexpected phone operations: %#v", phoneOperations)
	}
}

func TestSDKOperationCallMapsParametersAndUsesDefaultAccountID(t *testing.T) {
	client, operation, calls := buildSDKCallTestClient(t, `{"id":"widget-1"}`)
	value, err := operation.Call(context.Background(), SDKCallOptions{
		Query: map[string]any{"include_inactive": true},
		JSONBody: map[string]any{
			"name": "front desk",
		},
	})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if *calls != 1 {
		t.Fatalf("expected one request, got %d", *calls)
	}
	payload := value.(map[string]any)
	if payload["id"] != "widget-1" {
		t.Fatalf("unexpected response payload: %#v", payload)
	}
	if client.DefaultAccountID() != "default-acct" {
		t.Fatalf("unexpected default account id: %s", client.DefaultAccountID())
	}
}

func TestSDKOperationCallExplicitAccountIDOverridesDefault(t *testing.T) {
	_, operation, _ := buildSDKCallTestClient(t, `{"id":"widget-1"}`)
	operation.client.httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v2/accounts/explicit-acct/widgets" {
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}
		return jsonResponse(http.StatusOK, `{"id":"widget-1"}`), nil
	})}
	_, err := operation.Call(context.Background(), SDKCallOptions{
		PathParams: map[string]string{"account_id": "explicit-acct"},
		JSONBody:   map[string]any{"name": "front desk"},
	})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
}

func TestSDKOperationCallRequiresMissingPathParameter(t *testing.T) {
	client, operation, _ := buildSDKCallTestClient(t, `{"id":"widget-1"}`)
	client.defaultAccount = ""
	client.settings.AccountID = ""
	_, err := operation.Call(context.Background(), SDKCallOptions{
		JSONBody: map[string]any{"name": "front desk"},
	})
	if err == nil || !strings.Contains(err.Error(), "missing required path parameter") {
		t.Fatalf("expected missing path parameter error, got %v", err)
	}
}

func TestSDKOperationCallValidatesRequestBodyBeforeSending(t *testing.T) {
	_, operation, calls := buildSDKCallTestClient(t, `{"id":"widget-1"}`)
	_, err := operation.Call(context.Background(), SDKCallOptions{
		JSONBody: map[string]any{"display_name": "missing required name"},
	})
	if err == nil || !strings.Contains(err.Error(), "request body") {
		t.Fatalf("expected request body validation error, got %v", err)
	}
	if *calls != 0 {
		t.Fatalf("expected request validation to fail before sending, got %d calls", *calls)
	}
}

func TestSDKOperationCallStillValidatesResponsesAfterReceiving(t *testing.T) {
	_, operation, calls := buildSDKCallTestClient(t, `{"unexpected":"shape"}`)
	_, err := operation.Call(context.Background(), SDKCallOptions{
		JSONBody: map[string]any{"name": "front desk"},
	})
	if err == nil || !strings.Contains(err.Error(), "missing required property id") {
		t.Fatalf("expected response validation error, got %v", err)
	}
	if *calls != 1 {
		t.Fatalf("expected response validation after one request, got %d calls", *calls)
	}
}

func buildSDKCallTestClient(t *testing.T, responseBody string) (*Client, *SDKOperation, *int) {
	t.Helper()
	spec := map[string]any{
		"openapi": "3.0.0",
		"info":    map[string]any{"title": "Widgets"},
	}
	requestSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
		"required": []any{"name"},
	}
	responseSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{"type": "string"},
		},
		"required": []any{"id"},
	}
	schemaOperation := SchemaOperation{
		SchemaName:   "Widgets",
		Method:       "POST",
		TemplatePath: "/accounts/{accountId}/widgets",
		PathRegex:    templatePathToRegex("/accounts/{accountId}/widgets"),
		OperationID:  "createWidget",
		Summary:      "Create widget",
		Parameters: []map[string]any{
			{"name": "accountId", "in": "path", "required": true},
			{"name": "includeInactive", "in": "query", "required": false},
		},
		HasRequestBody: true,
		RequestBody: map[string]any{
			"content": map[string]any{
				"application/json": map[string]any{"schema": requestSchema},
			},
		},
		Responses: map[string]any{
			"200": map[string]any{
				"content": map[string]any{
					"application/json": map[string]any{"schema": responseSchema},
				},
			},
		},
		Spec: spec,
	}
	registry := &SchemaRegistry{
		operations: []SchemaOperation{schemaOperation},
		validator:  &SchemaValidator{tools: &OpenAPITools{}},
	}
	settings := DefaultSettings()
	settings.AccountID = "default-acct"
	calls := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/v2/accounts/default-acct/widgets" {
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}
		if r.URL.RawQuery != "" && r.URL.Query().Get("includeInactive") != "true" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if !bytes.Contains(body, []byte(`"name":"front desk"`)) {
			t.Fatalf("unexpected request body: %s", string(body))
		}
		return jsonResponse(http.StatusOK, responseBody), nil
	})}
	client := &Client{
		settings:       settings,
		defaultAccount: settings.AccountID,
		httpClient:     httpClient,
		logger:         DefaultLogger(),
		schemaRegistry: registry,
		tokenManager:   NewTokenManager(httpClient, settings, "token", DefaultLogger()),
	}
	operation := &SDKOperation{
		client:      client,
		Name:        "accounts.account_id.widgets.create",
		Path:        "/accounts/{accountId}/widgets",
		HTTPMethod:  "POST",
		OperationID: "createWidget",
	}
	operation.enrichFromSchema()
	return client, operation, &calls
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
