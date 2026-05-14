package zoomsdk

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// SchemaOperation is one indexed path-based OpenAPI operation.
type SchemaOperation struct {
	SchemaName     string
	Method         string
	TemplatePath   string
	PathRegex      *regexp.Regexp
	OperationID    string
	Summary        string
	Description    string
	Parameters     []map[string]any
	HasRequestBody bool
	RequestBody    map[string]any
	Responses      map[string]any
	Spec           map[string]any
	ServerURL      string
}

// WebhookOperation is one indexed webhook operation.
type WebhookOperation struct {
	SchemaName    string
	EventName     string
	OperationID   string
	Method        string
	RequestSchema map[string]any
	Spec          map[string]any
}

// SchemaRegistry indexes endpoint and master-account operations.
type SchemaRegistry struct {
	operations []SchemaOperation
	validator  *SchemaValidator
}

// WebhookRegistry indexes webhook operations.
type WebhookRegistry struct {
	operations []WebhookOperation
	validator  *SchemaValidator
}

// OpenAPITools owns compatibility helpers used by registries and validators.
type OpenAPITools struct{}

// SchemaValidator validates payloads against prepared schemas.
type SchemaValidator struct {
	tools *OpenAPITools
}

// NewSchemaRegistry loads vendored path-based OpenAPI operations.
func NewSchemaRegistry(root string) (*SchemaRegistry, error) {
	if root == "" {
		root = filepath.Join(discoverProjectRoot("."), "internal", "parity", "schemas")
	}
	registry := &SchemaRegistry{
		operations: []SchemaOperation{},
		validator:  &SchemaValidator{tools: &OpenAPITools{}},
	}
	for _, family := range []string{"endpoints", "master_accounts"} {
		familyRoot := filepath.Join(root, family)
		err := filepath.WalkDir(familyRoot, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".json") {
				return nil
			}
			operations, loadErr := loadPathOperations(path)
			if loadErr != nil {
				return loadErr
			}
			registry.operations = append(registry.operations, operations...)
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	sort.SliceStable(registry.operations, func(i, j int) bool {
		if registry.operations[i].TemplatePath == registry.operations[j].TemplatePath {
			return registry.operations[i].Method < registry.operations[j].Method
		}
		return registry.operations[i].TemplatePath < registry.operations[j].TemplatePath
	})
	return registry, nil
}

// NewWebhookRegistry loads vendored webhook OpenAPI operations.
func NewWebhookRegistry(root string) (*WebhookRegistry, error) {
	if root == "" {
		root = filepath.Join(discoverProjectRoot("."), "internal", "parity", "schemas", "webhooks")
	}
	registry := &WebhookRegistry{
		operations: []WebhookOperation{},
		validator:  &SchemaValidator{tools: &OpenAPITools{}},
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		operations, loadErr := loadWebhookOperations(path)
		if loadErr != nil {
			return loadErr
		}
		registry.operations = append(registry.operations, operations...)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return registry, nil
}

// FindOperation finds the operation matching a method and actual request path.
func (r *SchemaRegistry) FindOperation(method string, actualPath string) (*SchemaOperation, error) {
	normalizedMethod := strings.ToUpper(method)
	for index := range r.operations {
		operation := &r.operations[index]
		if operation.Method == normalizedMethod && operation.PathRegex.MatchString(actualPath) {
			return operation, nil
		}
	}
	return nil, &ValidationError{Message: fmt.Sprintf("no schema operation found for %s %s", normalizedMethod, actualPath)}
}

// BaseURLForRequest returns the operation-specific server URL when declared.
func (r *SchemaRegistry) BaseURLForRequest(method string, actualPath string, fallback string) string {
	operation, err := r.FindOperation(method, actualPath)
	if err != nil || operation.ServerURL == "" {
		return fallback
	}
	return operation.ServerURL
}

// ValidateResponse validates the payload for the operation and status code.
func (r *SchemaRegistry) ValidateResponse(method string, actualPath string, statusCode int, payload any) error {
	operation, err := r.FindOperation(method, actualPath)
	if err != nil {
		return err
	}
	responseSchema, err := pickResponseSchema(r.validator.tools, operation, statusCode)
	if err != nil {
		return err
	}
	if responseSchema == nil {
		return nil
	}
	context := fmt.Sprintf("%s %s response status=%d", strings.ToUpper(method), actualPath, statusCode)
	return r.validator.ValidatePayload(operation.Spec, responseSchema, payload, context)
}

// ValidateWebhook validates a webhook payload by event name and optional narrowing hints.
func (r *WebhookRegistry) ValidateWebhook(eventName string, payload any, schemaName string, operationID string) error {
	matches := []WebhookOperation{}
	for _, operation := range r.operations {
		if operation.EventName != eventName {
			continue
		}
		if schemaName != "" && operation.SchemaName != schemaName {
			continue
		}
		if operationID != "" && operation.OperationID != operationID {
			continue
		}
		matches = append(matches, operation)
	}
	if len(matches) == 0 {
		return &ValidationError{Message: fmt.Sprintf("Could not find webhook schema for %s", eventName)}
	}
	if len(matches) > 1 && schemaName == "" && operationID == "" {
		return &ValidationError{Message: fmt.Sprintf("ambiguous webhook schema lookup for %s", eventName)}
	}
	context := fmt.Sprintf("webhook %s", eventName)
	return r.validator.ValidatePayload(matches[0].Spec, matches[0].RequestSchema, payload, context)
}

// ValidatePayload validates a payload against a schema.
func (v *SchemaValidator) ValidatePayload(spec map[string]any, schema map[string]any, payload any, context string) error {
	prepared, err := v.tools.PrepareSchema(spec, schema)
	if err != nil {
		return err
	}
	normalized := v.tools.NormalizePayloadForSchema(payload, prepared)
	errors := []string{}
	validateAgainstSchema(prepared, normalized, "$", &errors)
	if len(errors) > 0 {
		limit := len(errors)
		if limit > 5 {
			limit = 5
		}
		return &ValidationError{Message: fmt.Sprintf("%s: %s", context, strings.Join(errors[:limit], "; "))}
	}
	return nil
}

// PickJSONMedia selects a JSON-like media definition from an OpenAPI content map.
func (t *OpenAPITools) PickJSONMedia(content map[string]any) map[string]any {
	for _, key := range []string{"application/json", "application/json; charset=utf-8", "application/scim+json"} {
		if candidate, ok := content[key].(map[string]any); ok {
			return candidate
		}
	}
	for mediaType, candidate := range content {
		if strings.Contains(strings.ToLower(mediaType), "json") {
			if value, ok := candidate.(map[string]any); ok {
				return value
			}
		}
	}
	return nil
}

// ResolveRef resolves a local JSON pointer against an OpenAPI spec.
func (t *OpenAPITools) ResolveRef(spec map[string]any, ref string) (any, error) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, &ValidationError{Message: fmt.Sprintf("Only local refs are supported, got: %s", ref)}
	}
	parts := strings.Split(strings.TrimPrefix(ref, "#/"), "/")
	for index, part := range parts {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		parts[index] = part
	}
	if len(parts) > 0 && parts[0] == "paths" {
		if _, ok := spec["paths"]; !ok {
			if _, ok := spec["webhooks"]; ok {
				parts[0] = "webhooks"
			}
		}
	}
	var current any = spec
	for _, part := range parts {
		mapping, ok := current.(map[string]any)
		if !ok {
			return nil, &ValidationError{Message: fmt.Sprintf("Unresolvable $ref: %s", ref)}
		}
		next, ok := mapping[part]
		if !ok {
			return nil, &ValidationError{Message: fmt.Sprintf("Unresolvable $ref: %s", ref)}
		}
		current = next
	}
	return current, nil
}

