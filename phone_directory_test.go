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

	for _, id := range phoneDirectoryReaderIDs {
		descriptor := requirePhoneDirectoryReader(t, id)
		t.Run(string(id), func(t *testing.T) {
			readPages, readAll := phoneDirectoryPublicReaders(directory, id)
			pages, err := readPages(ctx, options)
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
			if first["id"] != descriptor.itemKey+"-1" {
				t.Fatalf("unexpected first item: %#v", first)
			}

			items, err := readAll(ctx, options)
			if err != nil {
				t.Fatalf("read all: %v", err)
			}
			if len(items) != 3 {
				t.Fatalf("expected three flattened items, got %d", len(items))
			}

			assertPhoneDirectoryRequests(t, client, descriptor, options)
		})
	}
}

func TestPhoneDirectoryDescriptorsMatchGeneratedInventory(t *testing.T) {
	directory := buildPhoneDirectoryTestClient(t).PhoneDirectory()
	if len(phoneDirectoryReaderDescriptors) != len(phoneDirectoryReaderIDs) {
		t.Fatalf("descriptor count %d does not match public reader count %d", len(phoneDirectoryReaderDescriptors), len(phoneDirectoryReaderIDs))
	}
	for _, id := range phoneDirectoryReaderIDs {
		descriptor := requirePhoneDirectoryReader(t, id)
		operation, err := directory.operation(descriptor.operationName)
		if err != nil {
			t.Fatalf("%s operation: %v", id, err)
		}
		if operation.HTTPMethod != http.MethodGet || operation.Path != descriptor.path {
			t.Fatalf("%s descriptor route %s %s does not match inventory %s %s", id, http.MethodGet, descriptor.path, operation.HTTPMethod, operation.Path)
		}
		wantQuery := append([]string{}, descriptor.allowedQuery...)
		gotQuery := append([]string{}, operation.QueryParameters...)
		sort.Strings(wantQuery)
		sort.Strings(gotQuery)
		if strings.Join(gotQuery, ",") != strings.Join(wantQuery, ",") {
			t.Fatalf("%s descriptor query fields %v do not match inventory %v", id, wantQuery, gotQuery)
		}
	}
}

func TestPhoneDirectoryDocumentsLegacyCommonAreaPhonesDecision(t *testing.T) {
	directory := buildPhoneDirectoryTestClient(t).PhoneDirectory()
	if directory.SupportsLegacyCommonAreaPhones() {
		t.Fatal("legacy /phone/common_area_phones should not be advertised unless it appears in the generated inventory")
	}
	users := requirePhoneDirectoryReader(t, phoneDirectoryUsers)
	if operation, ok := directory.operationByPath(http.MethodGet, "/phone/users"); !ok || operation.Name != users.operationName {
		t.Fatalf("expected current phone users operation lookup, got %#v %v", operation, ok)
	}
	if _, ok := directory.operationByPath(http.MethodGet, PhoneCommonAreaPhonesLegacyPath); ok {
		t.Fatal("legacy common-area phones path unexpectedly exists in the generated inventory")
	}
	commonAreas := requirePhoneDirectoryReader(t, phoneDirectoryCommonAreas)
	operation, err := directory.operation(commonAreas.operationName)
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
	users := requirePhoneDirectoryReader(t, phoneDirectoryUsers)
	if err == nil || !strings.Contains(err.Error(), users.operationName) {
		t.Fatalf("expected missing operation error, got %v", err)
	}
}

func TestPhoneDirectoryRejectsUnregisteredReader(t *testing.T) {
	directory := buildPhoneDirectoryTestClient(t).PhoneDirectory()
	unknown := phoneDirectoryReaderID("Unknown")

	if _, err := directory.paginate(context.Background(), unknown, PhoneDirectoryListOptions{}); err == nil || !strings.Contains(err.Error(), "is not registered") {
		t.Fatalf("expected unregistered paginated reader error, got %v", err)
	}
	if _, err := directory.iterAll(context.Background(), unknown, PhoneDirectoryListOptions{}); err == nil || !strings.Contains(err.Error(), "is not registered") {
		t.Fatalf("expected unregistered flattened reader error, got %v", err)
	}
}

