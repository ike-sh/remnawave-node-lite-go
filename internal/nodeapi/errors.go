package nodeapi

import (
	"errors"
	"time"
)

// Issue mirrors the fields emitted by Zod for the validation rules used by
// the Node API. Path elements are strings or array indexes.
type Issue struct {
	Code       string `json:"code"`
	Expected   string `json:"expected,omitempty"`
	Received   string `json:"received,omitempty"`
	Minimum    *int   `json:"minimum,omitempty"`
	Type       string `json:"type,omitempty"`
	Inclusive  *bool  `json:"inclusive,omitempty"`
	Exact      *bool  `json:"exact,omitempty"`
	Validation string `json:"validation,omitempty"`
	Options    []any  `json:"options,omitempty"`
	Path       []any  `json:"path"`
	Message    string `json:"message"`
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
	return &ValidationError{
		StatusCode: 400,
		Message:    "Validation failed",
		Errors:     issues,
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

func InvalidTypeIssue(path []any, expected, received string) Issue {
	return Issue{
		Code:     "invalid_type",
		Expected: expected,
		Received: received,
		Path:     nonNilPath(path),
		Message:  "Expected " + expected + ", received " + received,
	}
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