// ResolveSchema recursively resolves local refs inside a schema fragment.
func (t *OpenAPITools) ResolveSchema(spec map[string]any, schema any) (any, error) {
	switch typed := schema.(type) {
	case map[string]any:
		if ref, ok := typed["$ref"].(string); ok {
			target, err := t.ResolveRef(spec, ref)
			if err != nil {
				return nil, err
			}
			targetMap, _ := cloneMap(target).(map[string]any)
			for key, value := range typed {
				if key != "$ref" {
					targetMap[key] = value
				}
			}
			return t.ResolveSchema(spec, targetMap)
		}
		resolved := map[string]any{}
		for key, value := range typed {
			next, err := t.ResolveSchema(spec, value)
			if err != nil {
				return nil, err
			}
			resolved[key] = next
		}
		return resolved, nil
	case []any:
		resolved := make([]any, 0, len(typed))
		for _, item := range typed {
			next, err := t.ResolveSchema(spec, item)
			if err != nil {
				return nil, err
			}
			resolved = append(resolved, next)
		}
		return resolved, nil
	default:
		return schema, nil
	}
}

// NormalizeSchema applies compatibility normalization to a schema.
func (t *OpenAPITools) NormalizeSchema(schema any) any {
	switch typed := schema.(type) {
	case map[string]any:
		normalized := map[string]any{}
		for key, value := range typed {
			if key == "type" {
				if stringValue, ok := value.(string); ok {
					normalized[key] = normalizeTypeName(stringValue)
					continue
				}
			}
			normalized[key] = t.NormalizeSchema(value)
		}
		properties, hasProperties := normalized["properties"].(map[string]any)
		required, hasRequired := normalized["required"].([]any)
		if hasProperties && hasRequired {
			changed := false
			for _, requiredName := range required {
				name, ok := requiredName.(string)
				if !ok {
					continue
				}
				if _, exists := properties[name]; !exists {
					properties[name] = map[string]any{}
					changed = true
				}
			}
			if changed {
				normalized["properties"] = properties
			}
		}
		return normalized
	case []any:
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			result = append(result, t.NormalizeSchema(item))
		}
		return result
	default:
		return schema
	}
}

