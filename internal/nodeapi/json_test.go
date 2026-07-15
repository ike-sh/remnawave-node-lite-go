package nodeapi_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/contract"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/nodeapi"
)

func TestDecodeJSONAcceptsOneDocumentAndStripsUnknownFields(t *testing.T) {
	t.Parallel()

	var request nodeapi.ResetRequest
	err := nodeapi.DecodeJSON(strings.NewReader(`{"reset":false,"ignored":"value"}`), &request)
	if err != nil {
		t.Fatalf("DecodeJSON() error = %+v", err)
	}
	if request.Reset == nil || *request.Reset {
		t.Fatalf("reset = %v, want false", request.Reset)
	}
}

func TestDecodeJSONValidationFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		code string
		path []any
	}{
		{name: "empty", body: "", code: "invalid_type", path: []any{}},
		{name: "malformed", body: `{"reset":`, code: "invalid_json", path: []any{}},
		{name: "missing", body: `{}`, code: "invalid_type", path: []any{"reset"}},
		{name: "wrong type", body: `{"reset":"false"}`, code: "invalid_type", path: []any{"reset"}},
		{name: "trailing document", body: `{"reset":false}{"reset":true}`, code: "invalid_json", path: []any{}},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var request nodeapi.ResetRequest
			validation := nodeapi.DecodeJSON(strings.NewReader(test.body), &request)
			if validation == nil {
				t.Fatal("DecodeJSON() error = nil")
			}
			if validation.StatusCode != 400 || validation.Message != "Validation failed" {
				t.Fatalf("validation = %+v", validation)
			}
			if len(validation.Errors) == 0 || validation.Errors[0].Code != test.code {
				t.Fatalf("issues = %+v, want first code %q", validation.Errors, test.code)
			}
			if got, want := pathJSON(validation.Errors[0].Path), pathJSON(test.path); got != want {
				t.Fatalf("path = %s, want %s", got, want)
			}

			raw, err := json.Marshal(validation)
			if err != nil {
				t.Fatal(err)
			}
			if err := contract.OfficialErrors.ValidationResponse.ValidateJSON(raw); err != nil {
				t.Fatalf("validation response violates official schema: %v\n%s", err, raw)
			}
		})
	}
}

func TestDecodeJSONReportsAllMissingFields(t *testing.T) {
	t.Parallel()

	var request nodeapi.TagResetRequest
	validation := nodeapi.DecodeJSON(strings.NewReader(`{}`), &request)
	if validation == nil || len(validation.Errors) != 2 {
		t.Fatalf("validation = %+v, want two issues", validation)
	}
}

func pathJSON(path []any) string {
	raw, _ := json.Marshal(path)
	return string(raw)
}
