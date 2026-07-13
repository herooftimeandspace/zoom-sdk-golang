package zoomsdk

import (
	"context"
	"fmt"
	"net/http"
)

const (
	// PhoneCommonAreaPhonesLegacyPath is the older common-area endpoint shape
	// some tenants have surfaced historically. It is not present in the
	// bundled Zoom OpenAPI inventory; use GET /phone/common_areas through
	// PhoneDirectory.CommonAreas or PhoneDirectory.AllCommonAreas instead.
	PhoneCommonAreaPhonesLegacyPath = "/phone/common_area_phones"
)

// phoneDirectoryReaderID identifies one stable public Phone directory reader.
// Named string keys keep public wrappers independent of descriptor declaration
// order without exposing a generic public reader API.
type phoneDirectoryReaderID string

const (
	phoneDirectoryUsers            phoneDirectoryReaderID = "Users"
	phoneDirectoryCommonAreas      phoneDirectoryReaderID = "CommonAreas"
	phoneDirectorySharedLineGroups phoneDirectoryReaderID = "SharedLineGroups"
	phoneDirectoryCallQueues       phoneDirectoryReaderID = "CallQueues"
)

// phoneDirectoryReaderIDs enumerates the complete public helper set
// independently from the descriptor map so missing or extra descriptors are
// detectable during validation.
var phoneDirectoryReaderIDs = [...]phoneDirectoryReaderID{
	phoneDirectoryUsers,
	phoneDirectoryCommonAreas,
	phoneDirectorySharedLineGroups,
	phoneDirectoryCallQueues,
}

// phoneDirectoryReaderDescriptor is the single source of truth for one Phone
// directory list operation. Public helper names are retained for documentation
// and diagnostics, while path, response collection, and query metadata drive
// the implementation and its fixtures.
type phoneDirectoryReaderDescriptor struct {
	operationName string
	path          string
	itemKey       string
	allowedQuery  []string
}

// phoneDirectoryReaderDescriptors contains every supported Phone directory
// list reader keyed by its stable public helper identity. Adding a reader
// requires one descriptor plus its intentionally explicit public wrappers.
var phoneDirectoryReaderDescriptors = map[phoneDirectoryReaderID]phoneDirectoryReaderDescriptor{
	phoneDirectoryUsers:            {operationName: "phone.users.list", path: "/phone/users", itemKey: "users", allowedQuery: []string{"page_size", "next_page_token", "site_id", "calling_type", "status", "department", "cost_center", "keyword"}},
	phoneDirectoryCommonAreas:      {operationName: "phone.common_areas.list", path: "/phone/common_areas", itemKey: "common_areas", allowedQuery: []string{"page_size", "next_page_token", "site_id", "calling_type", "common_area_device_type"}},
	phoneDirectorySharedLineGroups: {operationName: "phone.shared_line_groups.list", path: "/phone/shared_line_groups", itemKey: "shared_line_groups", allowedQuery: []string{"page_size", "next_page_token"}},
	phoneDirectoryCallQueues:       {operationName: "phone.call_queues.list", path: "/phone/call_queues", itemKey: "call_queues", allowedQuery: []string{"next_page_token", "page_size", "site_id", "cost_center", "department"}},
}

// PhoneDirectory exposes read-only Zoom Phone directory list helpers.
//
// The helper methods delegate to the generated SDK operation inventory, so
// request construction, OAuth, retries, pagination, and response validation
// stay inside this SDK. Returned items are raw Zoom response objects represented
// as map-like any values; callers should normalize or redact them at the
// application boundary and must not log raw payloads, bearer tokens, auth
// headers, or client secrets.
type PhoneDirectory struct {
	client *Client
}

// PhoneDirectory returns read-only Zoom Phone directory helpers.
func (c *Client) PhoneDirectory() *PhoneDirectory {
	return &PhoneDirectory{client: c}
}

// PhoneDirectoryListOptions contains supported query filters for Zoom Phone
// directory list endpoints. Unsupported fields are ignored per endpoint so the
// helper only sends query parameters documented by the bundled OpenAPI spec.
type PhoneDirectoryListOptions struct {
	PageSize             int
	NextPageToken        string
	SiteID               string
	CallingType          int
	Status               string
	Department           string
	CostCenter           string
	Keyword              string
	CommonAreaDeviceType int
}

// Users returns paginated GET /phone/users responses.
func (d *PhoneDirectory) Users(ctx context.Context, options PhoneDirectoryListOptions) ([]SDKPage, error) {
	return d.paginate(ctx, phoneDirectoryUsers, options)
}

// AllUsers flattens all users from paginated GET /phone/users responses.
func (d *PhoneDirectory) AllUsers(ctx context.Context, options PhoneDirectoryListOptions) ([]any, error) {
	return d.iterAll(ctx, phoneDirectoryUsers, options)
}

// CommonAreas returns paginated GET /phone/common_areas responses.
func (d *PhoneDirectory) CommonAreas(ctx context.Context, options PhoneDirectoryListOptions) ([]SDKPage, error) {
	return d.paginate(ctx, phoneDirectoryCommonAreas, options)
}

// AllCommonAreas flattens all common areas from paginated GET /phone/common_areas responses.
func (d *PhoneDirectory) AllCommonAreas(ctx context.Context, options PhoneDirectoryListOptions) ([]any, error) {
	return d.iterAll(ctx, phoneDirectoryCommonAreas, options)
}

// SharedLineGroups returns paginated GET /phone/shared_line_groups responses.
func (d *PhoneDirectory) SharedLineGroups(ctx context.Context, options PhoneDirectoryListOptions) ([]SDKPage, error) {
	return d.paginate(ctx, phoneDirectorySharedLineGroups, options)
}