// NormalizePayloadForSchema adjusts payloads to match known Zoom schema quirks.
func (t *OpenAPITools) NormalizePayloadForSchema(payload any, schema any) any {
	schemaMap, ok := schema.(map[string]any)
	if !ok {
		return payload
	}
	if _, ok := schemaMap["allOf"]; ok {
		return t.normalizeAllOfPayload(payload, schemaMap)
	}
	if _, ok := schemaMap["oneOf"]; ok {
		return t.normalizeVariantPayload(payload, schemaMap, "oneOf")
	}
	if _, ok := schemaMap["anyOf"]; ok {
		return t.normalizeVariantPayload(payload, schemaMap, "anyOf")
	}

	switch schemaMap["type"] {
	case "object":
		payloadMap, ok := payload.(map[string]any)
		if !ok {
			return payload
		}
		normalized := cloneMap(payloadMap).(map[string]any)
		properties, _ := schemaMap["properties"].(map[string]any)
		requiredNames := requiredNameSet(schemaMap["required"])
		for key, value := range payloadMap {
			propertySchema, ok := properties[key].(map[string]any)
			if !ok {
				continue
			}
			if shouldDropEmptyOptionalEnumValue(key, value, propertySchema, requiredNames) {
				delete(normalized, key)
				continue
			}
			normalized[key] = t.NormalizePayloadForSchema(value, propertySchema)
		}
		return normalized
	case "array":
		payloadSlice, ok := payload.([]any)
		if !ok {
			return payload
		}
		itemSchema := schemaMap["items"]
		normalized := make([]any, 0, len(payloadSlice))
		for _, item := range payloadSlice {
			normalized = append(normalized, t.NormalizePayloadForSchema(item, itemSchema))
		}
		return normalized
	default:
		return payload
	}
}

// PrepareSchema resolves refs and applies schema normalization.
func (t *OpenAPITools) PrepareSchema(spec map[string]any, schema map[string]any) (map[string]any, error) {
	resolved, err := t.ResolveSchema(spec, schema)
	if err != nil {
		return nil, err
	}
	normalized, _ := t.NormalizeSchema(resolved).(map[string]any)
	return normalized, nil
}

func (t *OpenAPITools) normalizeAllOfPayload(payload any, schema map[string]any) any {
	normalized := payload
	allOf, _ := schema["allOf"].([]any)
	for _, branch := range allOf {
		branchMap, ok := branch.(map[string]any)
		if !ok {
			continue
		}
		merged := mergeSchemaBranch(schema, branchMap, "allOf")
		normalized = t.NormalizePayloadForSchema(normalized, merged)
	}
	return normalized
}

