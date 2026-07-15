package nodeapi

import (
	"fmt"
	"net"
	"regexp"
	"strings"
)

var uuidPattern = regexp.MustCompile(
	`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
)

func invalidUUIDIssue(path []any) Issue {
	return Issue{
		Code:       "invalid_string",
		Validation: "uuid",
		Path:       nonNilPath(path),
		Message:    "Invalid uuid",
	}
}

func validateUUID(value *string, path []any) []Issue {
	if value == nil {
		return []Issue{MissingIssue(path, "string")}
	}
	if !uuidPattern.MatchString(*value) {
		return []Issue{invalidUUIDIssue(path)}
	}
	return nil
}

func validateIP(value *string, path []any) []Issue {
	if value == nil {
		return []Issue{MissingIssue(path, "string")}
	}
	if net.ParseIP(*value) == nil {
		return []Issue{{
			Code:       "invalid_string",
			Validation: "ip",
			Path:       nonNilPath(path),
			Message:    "Invalid ip",
		}}
	}
	return nil
}

func invalidEnumIssue(path []any, received any, options ...any) Issue {
	formatted := make([]string, 0, len(options))
	for _, option := range options {
		formatted = append(formatted, fmt.Sprintf("'%v'", option))
	}
	return Issue{
		Code:     "invalid_enum_value",
		Options:  options,
		Received: received,
		Path:     nonNilPath(path),
		Message: fmt.Sprintf(
			"Invalid enum value. Expected %s, received '%v'",
			strings.Join(formatted, " | "),
			received,
		),
	}
}

func invalidDiscriminatorIssue(path []any, options ...any) Issue {
	formatted := make([]string, 0, len(options))
	for _, option := range options {
		formatted = append(formatted, fmt.Sprintf("'%v'", option))
	}
	return Issue{
		Code:    "invalid_union_discriminator",
		Options: options,
		Path:    nonNilPath(path),
		Message: "Invalid discriminator value. Expected " + strings.Join(formatted, " | "),
	}
}

func tooSmallArrayIssue(path []any, minimum int) Issue {
	inclusive := true
	exact := false
	return Issue{
		Code:      "too_small",
		Minimum:   &minimum,
		Type:      "array",
		Inclusive: &inclusive,
		Exact:     &exact,
		Path:      nonNilPath(path),
		Message:   fmt.Sprintf("Array must contain at least %d element(s)", minimum),
	}
}

func requireString(value *string, path []any) []Issue {
	if value == nil {
		return []Issue{MissingIssue(path, "string")}
	}
	return nil
}

func appendPath(path []any, elements ...any) []any {
	result := make([]any, 0, len(path)+len(elements))
	result = append(result, path...)
	return append(result, elements...)
}
