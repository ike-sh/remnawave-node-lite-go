// Package contract contains the executable compatibility contract distilled
// from the pinned official Remnawave Node implementation.
package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"regexp"
	"strconv"
	"time"
)

type schemaKind uint8

const (
	kindAny schemaKind = iota
	kindObject
	kindArray
	kindString
	kindNumber
	kindInteger
	kindBoolean
)

// Schema models the subset of Zod used by the official 2.8.0 REST contract.
// Values are immutable after construction.
type Schema struct {
	kind       schemaKind
	nullable   bool
	properties map[string]*Schema
	required   map[string]struct{}
	items      *Schema
	minItems   int
	strings    map[string]struct{}
	numbers    map[float64]struct{}
	format     string
	oneOf      []*Schema
}

func anyValue() *Schema {
	return &Schema{kind: kindAny}
}

func object(properties map[string]*Schema, required ...string) *Schema {
	requiredSet := make(map[string]struct{}, len(required))
	for _, name := range required {
		requiredSet[name] = struct{}{}
	}
	return &Schema{kind: kindObject, properties: properties, required: requiredSet}
}

func record(values *Schema) *Schema {
	return &Schema{kind: kindObject, items: values}
}

func array(items *Schema, minItems ...int) *Schema {
	minimum := 0
	if len(minItems) != 0 {
		minimum = minItems[0]
	}
	return &Schema{kind: kindArray, items: items, minItems: minimum}
}

func stringValue() *Schema {
	return &Schema{kind: kindString}
}

func stringFormat(format string) *Schema {
	return &Schema{kind: kindString, format: format}
}

func stringEnum(values ...string) *Schema {
	allowed := make(map[string]struct{}, len(values))
	for _, value := range values {
		allowed[value] = struct{}{}
	}
	return &Schema{kind: kindString, strings: allowed}
}

func numberValue() *Schema {
	return &Schema{kind: kindNumber}
}

func numberEnum(values ...float64) *Schema {
	allowed := make(map[float64]struct{}, len(values))
	for _, value := range values {
		allowed[value] = struct{}{}
	}
	return &Schema{kind: kindNumber, numbers: allowed}
}

func integerValue() *Schema {
	return &Schema{kind: kindInteger}
}

func booleanValue() *Schema {
	return &Schema{kind: kindBoolean}
}

func nullable(schema *Schema) *Schema {
	clone := *schema
	clone.nullable = true
	return &clone
}

func oneOf(schemas ...*Schema) *Schema {
	return &Schema{oneOf: schemas}
}

// ValidateJSON validates one JSON document and rejects trailing documents.
func (s *Schema) ValidateJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return fmt.Errorf("decode JSON: multiple documents")
	} else if err != io.EOF {
		return fmt.Errorf("decode JSON trailer: %w", err)
	}
	return s.validate(value, "$")
}

func (s *Schema) validate(value any, path string) error {
	if value == nil {
		if s.nullable || s.kind == kindAny {
			return nil
		}
		return fmt.Errorf("%s: null is not allowed", path)
	}

	if len(s.oneOf) != 0 {
		matches := 0
		var lastErr error
		for _, candidate := range s.oneOf {
			if err := candidate.validate(value, path); err == nil {
				matches++
			} else {
				lastErr = err
			}
		}
		if matches != 1 {
			return fmt.Errorf("%s: matched %d union variants: %v", path, matches, lastErr)
		}
		return nil
	}

	switch s.kind {
	case kindAny:
		return nil
	case kindObject:
		return s.validateObject(value, path)
	case kindArray:
		return s.validateArray(value, path)
	case kindString:
		return s.validateString(value, path)
	case kindNumber, kindInteger:
		return s.validateNumber(value, path)
	case kindBoolean:
		if _, ok := value.(bool); !ok {
			return typeError(path, "boolean", value)
		}
		return nil
	default:
		return fmt.Errorf("%s: unsupported schema kind %d", path, s.kind)
	}
}

