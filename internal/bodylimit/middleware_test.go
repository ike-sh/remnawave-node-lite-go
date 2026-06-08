package bodylimit

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestDecompressMiddlewareZstd(t *testing.T) {
	t.Parallel()

	original := []byte(`{"hello":"world"}`)
	var compressed bytes.Buffer
	encoder, err := zstd.NewWriter(&compressed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := encoder.Write(original); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Close(); err != nil {
		t.Fatal(err)
	}

	var got []byte
	handler := DecompressMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			t.Fatal(readErr)
		}
		got = body
	}))

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(compressed.Bytes()))
	req.Header.Set("Content-Encoding", "zstd")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !bytes.Equal(got, original) {
		t.Fatalf("decoded body = %q, want %q", got, original)
	}
}
