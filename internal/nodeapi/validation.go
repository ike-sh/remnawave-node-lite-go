package nodeapi

import (
	"fmt"
	"net"
	"regexp"
	"strings"
)

const maxValidationIssues = 64

var uuidPattern = regexp.MustCompile(
	`^[0-9a-fA-F]{8}\b-[0-9a-fA-F]{4}\b-[0-9a-fA-F]{4}\b-[0-9a-fA-F]{4}\b-[0-9a-fA-F]{12}$`,
)

// This is the sole zone-bearing alternation in Zod 3.25.76's IPv6 regex.
var scopedIPv6Pattern = regexp.MustCompile(
	`^fe80:(?::[0-9a-fA-F]{0,4}){0,4}%[0-9a-zA-Z]{1,}$`,
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
	if !validIP(*value) {
		return []Issue{{
			Code:       "invalid_string",
			Validation: "ip",
			Path:       nonNilPath(path),
			Message:    "Invalid ip",
		}}
	}
	return nil
}

func validIP(value string) bool {
	return net.ParseIP(value) != nil || scopedIPv6Pattern.MatchString(value)
}

func invalidEnumIssue(path []any, received any, options ...any) Issue {
	received = boundIssueValue(received)
	if len(options) > maxIssueOptions {
		options = options[:maxIssueOptions]
	}
	for index := range options {
		options[index] = boundIssueValue(options[index])
	}
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

func tooBigArrayIssue(path []any, maximum int) Issue {
	inclusive := true
	exact := false
	return Issue{
		Code:      "too_big",
		Maximum:   &maximum,
		Type:      "array",
		Inclusive: &inclusive,
		Exact:     &exact,
		Path:      nonNilPath(path),
		Message:   fmt.Sprintf("Array must contain at most %d element(s)", maximum),
	}
}

func appendValidationIssues(issues []Issue, additions ...Issue) []Issue {
	remaining := maxValidationIssues - len(issues)
	if remaining <= 0 {
		return issues
	}
	if len(additions) > remaining {
		additions = additions[:remaining]
	}
	return append(issues, additions...)
}

func validationIssueLimitReached(issues []Issue) bool {
	return len(issues) >= maxValidationIssues
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