func (s *Schema) validateObject(value any, path string) error {
	objectValue, ok := value.(map[string]any)
	if !ok {
		return typeError(path, "object", value)
	}
	for name := range s.required {
		if _, exists := objectValue[name]; !exists {
			return fmt.Errorf("%s.%s: required property is missing", path, name)
		}
	}
	for name, child := range s.properties {
		childValue, exists := objectValue[name]
		if !exists {
			continue
		}
		if err := child.validate(childValue, path+"."+name); err != nil {
			return err
		}
	}
	if s.items != nil {
		for name, childValue := range objectValue {
			if err := s.items.validate(childValue, path+"."+name); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Schema) validateArray(value any, path string) error {
	arrayValue, ok := value.([]any)
	if !ok {
		return typeError(path, "array", value)
	}
	if len(arrayValue) < s.minItems {
		return fmt.Errorf("%s: item count %d is less than %d", path, len(arrayValue), s.minItems)
	}
	for index, item := range arrayValue {
		if err := s.items.validate(item, fmt.Sprintf("%s[%d]", path, index)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Schema) validateString(value any, path string) error {
	stringValue, ok := value.(string)
	if !ok {
		return typeError(path, "string", value)
	}
	if len(s.strings) != 0 {
		if _, allowed := s.strings[stringValue]; !allowed {
			return fmt.Errorf("%s: %q is not an allowed value", path, stringValue)
		}
	}
	switch s.format {
	case "":
		return nil
	case "uuid":
		if !uuidPattern.MatchString(stringValue) {
			return fmt.Errorf("%s: %q is not a UUID", path, stringValue)
		}
	case "ip":
		if !validIP(stringValue) {
			return fmt.Errorf("%s: %q is not an IP address", path, stringValue)
		}
	case "date-time":
		if !validDateTime(stringValue) {
			return fmt.Errorf("%s: %q is not an ISO date-time", path, stringValue)
		}
	default:
		return fmt.Errorf("%s: unsupported string format %q", path, s.format)
	}
	return nil
}

func (s *Schema) validateNumber(value any, path string) error {
	number, ok := value.(json.Number)
	if !ok {
		return typeError(path, "number", value)
	}
	parsed, err := strconv.ParseFloat(string(number), 64)
	if err != nil || math.IsInf(parsed, 0) || math.IsNaN(parsed) {
		return fmt.Errorf("%s: invalid number %q", path, number)
	}
	if s.kind == kindInteger && math.Trunc(parsed) != parsed {
		return fmt.Errorf("%s: %q is not an integer", path, number)
	}
	if len(s.numbers) != 0 {
		if _, allowed := s.numbers[parsed]; !allowed {
			return fmt.Errorf("%s: %v is not an allowed value", path, parsed)
		}
	}
	return nil
}

func typeError(path, want string, value any) error {
	return fmt.Errorf("%s: expected %s, got %T", path, want, value)
}

func validDateTime(value string) bool {
	layouts := [...]string{
		time.RFC3339Nano,
		"2006-01-02T15:04:05.999999999",
	}
	for _, layout := range layouts {
		if _, err := time.Parse(layout, value); err == nil {
			return true
		}
	}
	return false
}

var uuidPattern = regexp.MustCompile(
	`^[0-9a-fA-F]{8}\b-[0-9a-fA-F]{4}\b-[0-9a-fA-F]{4}\b-[0-9a-fA-F]{4}\b-[0-9a-fA-F]{12}$`,
)

// Zod 3.25.76 package/v3/types.js permits zones only through its
// fe80:(:[0-9a-fA-F]{0,4}){0,4}%[0-9a-zA-Z]{1,} IPv6 alternation.
var scopedIPv6Pattern = regexp.MustCompile(
	`^fe80:(?::[0-9a-fA-F]{0,4}){0,4}%[0-9a-zA-Z]{1,}$`,
)

func validIP(value string) bool {
	return net.ParseIP(value) != nil || scopedIPv6Pattern.MatchString(value)
}

func schemaKindName(schema *Schema) string {
	if len(schema.oneOf) != 0 {
		return "union"
	}
	switch schema.kind {
	case kindObject:
		return "object"
	case kindArray:
		return "array"
	case kindString:
		return "string"
	case kindNumber, kindInteger:
		return "number"
	case kindBoolean:
		return "boolean"
	default:
		return "any"
	}
}

func oppositeValue(schema *Schema) any {
	switch schemaKindName(schema) {
	case "object", "array", "string", "union":
		return true
	case "number":
		return "not-a-number"
	case "boolean":
		return "not-a-boolean"
	default:
		return map[string]any{"unexpected": "x"}
	}
}
