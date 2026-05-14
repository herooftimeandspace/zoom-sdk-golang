package zoomsdk

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// SDK exposes a generated inventory and operation lookup built from vendored OpenAPI metadata.
type SDK struct {
	client     *Client
	operations map[string]*SDKOperation
}

// SDKOperation exposes one generated operation.
type SDKOperation struct {
	client                *Client
	Name                  string
	Namespace             []string
	OperationName         string
	Aliases               []string
	Path                  string
	HTTPMethod            string
	OperationID           string
	Summary               string
	Description           string
	PathParameters        []string
	QueryParameters       []string
	PathParameterDetails  []SDKParameter
	QueryParameterDetails []SDKParameter
	HasRequestModel       bool
	HasResponseModel      bool
	RequestSchema         map[string]any
	ResponseSchema        map[string]any
	operationSpec         map[string]any
}

// SDKParameter describes one schema-derived SDK parameter.
type SDKParameter struct {
	Name        string
	GoName      string
	Location    string
	Required    bool
	Description string
}

// SDKOperationMetadata is the stable descriptive shape for one SDK operation.
type SDKOperationMetadata struct {
	Name            string
	Namespace       []string
	OperationName   string
	Aliases         []string
	HTTPMethod      string
	Path            string
	OperationID     string
	Summary         string
	Description     string
	PathParameters  []SDKParameter
	QueryParameters []SDKParameter
}

// SDKCallOptions controls an ergonomic SDK operation call.
type SDKCallOptions struct {
	PathParams map[string]string
	Query      map[string]any
	JSONBody   any
	Headers    map[string]string
}

// SDKPage is one paginated result page.
type SDKPage struct {
	Value         any
	Items         []any
	NextPageToken string
	PageSize      int
	PageNumber    int
	TotalRecords  int
	TotalPages    int
}

// NewSDK builds the Go migration SDK from vendored golden parity data.
func NewSDK(client *Client) (*SDK, error) {
	golden, err := GoldenPublicSurface()
	if err != nil {
		return nil, err
	}
	operations := map[string]*SDKOperation{}
	for name, metadata := range golden {
		namespace, operationName := splitOperationName(name)
		operation := &SDKOperation{
			client:           client,
			Name:             name,
			Namespace:        namespace,
			OperationName:    operationName,
			Aliases:          operationAliases(operationName, stringValue(metadata["operation_id"])),
			Path:             stringValue(metadata["path"]),
			HTTPMethod:       stringValue(metadata["http_method"]),
			OperationID:      stringValue(metadata["operation_id"]),
			PathParameters:   stringSlice(metadata["path_parameters"]),
			QueryParameters:  stringSlice(metadata["query_parameters"]),
			HasRequestModel:  boolValue(metadata["has_request_model"]),
			HasResponseModel: boolValue(metadata["has_response_model"]),
		}
		operation.enrichFromSchema()
		operations[name] = operation
	}
	return &SDK{client: client, operations: operations}, nil
}

// Operation returns one generated operation by its dotted parity name.
func (s *SDK) Operation(name string) (*SDKOperation, bool) {
	operation, ok := s.operations[name]
	return operation, ok
}

