package nodeapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
)

const (
	maxJSONDepth             = 64
	maxJSONKeyBytes          = 1_024
	maxJSONDocumentTokens    = 1_048_576
	maxOpaqueCollectionItems = 65_536
	maxControlArrayItems     = 16_384
	maxControlObjectKeys     = 16_384
)

var errJSONTokenLimit = errors.New("JSON document token limit exceeded")

type Validator interface {
	Validate() []Issue
}

type structuralJSONSchemaProvider interface {
	structuralJSONSchema() any
}

// DecodeJSON decodes exactly one JSON object or array. Unknown object fields
// are ignored to match Zod object parsing. Keys which only differ in case from
// a known field and duplicate keys are rejected before encoding/json can apply
// its case-insensitive or last-value-wins matching rules.
func DecodeJSON(body io.Reader, target any) *ValidationError {
	raw, validation := scanJSONDocument(body, reflect.TypeOf(target), false)
	if validation != nil {
		return validation
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return NewValidationError(issueFromDecodeError(err))
	}

	validator, ok := target.(Validator)
	if !ok {
		return nil
	}
	issues := validator.Validate()
	if len(issues) == 0 {
		return nil
	}
	return NewValidationError(issues...)
}

// ValidateJSONDocument applies the global JSON parser rules used for routes
// without a DTO. An absent body is valid; a present document must be a single
// JSON object or array.
func ValidateJSONDocument(body io.Reader) *ValidationError {
	_, validation := scanJSONDocument(body, nil, true)
	return validation
}

func scanJSONDocument(body io.Reader, schema reflect.Type, allowEmpty bool) ([]byte, *ValidationError) {
	if body == nil {
		if allowEmpty {
			return nil, nil
		}
		return nil, NewValidationError(MissingIssue(nil, "object"))
	}

	var raw bytes.Buffer
	decoder := json.NewDecoder(io.TeeReader(body, &raw))
	decoder.UseNumber()
	scanner := jsonTokenScanner{decoder: decoder}
	first, err := scanner.next()
	if errors.Is(err, io.EOF) {
		if raw.Len() != 0 {
			return nil, NewValidationError(invalidJSONIssue("Invalid JSON body"))
		}
		if allowEmpty {
			return raw.Bytes(), nil
		}
		return nil, NewValidationError(MissingIssue(nil, "object"))
	}
	if err != nil {
		return nil, NewValidationError(issueFromDecodeError(err))
	}
	root, ok := first.(json.Delim)
	if !ok || (root != '{' && root != '[') {
		return nil, NewValidationError(invalidJSONIssue("JSON body must be an object or array"))
	}
	if issue := scanJSONToken(&scanner, first, schema, nil, 1); issue != nil {
		return nil, NewValidationError(*issue)
	}
	if _, err := scanner.next(); err == nil {
		return nil, NewValidationError(invalidJSONIssue("Expected a single JSON document"))
	} else if !errors.Is(err, io.EOF) {
		return nil, NewValidationError(issueFromDecodeError(err))
	}
	return raw.Bytes(), nil
}

type jsonTokenScanner struct {
	decoder *json.Decoder
	tokens  int
}

func (s *jsonTokenScanner) next() (json.Token, error) {
	if s.tokens >= maxJSONDocumentTokens {
		_, err := s.decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil, err
		}
		return nil, errJSONTokenLimit
	}
	token, err := s.decoder.Token()
	if err == nil {
		s.tokens++
	}
	return token, err
}

func scanJSONToken(scanner *jsonTokenScanner, token json.Token, schema reflect.Type, path []any, depth int) *Issue {
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if depth > maxJSONDepth {
		issue := invalidJSONIssue("JSON body exceeds the maximum nesting depth")
		issue.Path = nonNilPath(path)
		return &issue
	}

	switch delim {
	case '{':
		return scanJSONObject(scanner, schema, path, depth)
	case '[':
		elementSchema, limited := controlArraySchema(schema)
		return scanJSONArray(scanner, elementSchema, path, depth, limited)
	default:
		issue := invalidJSONIssue("Invalid JSON body")
		issue.Path = nonNilPath(path)
		return &issue
	}
}

func scanJSONObject(scanner *jsonTokenScanner, schema reflect.Type, path []any, depth int) *Issue {
	fields, hasSchema := schemaFields(schema)
	seen := make(map[string]struct{})
	memberCount := 0
	for scanner.decoder.More() {
		memberCount++
		memberLimit := maxOpaqueCollectionItems
		if hasSchema {
			memberLimit = maxControlObjectKeys
		}
		if memberCount > memberLimit {
			issue := invalidJSONIssue(fmt.Sprintf("JSON object must contain at most %d members", memberLimit))
			issue.Path = nonNilPath(path)
			return &issue
		}

		keyToken, err := scanner.next()
		if err != nil {
			issue := jsonSyntaxIssue(err)
			return &issue
		}
		key, ok := keyToken.(string)
		if !ok {
			issue := invalidJSONIssue("Invalid JSON object key")
			issue.Path = nonNilPath(path)
			return &issue
		}
		if len(key) > maxJSONKeyBytes {
			issue := invalidJSONIssue(fmt.Sprintf("JSON object keys must contain at most %d bytes", maxJSONKeyBytes))
			issue.Path = nonNilPath(path)
			return &issue
		}
		keyPath := appendPath(path, key)
		if _, duplicate := seen[key]; duplicate {
			issue := invalidJSONIssue("Duplicate JSON object key")
			issue.Path = keyPath
			return &issue
		}
		seen[key] = struct{}{}

		var childSchema reflect.Type
		if hasSchema {
			if field, exact := fields.exact[key]; exact {
				childSchema = field
			} else if canonical, collision := fields.foldedName(key); collision {
				issue := invalidJSONIssue(fmt.Sprintf("JSON object key %q must use the exact spelling %q", key, canonical))
				issue.Path = keyPath
				return &issue
			}
		}

		valueToken, err := scanner.next()
		if err != nil {
			issue := jsonSyntaxIssue(err)
			return &issue
		}
		if issue := scanJSONToken(scanner, valueToken, childSchema, keyPath, depth+1); issue != nil {
			return issue
		}
	}
	if _, err := scanner.next(); err != nil {
		issue := jsonSyntaxIssue(err)
		return &issue
	}
	return nil
}