func (t *OpenAPITools) normalizeVariantPayload(payload any, schema map[string]any, keyword string) any {
	candidates, _ := schema[keyword].([]any)
	bestPayload := payload
	bestErrorCount := -1
	for _, branch := range candidates {
		branchMap, ok := branch.(map[string]any)
		if !ok {
			continue
		}
		merged := mergeSchemaBranch(schema, branchMap, keyword)
		candidatePayload := t.NormalizePayloadForSchema(payload, merged)
		errors := []string{}
		validateAgainstSchema(merged, candidatePayload, "$", &errors)
		if bestErrorCount == -1 || len(errors) < bestErrorCount {
			bestErrorCount = len(errors)
			bestPayload = candidatePayload
		}
		if len(errors) == 0 {
			break
		}
	}
	return bestPayload
}

func loadPathOperations(path string) ([]SchemaOperation, error) {
	spec, err := loadJSON(path)
	if err != nil {
		return nil, err
	}
	schemaName := specTitle(spec, filepath.Base(path))
	serverURL := firstServerURL(spec)
	paths, _ := spec["paths"].(map[string]any)
	operations := []SchemaOperation{}
	for templatePath, rawPathItem := range paths {
		pathItem, ok := rawPathItem.(map[string]any)
		if !ok {
			continue
		}
		pathParameters := extractParameters(pathItem["parameters"])
		for method, rawMethod := range pathItem {
			methodMap, ok := rawMethod.(map[string]any)
			if !ok {
				continue
			}
			parameters := append([]map[string]any{}, pathParameters...)
			parameters = append(parameters, extractParameters(methodMap["parameters"])...)
			requestBody, _ := methodMap["requestBody"].(map[string]any)
			responses, _ := methodMap["responses"].(map[string]any)
			operation := SchemaOperation{
				SchemaName:     schemaName,
				Method:         strings.ToUpper(method),
				TemplatePath:   templatePath,
				PathRegex:      templatePathToRegex(templatePath),
				OperationID:    stringValue(methodMap["operationId"]),
				Summary:        stringValue(methodMap["summary"]),
				Description:    stringValue(methodMap["description"]),
				Parameters:     parameters,
				HasRequestBody: requestBody != nil,
				RequestBody:    requestBody,
				Responses:      responses,
				Spec:           spec,
				ServerURL:      serverURL,
			}
			operations = append(operations, operation)
		}
	}
	return operations, nil
}

func loadWebhookOperations(path string) ([]WebhookOperation, error) {
	spec, err := loadJSON(path)
	if err != nil {
		return nil, err
	}
	schemaName := specTitle(spec, filepath.Base(path))
	webhooks, _ := spec["webhooks"].(map[string]any)
	operations := []WebhookOperation{}
	tools := &OpenAPITools{}
	for eventName, rawWebhook := range webhooks {
		methodMap, ok := rawWebhook.(map[string]any)
		if !ok {
			continue
		}
		for method, rawMethod := range methodMap {
			payload, ok := rawMethod.(map[string]any)
			if !ok {
				continue
			}
			requestBody, _ := payload["requestBody"].(map[string]any)
			content, _ := requestBody["content"].(map[string]any)
			media := tools.PickJSONMedia(content)
			if media == nil {
				continue
			}
			requestSchema, _ := media["schema"].(map[string]any)
			if requestSchema == nil {
				continue
			}
			operations = append(operations, WebhookOperation{
				SchemaName:    schemaName,
				EventName:     eventName,
				OperationID:   stringValue(payload["operationId"]),
				Method:        strings.ToUpper(method),
				RequestSchema: requestSchema,
				Spec:          spec,
			})
		}
	}
	return operations, nil
}

func pickResponseSchema(tools *OpenAPITools, operation *SchemaOperation, statusCode int) (map[string]any, error) {
	responseEntry, ok := operation.Responses[strconv.Itoa(statusCode)]
	if !ok {
		responseEntry, ok = operation.Responses["default"]
		if !ok {
			return nil, &ValidationError{Message: fmt.Sprintf("status %d is not documented for %s %s", statusCode, operation.Method, operation.TemplatePath)}
		}
	}
	responseMap, ok := responseEntry.(map[string]any)
	if !ok {
		return nil, nil
	}
	content, _ := responseMap["content"].(map[string]any)
	if len(content) == 0 {
		return nil, nil
	}
	media := tools.PickJSONMedia(content)
	if media == nil {
		return nil, nil
	}
	schema, _ := media["schema"].(map[string]any)
	return schema, nil
}