// AllSharedLineGroups flattens all shared line groups from paginated GET /phone/shared_line_groups responses.
func (d *PhoneDirectory) AllSharedLineGroups(ctx context.Context, options PhoneDirectoryListOptions) ([]any, error) {
	return d.iterAll(ctx, phoneDirectorySharedLineGroups, options)
}

// CallQueues returns paginated GET /phone/call_queues responses.
func (d *PhoneDirectory) CallQueues(ctx context.Context, options PhoneDirectoryListOptions) ([]SDKPage, error) {
	return d.paginate(ctx, phoneDirectoryCallQueues, options)
}

// AllCallQueues flattens all call queues from paginated GET /phone/call_queues responses.
func (d *PhoneDirectory) AllCallQueues(ctx context.Context, options PhoneDirectoryListOptions) ([]any, error) {
	return d.iterAll(ctx, phoneDirectoryCallQueues, options)
}

// SupportsLegacyCommonAreaPhones reports whether the vendored SDK inventory
// contains GET /phone/common_area_phones. The current bundled Zoom OpenAPI
// corpus does not include it, so consumers should use /phone/common_areas.
func (d *PhoneDirectory) SupportsLegacyCommonAreaPhones() bool {
	_, ok := d.operationByPath(http.MethodGet, PhoneCommonAreaPhonesLegacyPath)
	return ok
}

// paginate resolves a descriptor's generated SDK operation and walks every
// next_page_token page using only the filters allowed for that reader.
func (d *PhoneDirectory) paginate(ctx context.Context, id phoneDirectoryReaderID, options PhoneDirectoryListOptions) ([]SDKPage, error) {
	descriptor, err := phoneDirectoryReader(id)
	if err != nil {
		return nil, err
	}
	operation, err := d.operation(descriptor.operationName)
	if err != nil {
		return nil, err
	}
	return operation.Paginate(ctx, nil, options.query(descriptor), nil)
}

// iterAll resolves a descriptor's generated SDK operation and flattens all
// response item objects across pages using the descriptor's allowed filters.
func (d *PhoneDirectory) iterAll(ctx context.Context, id phoneDirectoryReaderID, options PhoneDirectoryListOptions) ([]any, error) {
	descriptor, err := phoneDirectoryReader(id)
	if err != nil {
		return nil, err
	}
	operation, err := d.operation(descriptor.operationName)
	if err != nil {
		return nil, err
	}
	return operation.IterAll(ctx, nil, options.query(descriptor), nil)
}

// phoneDirectoryReader returns one complete reader descriptor or a validation
// error that identifies an internal descriptor-registration defect.
func phoneDirectoryReader(id phoneDirectoryReaderID) (phoneDirectoryReaderDescriptor, error) {
	descriptor, ok := phoneDirectoryReaderDescriptors[id]
	if !ok {
		return phoneDirectoryReaderDescriptor{}, &ValidationError{Message: fmt.Sprintf("phone directory reader %q is not registered", id)}
	}
	return descriptor, nil
}

// operation returns a named generated SDK operation or a validation error that points at SDK initialization.
func (d *PhoneDirectory) operation(name string) (*SDKOperation, error) {
	if d == nil || d.client == nil || d.client.SDK == nil {
		return nil, &ValidationError{Message: "phone directory helper requires an initialized SDK client"}
	}
	operation, ok := d.client.SDK.Operation(name)
	if !ok {
		return nil, &ValidationError{Message: fmt.Sprintf("phone directory operation %q is not available in the generated SDK inventory", name)}
	}
	return operation, nil
}

// operationByPath finds a generated SDK operation by method and exact OpenAPI path.
func (d *PhoneDirectory) operationByPath(method string, path string) (*SDKOperation, bool) {
	if d == nil || d.client == nil || d.client.SDK == nil {
		return nil, false
	}
	for _, name := range d.client.SDK.Inventory() {
		operation, ok := d.client.SDK.Operation(name)
		if ok && operation.HTTPMethod == method && operation.Path == path {
			return operation, true
		}
	}
	return nil, false
}

// allowsQuery reports whether a descriptor permits one typed list option to be
// sent to its generated operation.
func (d phoneDirectoryReaderDescriptor) allowsQuery(name string) bool {
	for _, allowed := range d.allowedQuery {
		if allowed == name {
			return true
		}
	}
	return false
}

// query converts typed list options into the endpoint-specific query
// parameters declared by the shared reader descriptor.
func (o PhoneDirectoryListOptions) query(descriptor phoneDirectoryReaderDescriptor) map[string]any {
	query := map[string]any{}
	if descriptor.allowsQuery("page_size") && o.PageSize > 0 {
		query["page_size"] = o.PageSize
	}
	if descriptor.allowsQuery("next_page_token") && o.NextPageToken != "" {
		query["next_page_token"] = o.NextPageToken
	}
	if descriptor.allowsQuery("site_id") && o.SiteID != "" {
		query["site_id"] = o.SiteID
	}
	if descriptor.allowsQuery("calling_type") && o.CallingType != 0 {
		query["calling_type"] = o.CallingType
	}
	if descriptor.allowsQuery("status") && o.Status != "" {
		query["status"] = o.Status
	}
	if descriptor.allowsQuery("department") && o.Department != "" {
		query["department"] = o.Department
	}
	if descriptor.allowsQuery("cost_center") && o.CostCenter != "" {
		query["cost_center"] = o.CostCenter
	}
	if descriptor.allowsQuery("keyword") && o.Keyword != "" {
		query["keyword"] = o.Keyword
	}
	if descriptor.allowsQuery("common_area_device_type") && o.CommonAreaDeviceType != 0 {
		query["common_area_device_type"] = o.CommonAreaDeviceType
	}
	if len(query) == 0 {
		return nil
	}
	return query
}
