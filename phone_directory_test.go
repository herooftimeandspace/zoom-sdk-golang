package zoomsdk

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"
)

func TestPhoneDirectoryPaginatesSupportedReaders(t *testing.T) {
	client := buildPhoneDirectoryTestClient(t)
	directory := client.PhoneDirectory()
	ctx := context.Background()

	tests := []struct {
		name       string
		path       string
		itemKey    string
		readPages  func(context.Context, PhoneDirectoryListOptions) ([]SDKPage, error)
		readAll    func(context.Context, PhoneDirectoryListOptions) ([]any, error)
		wantQuery  map[string]string
		omitQuery  []string
		firstLabel string
	}{
		{
			name:       "users",
			path:       "/v2/phone/users",
			itemKey:    "users",
			readPages:  directory.Users,
			readAll:    directory.AllUsers,
			wantQuery:  map[string]string{"page_size": "2", "site_id": "site-1", "calling_type": "100", "status": "activate", "department": "Technology", "cost_center": "IT", "keyword": "front"},
			firstLabel: "users-1",
		},
		{
			name:       "common areas",
			path:       "/v2/phone/common_areas",
			itemKey:    "common_areas",
			readPages:  directory.CommonAreas,
			readAll:    directory.AllCommonAreas,
			wantQuery:  map[string]string{"page_size": "2", "site_id": "site-1", "calling_type": "100", "common_area_device_type": "2"},
			omitQuery:  []string{"status", "department", "cost_center", "keyword"},
			firstLabel: "common_areas-1",
		},
		{
			name:       "shared line groups",
			path:       "/v2/phone/shared_line_groups",
			itemKey:    "shared_line_groups",
			readPages:  directory.SharedLineGroups,
			readAll:    directory.AllSharedLineGroups,
			wantQuery:  map[string]string{"page_size": "2"},
			omitQuery:  []string{"site_id", "calling_type", "status", "department", "cost_center", "keyword", "common_area_device_type"},
			firstLabel: "shared_line_groups-1",
		},
		{
			name:       "call queues",
			path:       "/v2/phone/call_queues",
			itemKey:    "call_queues",
			readPages:  directory.CallQueues,
			readAll:    directory.AllCallQueues,
			wantQuery:  map[string]string{"page_size": "2", "site_id": "site-1", "department": "Technology", "cost_center": "IT"},
			omitQuery:  []string{"calling_type", "status", "keyword", "common_area_device_type"},
			firstLabel: "call_queues-1",
		},
	}

	options := PhoneDirectoryListOptions{
		PageSize:             2,
		SiteID:               "site-1",
		CallingType:          100,
		Status:               "activate",
		Department:           "Technology",
		CostCenter:           "IT",
		Keyword:              "front",
		CommonAreaDeviceType: 2,
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pages, err := tt.readPages(ctx, options)
			if err != nil {
				t.Fatalf("read pages: %v", err)
			}
			if len(pages) != 2 {
				t.Fatalf("expected two pages, got %d", len(pages))
			}
			if pages[0].NextPageToken != "token-2" || pages[0].PageSize != 2 || pages[0].TotalRecords != 3 {
				t.Fatalf("unexpected first page metadata: %#v", pages[0])
			}
			if len(pages[0].Items) != 2 || len(pages[1].Items) != 1 {
				t.Fatalf("unexpected page items: %#v %#v", pages[0].Items, pages[1].Items)
			}
			first := pages[0].Items[0].(map[string]any)
			if first["id"] != tt.firstLabel {
				t.Fatalf("unexpected first item: %#v", first)
			}

			items, err := tt.readAll(ctx, options)
			if err != nil {
				t.Fatalf("read all: %v", err)
			}
			if len(items) != 3 {
				t.Fatalf("expected three flattened items, got %d", len(items))
			}

			assertPhoneDirectoryRequests(t, client, tt.path, tt.itemKey, tt.wantQuery, tt.omitQuery)
		})
	}
}

