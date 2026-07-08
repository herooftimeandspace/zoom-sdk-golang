package zoomsdk

import (
	"context"
	"fmt"
	"net/http"
)

const (
	phoneUsersListOperation            = "phone.users.list"
	phoneCommonAreasListOperation      = "phone.common_areas.list"
	phoneSharedLineGroupsListOperation = "phone.shared_line_groups.list"
	phoneCallQueuesListOperation       = "phone.call_queues.list"

	// PhoneCommonAreaPhonesLegacyPath is the older common-area endpoint shape
	// some tenants have surfaced historically. It is not present in the
	// bundled Zoom OpenAPI inventory; use GET /phone/common_areas through
	// PhoneDirectory.CommonAreas or PhoneDirectory.AllCommonAreas instead.
	PhoneCommonAreaPhonesLegacyPath = "/phone/common_area_phones"
)

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
	return d.paginate(ctx, phoneUsersListOperation, options.query("page_size", "next_page_token", "site_id", "calling_type", "status", "department", "cost_center", "keyword"))
}

// AllUsers flattens all users from paginated GET /phone/users responses.
func (d *PhoneDirectory) AllUsers(ctx context.Context, options PhoneDirectoryListOptions) ([]any, error) {
	return d.iterAll(ctx, phoneUsersListOperation, options.query("page_size", "next_page_token", "site_id", "calling_type", "status", "department", "cost_center", "keyword"))
}

// CommonAreas returns paginated GET /phone/common_areas responses.
func (d *PhoneDirectory) CommonAreas(ctx context.Context, options PhoneDirectoryListOptions) ([]SDKPage, error) {
	return d.paginate(ctx, phoneCommonAreasListOperation, options.query("page_size", "next_page_token", "site_id", "calling_type", "common_area_device_type"))
}

// AllCommonAreas flattens all common areas from paginated GET /phone/common_areas responses.
func (d *PhoneDirectory) AllCommonAreas(ctx context.Context, options PhoneDirectoryListOptions) ([]any, error) {
	return d.iterAll(ctx, phoneCommonAreasListOperation, options.query("page_size", "next_page_token", "site_id", "calling_type", "common_area_device_type"))
}

// SharedLineGroups returns paginated GET /phone/shared_line_groups responses.
func (d *PhoneDirectory) SharedLineGroups(ctx context.Context, options PhoneDirectoryListOptions) ([]SDKPage, error) {
	return d.paginate(ctx, phoneSharedLineGroupsListOperation, options.query("page_size", "next_page_token"))
}

// AllSharedLineGroups flattens all shared line groups from paginated GET /phone/shared_line_groups responses.
func (d *PhoneDirectory) AllSharedLineGroups(ctx context.Context, options PhoneDirectoryListOptions) ([]any, error) {
	return d.iterAll(ctx, phoneSharedLineGroupsListOperation, options.query("page_size", "next_page_token"))
}

// CallQueues returns paginated GET /phone/call_queues responses.
func (d *PhoneDirectory) CallQueues(ctx context.Context, options PhoneDirectoryListOptions) ([]SDKPage, error) {
	return d.paginate(ctx, phoneCallQueuesListOperation, options.query("page_size", "next_page_token", "site_id", "cost_center", "department"))
}

// AllCallQueues flattens all call queues from paginated GET /phone/call_queues responses.
func (d *PhoneDirectory) AllCallQueues(ctx context.Context, options PhoneDirectoryListOptions) ([]any, error) {
	return d.iterAll(ctx, phoneCallQueuesListOperation, options.query("page_size", "next_page_token", "site_id", "cost_center", "department"))
}

// SupportsLegacyCommonAreaPhones reports whether the vendored SDK inventory
// contains GET /phone/common_area_phones. The current bundled Zoom OpenAPI
// corpus does not include it, so consumers should use /phone/common_areas.
func (d *PhoneDirectory) SupportsLegacyCommonAreaPhones() bool {
	_, ok := d.operationByPath(http.MethodGet, PhoneCommonAreaPhonesLegacyPath)
	return ok
}

// paginate resolves a generated SDK operation and walks every next_page_token page.
func (d *PhoneDirectory) paginate(ctx context.Context, operationName string, query map[string]any) ([]SDKPage, error) {
	operation, err := d.operation(operationName)
	if err != nil {
		return nil, err
	}
	return operation.Paginate(ctx, nil, query, nil)
}

// iterAll resolves a generated SDK operation and returns all item objects across pages.
func (d *PhoneDirectory) iterAll(ctx context.Context, operationName string, query map[string]any) ([]any, error) {
	operation, err := d.operation(operationName)
	if err != nil {
		return nil, err
	}
	return operation.IterAll(ctx, nil, query, nil)
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

// query converts typed list options into the endpoint-specific query parameters allowed by the OpenAPI spec.
func (o PhoneDirectoryListOptions) query(allowed ...string) map[string]any {
	allowedSet := map[string]bool{}
	for _, name := range allowed {
		allowedSet[name] = true
	}
	query := map[string]any{}
	if allowedSet["page_size"] && o.PageSize > 0 {
		query["page_size"] = o.PageSize
	}
	if allowedSet["next_page_token"] && o.NextPageToken != "" {
		query["next_page_token"] = o.NextPageToken
	}
	if allowedSet["site_id"] && o.SiteID != "" {
		query["site_id"] = o.SiteID
	}
	if allowedSet["calling_type"] && o.CallingType != 0 {
		query["calling_type"] = o.CallingType
	}
	if allowedSet["status"] && o.Status != "" {
		query["status"] = o.Status
	}
	if allowedSet["department"] && o.Department != "" {
		query["department"] = o.Department
	}
	if allowedSet["cost_center"] && o.CostCenter != "" {
		query["cost_center"] = o.CostCenter
	}
	if allowedSet["keyword"] && o.Keyword != "" {
		query["keyword"] = o.Keyword
	}
	if allowedSet["common_area_device_type"] && o.CommonAreaDeviceType != 0 {
		query["common_area_device_type"] = o.CommonAreaDeviceType
	}
	if len(query) == 0 {
		return nil
	}
	return query
}
