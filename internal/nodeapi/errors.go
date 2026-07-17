package nodeapi

import (
	"errors"
	"net/http"
	"time"
	"unicode/utf8"
)

const (
	maxIssueTextBytes    = 512
	maxIssuePathElements = 32
	maxIssueOptions      = 16
)

// Issue mirrors the fields emitted by Zod for the validation rules used by
// the Node API. Path elements are strings or array indexes.
type Issue struct {
	Code       string `json:"code"`
	Expected   string `json:"expected,omitempty"`
	Received   any    `json:"received,omitempty"`
	Minimum    *int   `json:"minimum,omitempty"`
	Maximum    *int   `json:"maximum,omitempty"`
	Type       string `json:"type,omitempty"`
	Inclusive  *bool  `json:"inclusive,omitempty"`
	Exact      *bool  `json:"exact,omitempty"`
	Validation string `json:"validation,omitempty"`
	Options    []any  `json:"options,omitempty"`
	Path       []any  `json:"path"`
	Message    string `json:"message"`
	cause      error
}

type ValidationError struct {
	StatusCode int     `json:"statusCode"`
	Message    string  `json:"message"`
	Errors     []Issue `json:"errors"`
}

func NewValidationError(issues ...Issue) *ValidationError {
	if len(issues) == 0 {
		issues = []Issue{{
			Code:    "custom",
			Path:    []any{},
			Message: "Invalid request",
		}}
	}
	status := http.StatusBadRequest
	if len(issues) > maxValidationIssues {
		issues = issues[:maxValidationIssues]
	}
	bounded := make([]Issue, len(issues))
	for index, issue := range issues {
		if requestBodyTooLarge(issue.cause) {
			status = http.StatusRequestEntityTooLarge
		}
		bounded[index] = boundIssue(issue)
	}
	return &ValidationError{
		StatusCode: status,
		Message:    "Validation failed",
		Errors:     bounded,
	}
}

func MissingIssue(path []any, expected string) Issue {
	return Issue{
		Code:     "invalid_type",
		Expected: expected,
		Received: "undefined",
		Path:     nonNilPath(path),
		Message:  "Required",
	}
}

func InvalidTypeIssue(path []any, expected string, received any) Issue {
	received = boundIssueValue(received)
	return Issue{
		Code:     "invalid_type",
		Expected: expected,
		Received: received,
		Path:     nonNilPath(path),
		Message:  "Expected " + expected + ", received " + receivedName(received),
	}
}

func requestBodyTooLarge(err error) bool {
	if err == nil {
		return false
	}
	var limitError *http.MaxBytesError
	return errors.As(err, &limitError)
}

func boundIssue(issue Issue) Issue {
	issue.Code = boundIssueString(issue.Code)
	issue.Expected = boundIssueString(issue.Expected)
	issue.Received = boundIssueValue(issue.Received)
	issue.Type = boundIssueString(issue.Type)
	issue.Validation = boundIssueString(issue.Validation)
	issue.Message = boundIssueString(issue.Message)

	if len(issue.Options) > maxIssueOptions {
		issue.Options = issue.Options[:maxIssueOptions]
	}
	if len(issue.Options) != 0 {
		options := make([]any, len(issue.Options))
		for index, option := range issue.Options {
			options[index] = boundIssueValue(option)
		}
		issue.Options = options
	}

	path := nonNilPath(issue.Path)
	if len(path) > maxIssuePathElements {
		path = path[:maxIssuePathElements]
	}
	boundedPath := make([]any, len(path))
	for index, element := range path {
		boundedPath[index] = boundIssueValue(element)
	}
	issue.Path = boundedPath
	return issue
}

func boundIssueValue(value any) any {
	switch value := value.(type) {
	case nil, bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return value
	case string:
		return boundIssueString(value)
	default:
		return "invalid value"
	}
}

func boundIssueString(value string) string {
	if len(value) <= maxIssueTextBytes {
		return value
	}
	end := maxIssueTextBytes - 3
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end] + "..."
}

func receivedName(received any) string {
	if value, ok := received.(string); ok {
		return value
	}
	return "invalid value"
}

func nonNilPath(path []any) []any {
	if path == nil {
		return []any{}
	}
	return path
}

// ServiceError is transport-neutral application failure metadata. The HTTP
// adapter adds request-specific fields such as path and timestamp.
type ServiceError struct {
	Status  int
	Code    string
	Message string
}

func (e ServiceError) Error() string {
	return e.Message
}

func AsServiceError(err error) (ServiceError, bool) {
	if err == nil {
		return ServiceError{}, false
	}
	var value ServiceError
	if errors.As(err, &value) {
		return value, true
	}
	var pointer *ServiceError
	if errors.As(err, &pointer) && pointer != nil {
		return *pointer, true
	}
	return ServiceError{}, false
}

type ApplicationError struct {
	Timestamp string `json:"timestamp"`
	Path      string `json:"path"`
	Message   string `json:"message"`
	ErrorCode string `json:"errorCode"`
}

func NewApplicationError(path string, err ServiceError) ApplicationError {
	return ApplicationError{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Path:      path,
		Message:   err.Message,
		ErrorCode: err.Code,
	}
}