func TestPhoneDirectoryDocumentsLegacyCommonAreaPhonesDecision(t *testing.T) {
	directory := buildPhoneDirectoryTestClient(t).PhoneDirectory()
	if directory.SupportsLegacyCommonAreaPhones() {
		t.Fatal("legacy /phone/common_area_phones should not be advertised unless it appears in the generated inventory")
	}
	if operation, ok := directory.operationByPath(http.MethodGet, "/phone/users"); !ok || operation.Name != phoneUsersListOperation {
		t.Fatalf("expected current phone users operation lookup, got %#v %v", operation, ok)
	}
	if _, ok := directory.operationByPath(http.MethodGet, PhoneCommonAreaPhonesLegacyPath); ok {
		t.Fatal("legacy common-area phones path unexpectedly exists in the generated inventory")
	}
	operation, err := directory.operation(phoneCommonAreasListOperation)
	if err != nil {
		t.Fatalf("current common-area operation missing: %v", err)
	}
	if operation.Path != "/phone/common_areas" {
		t.Fatalf("unexpected common-area replacement path: %s", operation.Path)
	}
}

func TestPhoneDirectoryRequiresInitializedSDK(t *testing.T) {
	_, err := (&PhoneDirectory{}).Users(context.Background(), PhoneDirectoryListOptions{})
	if err == nil || !strings.Contains(err.Error(), "initialized SDK client") {
		t.Fatalf("expected initialized SDK error, got %v", err)
	}

	client := &Client{SDK: &SDK{operations: map[string]*SDKOperation{}}}
	_, err = client.PhoneDirectory().Users(context.Background(), PhoneDirectoryListOptions{})
	if err == nil || !strings.Contains(err.Error(), phoneUsersListOperation) {
		t.Fatalf("expected missing operation error, got %v", err)
	}
}

func TestPhoneDirectoryQuerySupportsInitialNextPageToken(t *testing.T) {
	query := (PhoneDirectoryListOptions{NextPageToken: "start", PageSize: 10}).query("next_page_token")
	if len(query) != 1 || query["next_page_token"] != "start" {
		t.Fatalf("unexpected next token query: %#v", query)
	}
	if empty := (PhoneDirectoryListOptions{}).query("page_size", "next_page_token"); empty != nil {
		t.Fatalf("expected empty query to be nil, got %#v", empty)
	}
}

func buildPhoneDirectoryTestClient(t *testing.T) *Client {
	t.Helper()
	settings := DefaultSettings()
	settings.BaseURL = "https://api.zoom.test/v2"
	requests := map[string][]url.Values{}
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if auth := r.Header.Get("Authorization"); auth != "Bearer token" {
			t.Fatalf("unexpected authorization header: %s", auth)
		}
		path := r.URL.Path
		query := r.URL.Query()
		requests[path] = append(requests[path], cloneValues(query))
		itemKey, ok := map[string]string{
			"/v2/phone/users":              "users",
			"/v2/phone/common_areas":       "common_areas",
			"/v2/phone/shared_line_groups": "shared_line_groups",
			"/v2/phone/call_queues":        "call_queues",
		}[path]
		if !ok {
			t.Fatalf("unexpected request path: %s", path)
		}
		if query.Get("next_page_token") == "" {
			return jsonResponse(http.StatusOK, fmt.Sprintf(`{"%[1]s":[{"id":"%[1]s-1"},{"id":"%[1]s-2"}],"next_page_token":"token-2","page_size":2,"total_records":3}`, itemKey)), nil
		}
		if query.Get("next_page_token") != "token-2" {
			t.Fatalf("unexpected next page token: %s", query.Get("next_page_token"))
		}
		return jsonResponse(http.StatusOK, fmt.Sprintf(`{"%[1]s":[{"id":"%[1]s-3"}],"next_page_token":"","page_size":2,"total_records":3}`, itemKey)), nil
	})}
	registry := &SchemaRegistry{
		operations: phoneDirectorySchemaOperations(),
		validator:  &SchemaValidator{tools: &OpenAPITools{}},
	}
	client := &Client{
		settings:       settings,
		httpClient:     httpClient,
		logger:         DefaultLogger(),
		schemaRegistry: registry,
		tokenManager:   NewTokenManager(httpClient, settings, "token", DefaultLogger()),
	}
	sdk, err := NewSDK(client)
	if err != nil {
		t.Fatalf("new sdk: %v", err)
	}
	client.SDK = sdk
	t.Cleanup(func() {
		client.settings.BaseURL = settings.BaseURL
	})
	client.defaultAccount = settings.AccountID
	clientRequests.Store(client, requests)
	return client
}