// Inventory returns the generated operation names in stable order.
func (s *SDK) Inventory() []string {
	names := make([]string, 0, len(s.operations))
	for name := range s.operations {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Namespaces returns the top-level SDK namespaces in stable order.
func (s *SDK) Namespaces() []string {
	names := map[string]bool{}
	for _, operation := range s.operations {
		if len(operation.Namespace) > 0 {
			names[operation.Namespace[0]] = true
		}
	}
	return sortedMapKeys(names)
}

// NamespaceOperations returns operation names under the provided namespace prefix.
func (s *SDK) NamespaceOperations(prefix ...string) []string {
	names := []string{}
	for name, operation := range s.operations {
		if namespaceHasPrefix(operation.Namespace, prefix) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// Raw invokes the low-level request path for the operation.
func (o *SDKOperation) Raw(ctx context.Context, pathParams map[string]string, query map[string]any, jsonBody any, headers map[string]string) (any, error) {
	return o.client.Request(ctx, o.HTTPMethod, o.Path, RequestOptions{
		PathParams: pathParams,
		Query:      query,
		JSONBody:   jsonBody,
		Headers:    headers,
	})
}

// Call invokes the operation with SDK-level parameter defaults and validation.
func (o *SDKOperation) Call(ctx context.Context, options SDKCallOptions) (any, error) {
	pathParams, err := o.resolvePathParams(options.PathParams)
	if err != nil {
		return nil, err
	}
	query := o.resolveQuery(options.Query)
	if err := o.validateRequestBody(options.JSONBody); err != nil {
		return nil, err
	}
	return o.Raw(ctx, pathParams, query, options.JSONBody, options.Headers)
}

// Paginate walks pages using next_page_token semantics.
func (o *SDKOperation) Paginate(ctx context.Context, pathParams map[string]string, query map[string]any, headers map[string]string) ([]SDKPage, error) {
	currentQuery := map[string]any{}
	for key, value := range query {
		currentQuery[key] = value
	}
	pages := []SDKPage{}
	for {
		value, err := o.Raw(ctx, pathParams, currentQuery, nil, headers)
		if err != nil {
			return nil, err
		}
		page := buildSDKPage(value)
		pages = append(pages, page)
		if page.NextPageToken == "" {
			break
		}
		currentQuery["next_page_token"] = page.NextPageToken
	}
	return pages, nil
}

// Metadata returns a stable descriptive snapshot for this operation.
func (o *SDKOperation) Metadata() SDKOperationMetadata {
	return SDKOperationMetadata{
		Name:            o.Name,
		Namespace:       append([]string{}, o.Namespace...),
		OperationName:   o.OperationName,
		Aliases:         append([]string{}, o.Aliases...),
		HTTPMethod:      o.HTTPMethod,
		Path:            o.Path,
		OperationID:     o.OperationID,
		Summary:         o.Summary,
		Description:     o.Description,
		PathParameters:  append([]SDKParameter{}, o.PathParameterDetails...),
		QueryParameters: append([]SDKParameter{}, o.QueryParameterDetails...),
	}
}

// Describe renders a stable human-readable operation description.
func (o *SDKOperation) Describe() string {
	summary := o.Summary
	if summary == "" {
		summary = o.DebugDescribeOperation()
	}
	return fmt.Sprintf("%s\nOperation ID: %s\nHTTP: %s %s\nPath parameters: %s\nQuery parameters: %s", summary, o.OperationID, o.HTTPMethod, o.Path, strings.Join(parameterNames(o.PathParameterDetails), ", "), strings.Join(parameterNames(o.QueryParameterDetails), ", "))
}

// IterAll flattens items across paginated responses.
func (o *SDKOperation) IterAll(ctx context.Context, pathParams map[string]string, query map[string]any, headers map[string]string) ([]any, error) {
	pages, err := o.Paginate(ctx, pathParams, query, headers)
	if err != nil {
		return nil, err
	}
	items := []any{}
	for _, page := range pages {
		items = append(items, page.Items...)
	}
	return items, nil
}

func buildSDKPage(value any) SDKPage {
	page := SDKPage{Value: value}
	payload, ok := value.(map[string]any)
	if !ok {
		return page
	}
	page.NextPageToken = firstString(payload, "next_page_token", "nextPageToken")
	page.PageSize = firstInt(payload, "page_size", "pageSize")
	page.PageNumber = firstInt(payload, "page_number", "pageNumber")
	page.TotalRecords = firstInt(payload, "total_records", "totalRecords")
	page.TotalPages = firstInt(payload, "total_pages", "totalPages")
	page.Items = preferredCollection(payload)
	return page
}

func preferredCollection(payload map[string]any) []any {
	for _, key := range sortedKeys(payload) {
		if key == "next_page_token" || key == "nextPageToken" || key == "page_size" || key == "pageSize" || key == "page_number" || key == "pageNumber" || key == "total_records" || key == "totalRecords" || key == "total_pages" || key == "totalPages" {
			continue
		}
		if items, ok := payload[key].([]any); ok {
			return items
		}
	}
	return nil
}

func sortedKeys(payload map[string]any) []string {
	keys := make([]string, 0, len(payload))
	for key := range payload {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func firstString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key].(string); ok {
			return value
		}
	}
	return ""
}

func firstInt(payload map[string]any, keys ...string) int {
	for _, key := range keys {
		switch value := payload[key].(type) {
		case int:
			return value
		case float64:
			return int(value)
		case json.Number:
			parsed, err := value.Int64()
			if err == nil {
				return int(parsed)
			}
		}
	}
	return 0
}

func stringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if stringValue, ok := item.(string); ok {
				result = append(result, stringValue)
			}
		}
		return result
	default:
		return nil
	}
}

