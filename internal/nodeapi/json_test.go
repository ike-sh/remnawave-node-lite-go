package nodeapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
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
		{name: "whitespace", body: " \n\t", code: "invalid_json", path: []any{}},
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

func TestDecodeJSONRejectsCaseCollisionsAndDuplicateKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{name: "wrong case", body: `{"RESET":false}`},
		{name: "case shadow", body: `{"reset":false,"RESET":true}`},
		{name: "duplicate known", body: `{"reset":false,"reset":true}`},
		{name: "duplicate unknown", body: `{"reset":false,"ignored":1,"ignored":2}`},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var request nodeapi.ResetRequest
			validation := nodeapi.DecodeJSON(strings.NewReader(test.body), &request)
			if validation == nil || len(validation.Errors) != 1 || validation.Errors[0].Code != "invalid_json" {
				t.Fatalf("validation = %+v, want one invalid_json issue", validation)
			}
			if request.Reset != nil {
				t.Fatalf("request was mutated before structural validation: %+v", request)
			}
		})
	}
}

func TestDecodeJSONRejectsNestedCaseCollision(t *testing.T) {
	t.Parallel()

	body := `{"affectedInboundTags":[],"users":[{"inboundData":[],"userData":{"USERID":"u"}}]}`
	var request nodeapi.AddUsersRequest
	validation := nodeapi.DecodeJSON(strings.NewReader(body), &request)
	if validation == nil || validation.Errors[0].Code != "invalid_json" {
		t.Fatalf("validation = %+v, want invalid_json", validation)
	}
	if got, want := pathJSON(validation.Errors[0].Path), `["users",0,"userData","USERID"]`; got != want {
		t.Fatalf("path = %s, want %s", got, want)
	}
}

func TestDecodeJSONRejectsUnicodeSimpleFoldShadow(t *testing.T) {
	t.Parallel()

	body := `{"affectedInboundTags":[],"users":[{"inboundData":[],"userData":{"ſsPassword":"shadow"}}]}`
	var request nodeapi.AddUsersRequest
	validation := nodeapi.DecodeJSON(strings.NewReader(body), &request)
	if validation == nil || validation.Errors[0].Code != "invalid_json" {
		t.Fatalf("validation = %+v, want invalid_json", validation)
	}
	if got, want := pathJSON(validation.Errors[0].Path), `["users",0,"userData","ſsPassword"]`; got != want {
		t.Fatalf("path = %s, want %s", got, want)
	}
}

func TestDecodeJSONBoundsArrayWorkAndValidationIssues(t *testing.T) {
	t.Parallel()

	const malformedUsers = 250_000
	body := `{"affectedInboundTags":[],"users":[` + strings.Repeat(`{},`, malformedUsers-1) + `{}` + `]}`
	reader := &countingReader{Reader: strings.NewReader(body)}
	var request nodeapi.AddUsersRequest
	validation := nodeapi.DecodeJSON(reader, &request)
	if validation == nil || validation.Errors[0].Code != "too_big" {
		t.Fatalf("validation = %+v, want too_big", validation)
	}
	if reader.bytesRead >= 256<<10 {
		t.Fatalf("decoder read %d bytes before rejecting oversized array", reader.bytesRead)
	}
	if request.Users != nil {
		t.Fatal("oversized array was decoded into the request")
	}

	boundedBody := `{"affectedInboundTags":[],"users":[` + strings.Repeat(`{},`, 999) + `{}` + `]}`
	validation = nodeapi.DecodeJSON(strings.NewReader(boundedBody), &request)
	if validation == nil {
		t.Fatal("bounded invalid request was accepted")
	}
	if len(validation.Errors) != 64 {
		t.Fatalf("validation issue count = %d, want hard cap 64", len(validation.Errors))
	}
}

func TestValidateJSONDocumentMatchesGlobalStrictParser(t *testing.T) {
	t.Parallel()

	for _, body := range []string{"", `{}`, `[]`} {
		if validation := nodeapi.ValidateJSONDocument(strings.NewReader(body)); validation != nil {
			t.Errorf("body %q rejected: %+v", body, validation)
		}
	}
	for _, body := range []string{" \n\t", `null`, `true`, `1`, `"value"`, `{"broken":`, `{} {`} {
		validation := nodeapi.ValidateJSONDocument(strings.NewReader(body))
		if validation == nil || validation.Errors[0].Code != "invalid_json" {
			t.Errorf("body %q validation = %+v, want invalid_json", body, validation)
		}
	}
}

func TestDecodeJSONBoundsOpaqueCollections(t *testing.T) {
	for _, test := range []struct {
		name      string
		body      string
		wantValid bool
	}{
		{name: "object at limit", body: opaqueObjectBody(65_536), wantValid: true},
		{name: "object over limit", body: opaqueObjectBody(65_537)},
		{name: "array at limit", body: opaqueArrayBody(65_536), wantValid: true},
		{name: "array over limit", body: opaqueArrayBody(65_537)},
	} {
		t.Run(test.name, func(t *testing.T) {
			var request opaqueConfigRequest
			validation := nodeapi.DecodeJSON(strings.NewReader(test.body), &request)
			if (validation == nil) != test.wantValid {
				t.Fatalf("validation = %+v, want valid=%v", validation, test.wantValid)
			}
		})
	}
}