func pickRequestSchema(tools *OpenAPITools, operation *SchemaOperation) map[string]any {
	if operation == nil {
		return nil
	}
	content, _ := operation.RequestBody["content"].(map[string]any)
	if len(content) == 0 {
		return nil
	}
	media := tools.PickJSONMedia(content)
	if media == nil {
		return nil
	}
	schema, _ := media["schema"].(map[string]any)
	return schema
}

func representativeResponseSchema(tools *OpenAPITools, operation *SchemaOperation) map[string]any {
	if operation == nil {
		return nil
	}
	for _, statusCode := range []int{200, 201, 202, 204} {
		schema, err := pickResponseSchema(tools, operation, statusCode)
		if err == nil && schema != nil {
			return schema
		}
	}
	schema, err := pickResponseSchema(tools, operation, 0)
	if err == nil {
		return schema
	}
	return nil
}

func validateAgainstSchema(schema any, payload any, path string, errors *[]string) {
	schemaMap, ok := schema.(map[string]any)
	if !ok {
		return
	}
	if enumValues, ok := schemaMap["enum"].([]any); ok {
		if !enumContains(enumValues, payload) {
			*errors = append(*errors, fmt.Sprintf("path=%s message=value is not in enum", path))
			return
		}
	}

	if allOf, ok := schemaMap["allOf"].([]any); ok {
		for _, branch := range allOf {
			validateAgainstSchema(branch, payload, path, errors)
		}
	}
	if oneOf, ok := schemaMap["oneOf"].([]any); ok {
		matchCount := 0
		for _, branch := range oneOf {
			branchErrors := []string{}
			validateAgainstSchema(branch, payload, path, &branchErrors)
			if len(branchErrors) == 0 {
				matchCount++
			}
		}
		if matchCount == 0 {
			*errors = append(*errors, fmt.Sprintf("path=%s message=payload did not match any oneOf branch", path))
		}
	}
	if anyOf, ok := schemaMap["anyOf"].([]any); ok {
		matchCount := 0
		for _, branch := range anyOf {
			branchErrors := []string{}
			validateAgainstSchema(branch, payload, path, &branchErrors)
			if len(branchErrors) == 0 {
				matchCount++
			}
		}
		if matchCount == 0 {
			*errors = append(*errors, fmt.Sprintf("path=%s message=payload did not match any anyOf branch", path))
		}
	}

	typeName, _ := schemaMap["type"].(string)
	switch typeName {
	case "object":
		payloadMap, ok := payload.(map[string]any)
		if !ok {
			*errors = append(*errors, fmt.Sprintf("path=%s message=expected object", path))
			return
		}
		requiredNames := requiredNameSet(schemaMap["required"])
		for name := range requiredNames {
			if _, exists := payloadMap[name]; !exists {
				*errors = append(*errors, fmt.Sprintf("path=%s message=missing required property %s", path, name))
			}
		}
		properties, _ := schemaMap["properties"].(map[string]any)
		for key, value := range payloadMap {
			if propertySchema, ok := properties[key]; ok {
				validateAgainstSchema(propertySchema, value, path+"."+key, errors)
			} else if additional, ok := schemaMap["additionalProperties"].(bool); ok && !additional {
				*errors = append(*errors, fmt.Sprintf("path=%s message=unexpected property %s", path, key))
			}
		}
	case "array":
		payloadSlice, ok := payload.([]any)
		if !ok {
			*errors = append(*errors, fmt.Sprintf("path=%s message=expected array", path))
			return
		}
		itemSchema := schemaMap["items"]
		for index, item := range payloadSlice {
			validateAgainstSchema(itemSchema, item, fmt.Sprintf("%s[%d]", path, index), errors)
		}
	case "string":
		if _, ok := payload.(string); !ok {
			*errors = append(*errors, fmt.Sprintf("path=%s message=expected string", path))
		}
	case "integer":
		switch payload.(type) {
		case int, int32, int64, float64, json.Number:
			// tolerated
		default:
			*errors = append(*errors, fmt.Sprintf("path=%s message=expected integer", path))
		}
	case "number":
		switch payload.(type) {
		case int, int32, int64, float64, json.Number:
		default:
			*errors = append(*errors, fmt.Sprintf("path=%s message=expected number", path))
		}
	case "boolean":
		if _, ok := payload.(bool); !ok {
			*errors = append(*errors, fmt.Sprintf("path=%s message=expected boolean", path))
		}
	}
}

