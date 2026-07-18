package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJSONResponseWritersDoNotEscapeHTMLCharacters(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		call       func(http.ResponseWriter)
		wantStatus int
		wantBody   string
	}{
		{
			name: "plain response",
			call: func(w http.ResponseWriter) {
				writeJSON(w, http.StatusCreated, map[string]any{"marker": "<>&"})
			},
			wantStatus: http.StatusCreated,
			wantBody:   `{"marker":"<>&"}` + "\n",
		},
		{
			name: "node envelope",
			call: func(w http.ResponseWriter) {
				writeNodeResponse(w, map[string]any{"marker": "<>&"})
			},
			wantStatus: http.StatusOK,
			wantBody:   `{"response":{"marker":"<>&"}}` + "\n",
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			response := httptest.NewRecorder()

			test.call(response)

			if got := response.Code; got != test.wantStatus {
				t.Fatalf("response status = %d, want %d", got, test.wantStatus)
			}
			if got := response.Body.String(); got != test.wantBody {
				t.Fatalf("response body = %q, want %q", got, test.wantBody)
			}
			if strings.Contains(response.Body.String(), `\u003`) {
				t.Fatalf("response contains an HTML escape: %q", response.Body.String())
			}
			if got := response.Header().Get("Content-Type"); got != "application/json" {
				t.Fatalf("Content-Type = %q, want application/json", got)
			}
		})
	}
}
