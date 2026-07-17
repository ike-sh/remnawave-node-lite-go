package httpserver

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/klauspost/compress/zstd"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/bodylimit"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/stats"
)

func newJSONRequest(method, target string, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, target, body)
	request.Header.Set("Content-Type", "application/json")
	return request
}

func TestDTOParsingTranscodesUTF16(t *testing.T) {
	for _, test := range []struct {
		name        string
		charset     string
		bodyEncoder transform.Transformer
	}{
		{name: "little endian", charset: "utf-16le", bodyEncoder: unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewEncoder()},
		{name: "big endian", charset: "utf-16be", bodyEncoder: unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM).NewEncoder()},
		{name: "BOM detected", charset: "utf-16", bodyEncoder: unicode.UTF16(unicode.BigEndian, unicode.UseBOM).NewEncoder()},
	} {
		t.Run(test.name, func(t *testing.T) {
			encoded, _, err := transform.Bytes(test.bodyEncoder, []byte(`{"reset":false}`))
			if err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int64
			server := &Server{statsService: stats.NewService(countingStatsProvider{calls: &calls}, nil)}
			request := httptest.NewRequest(http.MethodPost, "/node/stats/get-users-stats", bytes.NewReader(encoded))
			request.Header.Set("Content-Type", "application/json; charset="+test.charset)
			response := httptest.NewRecorder()

			server.handleNodeRoutes(response, request)

			if response.Code != http.StatusOK || calls.Load() != 1 {
				t.Fatalf("response = %d calls=%d body=%s", response.Code, calls.Load(), response.Body.String())
			}
		})
	}
}

func TestDTOParsingRejectsFalseUTF16Declaration(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	server := &Server{statsService: stats.NewService(countingStatsProvider{calls: &calls}, nil)}
	request := httptest.NewRequest(http.MethodPost, "/node/stats/get-users-stats", strings.NewReader(`{"reset":false}`))
	request.Header.Set("Content-Type", "application/json; charset=utf-16le")
	response := httptest.NewRecorder()

	server.handleNodeRoutes(response, request)

	if response.Code != http.StatusBadRequest || calls.Load() != 0 {
		t.Fatalf("response = %d calls=%d body=%s", response.Code, calls.Load(), response.Body.String())
	}
}

func TestNodeTransportReturns413AtEveryBodyExpansionLayer(t *testing.T) {
	if err := bodylimit.Configure(false, 1); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bodylimit.Configure(false, 0) })

	largeUTF8 := `{"reset":false,"ignored":"` + strings.Repeat("x", 2<<20) + `"}`
	largeTranscoded := `{"reset":false,"ignored":"` + strings.Repeat("汉", 360_000) + `"}`
	utf16Body, _, err := transform.Bytes(
		unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewEncoder(),
		[]byte(largeTranscoded),
	)
	if err != nil {
		t.Fatal(err)
	}
	encoder, err := zstd.NewWriter(nil, zstd.WithWindowSize(128<<10))
	if err != nil {
		t.Fatal(err)
	}
	zstdBody := encoder.EncodeAll([]byte(largeUTF8), nil)
	encoder.Close()

	for _, test := range []struct {
		name            string
		body            []byte
		contentEncoding string
		charset         string
	}{
		{name: "identity", body: []byte(largeUTF8)},
		{name: "decompressed", body: zstdBody, contentEncoding: "zstd"},
		{name: "transcoded", body: utf16Body, charset: "utf-16le"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int64
			server := &Server{statsService: stats.NewService(countingStatsProvider{calls: &calls}, nil)}
			handler := bodylimit.DecompressMiddleware(bodylimit.LimitMiddleware(http.HandlerFunc(server.handleNodeRoutes)))
			request := httptest.NewRequest(http.MethodPost, "/node/stats/get-users-stats", bytes.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			if test.contentEncoding != "" {
				request.Header.Set("Content-Encoding", test.contentEncoding)
			}
			if test.charset != "" {
				request.Header.Set("Content-Type", "application/json; charset="+test.charset)
			}
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != http.StatusRequestEntityTooLarge || calls.Load() != 0 {
				t.Fatalf("response = %d calls=%d body=%s", response.Code, calls.Load(), response.Body.String())
			}
		})
	}
}

func TestUnknownContentEncodingPrecedesNoDTOHandler(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	server := &Server{statsService: stats.NewService(countingStatsProvider{calls: &calls}, nil)}
	handler := bodylimit.DecompressMiddleware(bodylimit.LimitMiddleware(http.HandlerFunc(server.handleNodeRoutes)))
	request := httptest.NewRequest(http.MethodGet, "/node/stats/get-system-stats", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Content-Encoding", "snappy")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnsupportedMediaType || calls.Load() != 0 {
		t.Fatalf("response = %d calls=%d body=%s", response.Code, calls.Load(), response.Body.String())
	}
}

func TestDTOParsingAcceptsDecompressedBodyWithUnknownLength(t *testing.T) {
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	compressed := encoder.EncodeAll([]byte(`{"reset":false}`), nil)
	encoder.Close()

	var calls atomic.Int64
	server := &Server{statsService: stats.NewService(countingStatsProvider{calls: &calls}, nil)}
	handler := bodylimit.DecompressMiddleware(bodylimit.LimitMiddleware(http.HandlerFunc(server.handleNodeRoutes)))
	request := newJSONRequest(
		http.MethodPost,
		"/node/stats/get-users-stats",
		bytes.NewReader(compressed),
	)
	request.Header.Set("Content-Encoding", "zstd")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1", calls.Load())
	}
}