func phoneDirectorySchemaOperations() []SchemaOperation {
	operations := []SchemaOperation{}
	for _, endpoint := range []struct {
		path    string
		itemKey string
	}{
		{path: "/phone/users", itemKey: "users"},
		{path: "/phone/common_areas", itemKey: "common_areas"},
		{path: "/phone/shared_line_groups", itemKey: "shared_line_groups"},
		{path: "/phone/call_queues", itemKey: "call_queues"},
	} {
		operations = append(operations, SchemaOperation{
			SchemaName:   "Phone",
			Method:       http.MethodGet,
			TemplatePath: endpoint.path,
			PathRegex:    templatePathToRegex(endpoint.path),
			OperationID:  endpoint.itemKey,
			Responses: map[string]any{
				"200": map[string]any{
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{
								"type": "object",
								"properties": map[string]any{
									endpoint.itemKey:     map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
									"next_page_token":    map[string]any{"type": "string"},
									"page_size":          map[string]any{"type": "integer"},
									"total_records":      map[string]any{"type": "integer"},
									"unexpected_ignored": map[string]any{"type": "string"},
								},
							},
						},
					},
				},
			},
			Spec: map[string]any{},
		})
	}
	return operations
}

func assertPhoneDirectoryRequests(t *testing.T, client *Client, path string, itemKey string, wantQuery map[string]string, omitQuery []string) {
	t.Helper()
	requests := phoneDirectoryRequests(t, client)
	values := requests[path]
	if len(values) != 4 {
		t.Fatalf("expected four requests for %s after page and all reads, got %d: %#v", path, len(values), values)
	}
	for _, index := range []int{0, 2} {
		for key, want := range wantQuery {
			if got := values[index].Get(key); got != want {
				t.Fatalf("%s request %d query %s: got %q want %q", itemKey, index, key, got, want)
			}
		}
		if got := values[index].Get("next_page_token"); got != "" {
			t.Fatalf("%s first page unexpectedly included next_page_token %q", itemKey, got)
		}
	}
	for _, index := range []int{1, 3} {
		if got := values[index].Get("next_page_token"); got != "token-2" {
			t.Fatalf("%s request %d next_page_token: got %q", itemKey, index, got)
		}
	}
	for _, valuesIndex := range []int{0, 1, 2, 3} {
		for _, key := range omitQuery {
			if got := values[valuesIndex].Get(key); got != "" {
				t.Fatalf("%s request %d should omit unsupported query %s=%q", itemKey, valuesIndex, key, got)
			}
		}
	}
}

var clientRequests phoneDirectoryRequestStore

type phoneDirectoryRequestStore struct {
	values map[*Client]map[string][]url.Values
}

func (s *phoneDirectoryRequestStore) Store(client *Client, requests map[string][]url.Values) {
	if s.values == nil {
		s.values = map[*Client]map[string][]url.Values{}
	}
	s.values[client] = requests
}

func phoneDirectoryRequests(t *testing.T, client *Client) map[string][]url.Values {
	t.Helper()
	requests := clientRequests.values[client]
	if requests == nil {
		t.Fatal("missing request capture")
	}
	return requests
}

func cloneValues(values url.Values) url.Values {
	cloned := url.Values{}
	for key, items := range values {
		cloned[key] = append([]string{}, items...)
		sort.Strings(cloned[key])
	}
	return cloned
}

func TestPhoneDirectoryHelpersDoNotLogPayloadsOrSecrets(t *testing.T) {
	var output strings.Builder
	client := buildPhoneDirectoryTestClient(t)
	client.logger = NewLogger(&output)
	if _, err := client.PhoneDirectory().Users(context.Background(), PhoneDirectoryListOptions{PageSize: 1}); err != nil {
		t.Fatalf("users: %v", err)
	}
	logs, err := io.ReadAll(strings.NewReader(output.String()))
	if err != nil {
		t.Fatalf("read logs: %v", err)
	}
	logText := string(logs)
	for _, sensitive := range []string{"Bearer token", "users-1", "users-2", "client_secret", "Authorization"} {
		if strings.Contains(logText, sensitive) {
			t.Fatalf("logs should not include %q: %s", sensitive, logText)
		}
	}
}