func normalizeTypeName(value string) string {
	lowered := strings.ToLower(value)
	typeMap := map[string]string{
		"array":   "array",
		"boolean": "boolean",
		"integer": "integer",
		"int64":   "integer",
		"long":    "integer",
		"number":  "number",
		"object":  "object",
		"string":  "string",
	}
	if normalized, ok := typeMap[lowered]; ok {
		return normalized
	}
	for _, token := range []string{"enum", "country", "states", "city", "campus", "building", "floor"} {
		if strings.Contains(lowered, token) {
			return "string"
		}
	}
	return value
}

func mergeSchemaBranch(schema map[string]any, branch map[string]any, keyword string) map[string]any {
	merged := map[string]any{}
	for key, value := range schema {
		if key != keyword {
			merged[key] = cloneMap(value)
		}
	}
	for key, value := range branch {
		switch key {
		case "properties":
			existing, _ := merged["properties"].(map[string]any)
			branchProps, _ := value.(map[string]any)
			next := map[string]any{}
			for propKey, propValue := range existing {
				next[propKey] = propValue
			}
			for propKey, propValue := range branchProps {
				next[propKey] = propValue
			}
			merged[key] = next
		case "required":
			merged[key] = mergeRequiredLists(merged["required"], value)
		default:
			merged[key] = cloneMap(value)
		}
	}
	return merged
}

func mergeRequiredLists(left any, right any) []any {
	seen := map[string]bool{}
	result := []any{}
	for _, source := range []any{left, right} {
		items, _ := source.([]any)
		for _, item := range items {
			name, ok := item.(string)
			if !ok || seen[name] {
				continue
			}
			seen[name] = true
			result = append(result, name)
		}
	}
	return result
}

func shouldDropEmptyOptionalEnumValue(key string, value any, propertySchema map[string]any, requiredNames map[string]bool) bool {
	if requiredNames[key] || value != "" {
		return false
	}
	if propertySchema["type"] != "string" {
		return false
	}
	enumValues, ok := propertySchema["enum"].([]any)
	if !ok {
		return false
	}
	for _, enumValue := range enumValues {
		if enumValue == "" {
			return false
		}
	}
	return true
}

func requiredNameSet(value any) map[string]bool {
	result := map[string]bool{}
	items, _ := value.([]any)
	for _, item := range items {
		if name, ok := item.(string); ok {
			result[name] = true
		}
	}
	return result
}

func enumContains(values []any, payload any) bool {
	for _, value := range values {
		if fmt.Sprintf("%v", value) == fmt.Sprintf("%v", payload) {
			return true
		}
	}
	return false
}

func loadJSON(path string) (map[string]any, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var parsed map[string]any
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return nil, err
	}
	return parsed, nil
}

func cloneMap(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := map[string]any{}
		for key, nested := range typed {
			result[key] = cloneMap(nested)
		}
		return result
	case []any:
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			result = append(result, cloneMap(item))
		}
		return result
	default:
		return value
	}
}

func specTitle(spec map[string]any, fallback string) string {
	info, _ := spec["info"].(map[string]any)
	title, _ := info["title"].(string)
	if title == "" {
		return fallback
	}
	return title
}

func firstServerURL(spec map[string]any) string {
	servers, _ := spec["servers"].([]any)
	if len(servers) == 0 {
		return ""
	}
	serverMap, _ := servers[0].(map[string]any)
	return strings.TrimRight(stringValue(serverMap["url"]), "/")
}

func templatePathToRegex(template string) *regexp.Regexp {
	pattern := regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(template, `[^/]+`)
	return regexp.MustCompile("^" + pattern + "$")
}

func extractParameters(value any) []map[string]any {
	items, _ := value.([]any)
	result := []map[string]any{}
	for _, item := range items {
		if mapping, ok := item.(map[string]any); ok {
			result = append(result, mapping)
		}
	}
	return result
}

func stringValue(value any) string {
	if result, ok := value.(string); ok {
		return result
	}
	return ""
}