func TestDecodeJSONBoundsTotalDocumentTokens(t *testing.T) {
	var request opaqueConfigRequest
	validation := nodeapi.DecodeJSON(strings.NewReader(opaqueNestedTokenBody(100, 11_000)), &request)
	if validation == nil || validation.Errors[0].Code != "invalid_json" {
		t.Fatalf("validation = %+v, want total token limit rejection", validation)
	}
	if request.Config != nil {
		t.Fatal("token-heavy document was unmarshaled after structural rejection")
	}
}

func TestDecodeJSONBoundsValidationResponseStrings(t *testing.T) {
	t.Parallel()

	large := strings.Repeat("x", 1<<20)
	body := `{"affectedInboundTags":[],"users":[{"inboundData":[{"type":"vless","tag":"in","flow":"` + large + `"}],"userData":{"userId":"u","hashUuid":"00000000-0000-4000-8000-000000000001","vlessUuid":"00000000-0000-4000-8000-000000000002","trojanPassword":"","ssPassword":""}}]}`
	var request nodeapi.AddUsersRequest
	validation := nodeapi.DecodeJSON(strings.NewReader(body), &request)
	if validation == nil {
		t.Fatal("oversized invalid enum value was accepted")
	}
	encoded, err := json.Marshal(validation)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > 8<<10 {
		t.Fatalf("validation response is %d bytes, want <= 8 KiB", len(encoded))
	}
	if received, ok := validation.Errors[0].Received.(string); !ok || len(received) > 512 {
		t.Fatalf("bounded received value = %T/%d bytes", validation.Errors[0].Received, len(received))
	}
}

func TestDecodeJSONRejectsLargeKeyWithoutEchoingIt(t *testing.T) {
	t.Parallel()

	body := `{"reset":false,"` + strings.Repeat("k", 1<<20) + `":true}`
	var request nodeapi.ResetRequest
	validation := nodeapi.DecodeJSON(strings.NewReader(body), &request)
	if validation == nil || validation.Errors[0].Code != "invalid_json" {
		t.Fatalf("validation = %+v, want invalid_json", validation)
	}
	encoded, err := json.Marshal(validation)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > 4<<10 {
		t.Fatalf("validation response echoed the large key: %d bytes", len(encoded))
	}
}

func TestDecodeJSONPreservesMaxBytesErrorStatus(t *testing.T) {
	t.Parallel()

	httpRequest := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"reset":false}`))
	limited := http.MaxBytesReader(httptest.NewRecorder(), httpRequest.Body, 8)
	var request nodeapi.ResetRequest
	validation := nodeapi.DecodeJSON(limited, &request)
	if validation == nil || validation.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("validation = %+v, want status 413", validation)
	}
}

type opaqueConfigRequest struct {
	Config *map[string]any `json:"config"`
}

func opaqueObjectBody(members int) string {
	var body strings.Builder
	body.Grow(members*10 + 16)
	body.WriteString(`{"config":{`)
	for index := 0; index < members; index++ {
		if index != 0 {
			body.WriteByte(',')
		}
		body.WriteByte('"')
		body.WriteString(strconv.Itoa(index))
		body.WriteString(`":0`)
	}
	body.WriteString(`}}`)
	return body.String()
}

func opaqueArrayBody(items int) string {
	var body strings.Builder
	body.Grow(items*2 + 24)
	body.WriteString(`{"config":{"items":[`)
	for index := 0; index < items; index++ {
		if index != 0 {
			body.WriteByte(',')
		}
		body.WriteByte('0')
	}
	body.WriteString(`]}}`)
	return body.String()
}

func opaqueNestedTokenBody(groups, items int) string {
	var body strings.Builder
	body.Grow(groups*items*2 + groups*16 + 24)
	body.WriteString(`{"config":{`)
	for group := 0; group < groups; group++ {
		if group != 0 {
			body.WriteByte(',')
		}
		body.WriteByte('"')
		body.WriteString(strconv.Itoa(group))
		body.WriteString(`":[`)
		for item := 0; item < items; item++ {
			if item != 0 {
				body.WriteByte(',')
			}
			body.WriteByte('0')
		}
		body.WriteByte(']')
	}
	body.WriteString(`}}`)
	return body.String()
}

type countingReader struct {
	io.Reader
	bytesRead int
}

func (r *countingReader) Read(buffer []byte) (int, error) {
	count, err := r.Reader.Read(buffer)
	r.bytesRead += count
	return count, err
}

func pathJSON(path []any) string {
	raw, _ := json.Marshal(path)
	return string(raw)
}