func TestPhoneDirectoryQuerySupportsInitialNextPageToken(t *testing.T) {
	descriptor := requirePhoneDirectoryReader(t, phoneDirectoryUsers)
	query := (PhoneDirectoryListOptions{NextPageToken: "start", PageSize: 10}).query(descriptor)
	if len(query) != 2 || query["next_page_token"] != "start" || query["page_size"] != 10 {
		t.Fatalf("unexpected next token query: %#v", query)
	}
	if empty := (PhoneDirectoryListOptions{}).query(descriptor); empty != nil {
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
		descriptor, ok := phoneDirectoryReaderByPath(strings.TrimPrefix(path, "/v2"))
		if !ok {
			t.Fatalf("unexpected request path: %s", path)
		}
		itemKey := descriptor.itemKey
		if query.Get("next_page_token") == "" {
			return jsonResponse(http.StatusOK, fmt.Sprintf(`{"%[1]s":[{"id":"%[1]s-1"},{"id":"%[1]s-2"}],"next_page_token":"token-2","page_size":2,"total_records":3}`, itemKey)), nil
		}
		if query.Get("next_page_token") != "token-2" {
			t.Fatalf("unexpected next page token: %s", query.Get("next_page_token"))
		}
		return jsonResponse(http.StatusOK, fmt.Sprintf(`{"%[1]s":[{"id":"%[1]s-3"}],"next_page_token":"","page_size":2,"total_records":3}`, itemKey)), nil
	})}
	registry := &SchemaRegistry{
		operations: phoneDirectorySchemaOperations(t),
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

func phoneDirectorySchemaOperations(t *testing.T) []SchemaOperation {
	t.Helper()
	operations := []SchemaOperation{}
	for _, id := range phoneDirectoryReaderIDs {
		descriptor := requirePhoneDirectoryReader(t, id)
		operations = append(operations, SchemaOperation{
			SchemaName:   "Phone",
			Method:       http.MethodGet,
			TemplatePath: descriptor.path,
			PathRegex:    templatePathToRegex(descriptor.path),
			OperationID:  descriptor.itemKey,
			Responses: map[string]any{
				"200": map[string]any{
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{
								"type": "object",
								"properties": map[string]any{
									descriptor.itemKey:   map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
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

func phoneDirectoryReaderByPath(path string) (phoneDirectoryReaderDescriptor, bool) {
	for _, id := range phoneDirectoryReaderIDs {
		descriptor, ok := phoneDirectoryReaderDescriptors[id]
		if !ok {
			continue
		}
		if descriptor.path == path {
			return descriptor, true
		}
	}
	return phoneDirectoryReaderDescriptor{}, false
}

func requirePhoneDirectoryReader(t *testing.T, id phoneDirectoryReaderID) phoneDirectoryReaderDescriptor {
	t.Helper()
	descriptor, err := phoneDirectoryReader(id)
	if err != nil {
		t.Fatal(err)
	}
	return descriptor
}

func assertPhoneDirectoryRequests(t *testing.T, client *Client, descriptor phoneDirectoryReaderDescriptor, options PhoneDirectoryListOptions) {
	t.Helper()
	requests := phoneDirectoryRequests(t, client)
	path := "/v2" + descriptor.path
	values := requests[path]
	if len(values) != 4 {
		t.Fatalf("expected four requests for %s after page and all reads, got %d: %#v", path, len(values), values)
	}
	for _, index := range []int{0, 2} {
		for key, want := range phoneDirectoryOptionValues(options) {
			got := values[index].Get(key)
			if descriptor.allowsQuery(key) && got != want {
				t.Fatalf("%s request %d query %s: got %q want %q", descriptor.itemKey, index, key, got, want)
			}
			if !descriptor.allowsQuery(key) && got != "" {
				t.Fatalf("%s request %d should omit unsupported query %s=%q", descriptor.itemKey, index, key, got)
			}
		}
		if got := values[index].Get("next_page_token"); got != "" {
			t.Fatalf("%s first page unexpectedly included next_page_token %q", descriptor.itemKey, got)
		}
	}
	for _, index := range []int{1, 3} {
		if got := values[index].Get("next_page_token"); got != "token-2" {
			t.Fatalf("%s request %d next_page_token: got %q", descriptor.itemKey, index, got)
		}
	}
}

func phoneDirectoryPublicReaders(directory *PhoneDirectory, id phoneDirectoryReaderID) (
	func(context.Context, PhoneDirectoryListOptions) ([]SDKPage, error),
	func(context.Context, PhoneDirectoryListOptions) ([]any, error),
) {
	switch id {
	case phoneDirectoryUsers:
		return directory.Users, directory.AllUsers
	case phoneDirectoryCommonAreas:
		return directory.CommonAreas, directory.AllCommonAreas
	case phoneDirectorySharedLineGroups:
		return directory.SharedLineGroups, directory.AllSharedLineGroups
	case phoneDirectoryCallQueues:
		return directory.CallQueues, directory.AllCallQueues
	default:
		panic(fmt.Sprintf("unknown phone directory reader %q", id))
	}
}

func phoneDirectoryOptionValues(options PhoneDirectoryListOptions) map[string]string {
	return map[string]string{
		"page_size":               fmt.Sprint(options.PageSize),
		"site_id":                 options.SiteID,
		"calling_type":            fmt.Sprint(options.CallingType),
		"status":                  options.Status,
		"department":              options.Department,
		"cost_center":             options.CostCenter,
		"keyword":                 options.Keyword,
		"common_area_device_type": fmt.Sprint(options.CommonAreaDeviceType),
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
