package nodeapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
)

type Validator interface {
	Validate() []Issue
}

// DecodeJSON decodes exactly one JSON document. Unknown object fields are
// ignored to match Zod object parsing, while missing and mistyped fields are
// returned as the official validation response shape.
func DecodeJSON(body io.Reader, target any) *ValidationError {
	decoder := json.NewDecoder(body)
	if err := decoder.Decode(target); err != nil {
		return NewValidationError(issueFromDecodeError(err))
	}

	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err == nil {
		return NewValidationError(invalidJSONIssue("Expected a single JSON document"))
	} else if !errors.Is(err, io.EOF) {
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

func issueFromDecodeError(err error) Issue {
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