func boolValue(value any) bool {
	result, _ := value.(bool)
	return result
}

func (o *SDKOperation) enrichFromSchema() {
	if o == nil || o.client == nil || o.client.schemaRegistry == nil {
		o.applyGoldenParameterFallbacks()
		return
	}
	schemaOperation, err := o.client.schemaRegistry.FindOperation(o.HTTPMethod, o.Path)
	if err != nil {
		o.applyGoldenParameterFallbacks()
		return
	}
	o.Summary = schemaOperation.Summary
	o.Description = schemaOperation.Description
	o.operationSpec = schemaOperation.Spec
	tools := o.client.schemaRegistry.validator.tools
	o.RequestSchema = pickRequestSchema(tools, schemaOperation)
	o.ResponseSchema = representativeResponseSchema(tools, schemaOperation)
	o.PathParameterDetails, o.QueryParameterDetails = sdkParametersForOperation(schemaOperation)
	if len(o.PathParameterDetails) == 0 && len(o.PathParameters) > 0 {
		o.PathParameterDetails = parameterDetailsFromNames(o.PathParameters, "path", true)
	}
	if len(o.QueryParameterDetails) == 0 && len(o.QueryParameters) > 0 {
		o.QueryParameterDetails = parameterDetailsFromNames(o.QueryParameters, "query", false)
	}
}

func (o *SDKOperation) applyGoldenParameterFallbacks() {
	o.PathParameterDetails = parameterDetailsFromNames(o.PathParameters, "path", true)
	o.QueryParameterDetails = parameterDetailsFromNames(o.QueryParameters, "query", false)
}

func (o *SDKOperation) resolvePathParams(input map[string]string) (map[string]string, error) {
	resolved := map[string]string{}
	for key, value := range input {
		resolved[key] = value
	}
	for _, placeholder := range pathPlaceholders(o.Path) {
		goName := snakeCase(placeholder)
		if _, ok := resolved[placeholder]; ok {
			continue
		}
		if value, ok := resolved[goName]; ok {
			resolved[placeholder] = value
			continue
		}
		if goName == "account_id" {
			if defaultAccount := o.client.DefaultAccountID(); defaultAccount != "" {
				resolved[placeholder] = defaultAccount
				continue
			}
		}
		return nil, &ValidationError{Message: fmt.Sprintf("missing required path parameter %q for SDK operation %s", goName, o.Name)}
	}
	return resolved, nil
}

func (o *SDKOperation) resolveQuery(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	aliases := map[string]string{}
	for _, parameter := range o.QueryParameterDetails {
		aliases[parameter.Name] = parameter.Name
		aliases[parameter.GoName] = parameter.Name
	}
	query := map[string]any{}
	for key, value := range input {
		if original, ok := aliases[key]; ok {
			query[original] = value
			continue
		}
		query[key] = value
	}
	return query
}

func (o *SDKOperation) validateRequestBody(body any) error {
	if body == nil || o.RequestSchema == nil || o.operationSpec == nil || o.client == nil || o.client.schemaRegistry == nil {
		return nil
	}
	context := fmt.Sprintf("%s %s request body", o.HTTPMethod, o.Path)
	return o.client.schemaRegistry.validator.ValidatePayload(o.operationSpec, o.RequestSchema, body, context)
}