func scanJSONArray(
	scanner *jsonTokenScanner,
	elementSchema reflect.Type,
	path []any,
	depth int,
	limit int,
) *Issue {
	itemCount := 0
	for scanner.decoder.More() {
		if itemCount >= limit {
			issue := tooBigArrayIssue(path, limit)
			return &issue
		}
		itemPath := path
		if elementSchema != nil {
			itemPath = appendPath(path, itemCount)
		}
		itemCount++
		token, err := scanner.next()
		if err != nil {
			issue := jsonSyntaxIssue(err)
			return &issue
		}
		if issue := scanJSONToken(scanner, token, elementSchema, itemPath, depth+1); issue != nil {
			return issue
		}
	}
	if _, err := scanner.next(); err != nil {
		issue := jsonSyntaxIssue(err)
		return &issue
	}
	return nil
}

func jsonSyntaxIssue(err error) Issue {
	if errors.Is(err, errJSONTokenLimit) {
		return invalidJSONIssue(fmt.Sprintf("JSON body must contain at most %d structural tokens", maxJSONDocumentTokens))
	}
	issue := invalidJSONIssue("Invalid JSON body")
	issue.cause = err
	return issue
}

type objectFieldSchema struct {
	exact map[string]reflect.Type
}

func (s objectFieldSchema) foldedName(candidate string) (string, bool) {
	for canonical := range s.exact {
		if strings.EqualFold(candidate, canonical) {
			return canonical, true
		}
	}
	return "", false
}

var objectFieldSchemas sync.Map

func schemaFields(schema reflect.Type) (objectFieldSchema, bool) {
	schema = indirectSchema(schema)
	if schema == nil {
		return objectFieldSchema{}, false
	}
	provider, ok := reflect.New(schema).Interface().(structuralJSONSchemaProvider)
	if ok {
		schema = indirectSchema(reflect.TypeOf(provider.structuralJSONSchema()))
	}
	if schema == nil || schema.Kind() != reflect.Struct {
		return objectFieldSchema{}, false
	}
	if cached, ok := objectFieldSchemas.Load(schema); ok {
		return cached.(objectFieldSchema), true
	}

	fields := objectFieldSchema{
		exact: make(map[string]reflect.Type),
	}
	for index := 0; index < schema.NumField(); index++ {
		field := schema.Field(index)
		if !field.IsExported() {
			continue
		}
		name := field.Name
		if tag, ok := field.Tag.Lookup("json"); ok {
			name = strings.Split(tag, ",")[0]
		}
		if name == "-" || name == "" {
			continue
		}
		fields.exact[name] = field.Type
	}
	objectFieldSchemas.Store(schema, fields)
	return fields, true
}

func controlArraySchema(schema reflect.Type) (reflect.Type, int) {
	schema = indirectSchema(schema)
	if schema == nil || (schema.Kind() != reflect.Array && schema.Kind() != reflect.Slice) {
		return nil, maxOpaqueCollectionItems
	}
	if schema == reflect.TypeOf(json.RawMessage{}) {
		return nil, maxOpaqueCollectionItems
	}
	return schema.Elem(), maxControlArrayItems
}

func indirectSchema(schema reflect.Type) reflect.Type {
	for schema != nil && schema.Kind() == reflect.Pointer {
		schema = schema.Elem()
	}
	return schema
}

func issueFromDecodeError(err error) Issue {
	if requestBodyTooLarge(err) {
		issue := invalidJSONIssue("Request body is too large")
		issue.cause = err
		return issue
	}
	if errors.Is(err, io.EOF) {
		return MissingIssue(nil, "object")
	}

	var typeError *json.UnmarshalTypeError
	if errors.As(err, &typeError) {
		return InvalidTypeIssue(fieldPath(typeError.Field), jsonTypeName(typeError.Type), typeError.Value)
	}

	return invalidJSONIssue("Invalid JSON body")
}

func invalidJSONIssue(message string) Issue {
	return Issue{
		Code:    "invalid_json",
		Path:    []any{},
		Message: message,
	}
}

func fieldPath(field string) []any {
	if field == "" {
		return []any{}
	}
	parts := strings.Split(field, ".")
	path := make([]any, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			path = append(path, lowerFirst(part))
		}
	}
	return path
}

func lowerFirst(value string) string {
	if value == "" {
		return value
	}
	return strings.ToLower(value[:1]) + value[1:]
}

func jsonTypeName(value reflect.Type) string {
	if value == nil {
		return "unknown"
	}
	for value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.Bool:
		return "boolean"
	case reflect.String:
		return "string"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Map, reflect.Struct:
		return "object"
	default:
		return fmt.Sprint(value.Kind())
	}
}