func sdkParametersForOperation(operation *SchemaOperation) ([]SDKParameter, []SDKParameter) {
	pathParameters := []SDKParameter{}
	queryParameters := []SDKParameter{}
	seen := map[string]bool{}
	for _, raw := range operation.Parameters {
		name := stringValue(raw["name"])
		location := stringValue(raw["in"])
		if name == "" || location == "" {
			continue
		}
		key := location + ":" + name
		if seen[key] {
			continue
		}
		seen[key] = true
		parameter := SDKParameter{
			Name:        name,
			GoName:      snakeCase(name),
			Location:    location,
			Required:    boolValue(raw["required"]),
			Description: stringValue(raw["description"]),
		}
		switch location {
		case "path":
			pathParameters = append(pathParameters, parameter)
		case "query":
			queryParameters = append(queryParameters, parameter)
		}
	}
	return pathParameters, queryParameters
}

func parameterDetailsFromNames(names []string, location string, required bool) []SDKParameter {
	result := make([]SDKParameter, 0, len(names))
	for _, name := range names {
		result = append(result, SDKParameter{
			Name:     name,
			GoName:   snakeCase(name),
			Location: location,
			Required: required,
		})
	}
	return result
}

func splitOperationName(name string) ([]string, string) {
	parts := strings.Split(name, ".")
	if len(parts) == 0 {
		return nil, ""
	}
	if len(parts) == 1 {
		return nil, parts[0]
	}
	return append([]string{}, parts[:len(parts)-1]...), parts[len(parts)-1]
}

func operationAliases(operationName string, operationID string) []string {
	seen := map[string]bool{}
	aliases := []string{}
	for _, candidate := range []string{operationName, snakeCase(operationID), operationID} {
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		aliases = append(aliases, candidate)
	}
	return aliases
}

func namespaceHasPrefix(namespace []string, prefix []string) bool {
	if len(prefix) == 0 {
		return true
	}
	if len(namespace) < len(prefix) {
		return false
	}
	for index, value := range prefix {
		if namespace[index] != value {
			return false
		}
	}
	return true
}

func sortedMapKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func parameterNames(parameters []SDKParameter) []string {
	names := make([]string, 0, len(parameters))
	for _, parameter := range parameters {
		if parameter.GoName != "" {
			names = append(names, parameter.GoName)
			continue
		}
		names = append(names, parameter.Name)
	}
	return names
}

func pathPlaceholders(path string) []string {
	matches := regexp.MustCompile(`\{([^/{}]+)\}`).FindAllStringSubmatch(path, -1)
	result := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			result = append(result, match[1])
		}
	}
	return result
}

func snakeCase(value string) string {
	if value == "" {
		return ""
	}
	value = strings.NewReplacer("-", "_", " ", "_", ".", "_").Replace(value)
	var builder strings.Builder
	var previousUnderscore bool
	for index, r := range value {
		if r == '_' {
			if !previousUnderscore && builder.Len() > 0 {
				builder.WriteRune('_')
				previousUnderscore = true
			}
			continue
		}
		if unicode.IsUpper(r) {
			if index > 0 && !previousUnderscore {
				builder.WriteRune('_')
			}
			builder.WriteRune(unicode.ToLower(r))
			previousUnderscore = false
			continue
		}
		builder.WriteRune(unicode.ToLower(r))
		previousUnderscore = false
	}
	return strings.Trim(builder.String(), "_")
}

// GoName converts a parity operation path into a stable exported Go-ish identifier fragment.
func GoName(path string) string {
	if path == "" {
		return ""
	}
	parts := strings.Split(path, ".")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		result = append(result, exportName(part))
	}
	return strings.Join(result, "")
}

func exportName(value string) string {
	if value == "" {
		return ""
	}
	replacer := strings.NewReplacer("-", "_", "'", "", " ", "_")
	value = replacer.Replace(value)
	chunks := strings.FieldsFunc(value, func(r rune) bool {
		return r == '_' || r == '.'
	})
	for index, chunk := range chunks {
		if chunk == "" {
			continue
		}
		chunks[index] = strings.ToUpper(chunk[:1]) + chunk[1:]
	}
	return strings.Join(chunks, "")
}

// DebugDescribeOperation renders a stable printable description for parity tests.
func (o *SDKOperation) DebugDescribeOperation() string {
	return fmt.Sprintf("%s %s (%s)", o.HTTPMethod, o.Path, o.OperationID)
}
