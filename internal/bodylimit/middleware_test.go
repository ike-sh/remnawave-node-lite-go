package bodylimit

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

func TestDecompressMiddlewareSupportedEncodings(t *testing.T) {
	original := []byte(`{"hello":"world"}`)
	for _, encoding := range []string{"identity", "gzip", "deflate", "br", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			compressed := encodeBody(t, encoding, original)
			var got []byte
			handler := DecompressMiddleware(LimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, readErr := io.ReadAll(r.Body)
				if readErr != nil {
					t.Fatal(readErr)
				}
				got = body
			})))

			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(compressed))
			req.Header.Set("Content-Encoding", encoding)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if !bytes.Equal(got, original) {
				t.Fatalf("decoded body = %q, want %q", got, original)
			}
			if encoding != "identity" && req.Header.Get("Content-Encoding") != "" {
				t.Fatalf("Content-Encoding = %q after decoding", req.Header.Get("Content-Encoding"))
			}
		})
	}
}

func TestDecodedReadCloserAllowsExactLimitAndRejectsOverflow(t *testing.T) {
	for _, test := range []struct {
		name      string
		body      []byte
		limit     int64
		wantLarge bool
	}{
		{name: "exact", body: []byte("1234"), limit: 4},
		{name: "overflow", body: []byte("12345"), limit: 4, wantLarge: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := &decodedReadCloser{
				reader:   bytes.NewReader(test.body),
				original: io.NopCloser(bytes.NewReader(nil)),
				limit:    test.limit,
			}
			defer reader.Close()
			got, err := io.ReadAll(reader)
			var limitError *http.MaxBytesError
			if errors.As(err, &limitError) != test.wantLarge {
				t.Fatalf("read error = %v, want payload-too-large=%v", err, test.wantLarge)
			}
			if !test.wantLarge && !bytes.Equal(got, test.body) {
				t.Fatalf("decoded body = %q, want %q", got, test.body)
			}
		})
	}
}

func TestLowMemoryBodyLimit(t *testing.T) {
	if err := Configure(true, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Configure(false, 0) })
	if got := MaxBytesLimit(); got != lowMemoryMaxBytes {
		t.Fatalf("low-memory body limit = %d, want %d", got, lowMemoryMaxBytes)
	}
}

func TestConfiguredBodyLimitValidation(t *testing.T) {
	if err := Configure(false, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Configure(false, 0) })

	for _, value := range []int{-1, maxConfiguredMB + 1} {
		if err := Configure(false, value); err == nil {
			t.Fatalf("Configure(%d) succeeded", value)
		}
		if got := MaxBytesLimit(); got != defaultMaxBytes {
			t.Fatalf("invalid Configure(%d) changed limit to %d", value, got)
		}
	}

	if err := Configure(false, maxConfiguredMB); err != nil {
		t.Fatal(err)
	}
	if got, want := MaxBytesLimit(), int64(maxConfiguredMB)<<20; got != want {
		t.Fatalf("configured limit = %d, want %d", got, want)
	}
}

func TestLowMemoryRejectsConfiguredLimitAboveMemoryEnvelope(t *testing.T) {
	if err := Configure(false, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Configure(false, 0) })

	if err := Configure(true, lowMemoryMaxBytes>>20); err != nil {
		t.Fatalf("configure low-memory ceiling: %v", err)
	}
	if err := Configure(true, (lowMemoryMaxBytes>>20)+1); err == nil {
		t.Fatal("LOW_MEMORY=1 accepted BODY_LIMIT_MB above 16 MiB")
	}
	if got := MaxBytesLimit(); got != lowMemoryMaxBytes {
		t.Fatalf("invalid configuration changed limit to %d", got)
	}
}

func TestRequestLimitUsesSmallerRouteOrConfiguredCeiling(t *testing.T) {
	previous := maxBytes.Swap(128 << 10)
	t.Cleanup(func() { maxBytes.Store(previous) })

	request := httptest.NewRequest(http.MethodPost, "/", nil)
	for _, test := range []struct {
		name       string
		routeLimit int64
		want       int64
	}{
		{name: "route is smaller", routeLimit: 64 << 10, want: 64 << 10},
		{name: "configured ceiling is smaller", routeLimit: 256 << 10, want: 128 << 10},
	} {
		t.Run(test.name, func(t *testing.T) {
			limited := WithRequestLimit(request, test.routeLimit)
			if got := RequestLimit(limited); got != test.want {
				t.Fatalf("RequestLimit() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestZstdWindowLimitTracksRequestBudget(t *testing.T) {
	for _, test := range []struct {
		requestLimit int64
		want         int64
	}{
		{requestLimit: 64 << 10, want: 64 << 10},
		{requestLimit: 256 << 10, want: 256 << 10},
		{requestLimit: 16 << 20, want: 16 << 20},
		{requestLimit: 64 << 20, want: maxZstdWindowBytes},
	} {
		if got := zstdWindowLimit(test.requestLimit); got != test.want {
			t.Errorf("zstdWindowLimit(%d) = %d, want %d", test.requestLimit, got, test.want)
		}
	}
}

func TestZstdDecoderAcceptsLegal64KiBWindow(t *testing.T) {
	original := bytes.Repeat([]byte("a"), 64<<10)
	encoder, err := zstd.NewWriter(
		nil,
		zstd.WithWindowSize(64<<10),
		zstd.WithSingleSegment(false),
	)
	if err != nil {
		t.Fatal(err)
	}
	compressed := encoder.EncodeAll(original, nil)
	encoder.Close()
	var header zstd.Header
	if err := header.Decode(compressed); err != nil {
		t.Fatalf("decode zstd header: %v", err)
	}
	if header.SingleSegment || header.WindowSize != 64<<10 {
		t.Fatalf("zstd frame singleSegment=%v window=%d, want false/65536", header.SingleSegment, header.WindowSize)
	}

	var got []byte
	handler := DecompressMiddleware(LimitMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, err = io.ReadAll(r.Body)
	})))
	request := WithRequestLimit(
		httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(compressed)),
		64<<10,
	)
	request.Header.Set("Content-Encoding", "zstd")
	handler.ServeHTTP(httptest.NewRecorder(), request)

	if err != nil {
		t.Fatalf("decode legal 64 KiB window: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("decoded body = %q, want %q", got, original)
	}
}

func TestDecoderSlotsAreBoundedAndCancelable(t *testing.T) {
	releaseFirst, err := acquireDecoder(context.Background())
	if err != nil {
		t.Fatalf("acquire first decoder slot: %v", err)
	}
	defer releaseFirst()
	releaseSecond, err := acquireDecoder(context.Background())
	if err != nil {
		t.Fatalf("acquire second decoder slot: %v", err)
	}
	defer releaseSecond()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if release, err := acquireDecoder(ctx); !errors.Is(err, context.Canceled) {
		if release != nil {
			release()
		}
		t.Fatalf("decoder wait error = %v, want context canceled", err)
	} else if release != nil {
		release()
		t.Fatal("canceled decoder wait returned a release function")
	}
}

func TestDecompressMiddlewareRejectsUnknownEncoding(t *testing.T) {
	called := false
	handler := DecompressMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte("body")))
	req.Header.Set("Content-Encoding", "snappy")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415; body=%s", rec.Code, rec.Body.String())
	}
	if called {
		t.Fatal("next handler ran for unsupported content encoding")
	}
}

func TestDecompressMiddlewareBoundsDecodedBytesForEveryEncoding(t *testing.T) {
	previous := maxBytes.Swap(64 << 10)
	t.Cleanup(func() { maxBytes.Store(previous) })
	original := bytes.Repeat([]byte("a"), 128<<10)

	for _, encoding := range []string{"gzip", "deflate", "br", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			var readErr error
			handler := DecompressMiddleware(LimitMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				_, readErr = io.ReadAll(r.Body)
			})))
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(encodeBody(t, encoding, original)))
			req.Header.Set("Content-Encoding", encoding)
			handler.ServeHTTP(httptest.NewRecorder(), req)

			var limitError *http.MaxBytesError
			if !errors.As(readErr, &limitError) {
				t.Fatalf("read error = %v, want *http.MaxBytesError", readErr)
			}
		})
	}
}

func TestDecompressMiddlewareReturnsRetryable503OnDeadline(t *testing.T) {
	releaseFirst, err := acquireDecoder(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer releaseFirst()
	releaseSecond, err := acquireDecoder(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer releaseSecond()

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	request := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(encodeBody(t, "gzip", []byte("body")))).WithContext(ctx)
	request.Header.Set("Content-Encoding", "gzip")
	response := httptest.NewRecorder()
	DecompressMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("deadline-exceeded request reached downstream handler")
	})).ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") != "1" {
		t.Fatalf("response = %d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	var body struct {
		StatusCode int    `json:"statusCode"`
		Message    string `json:"message"`
		Error      string `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.StatusCode != http.StatusServiceUnavailable || body.Error != http.StatusText(http.StatusServiceUnavailable) {
		t.Fatalf("response body = %+v", body)
	}
}

func TestDecompressMiddlewareAbortsSilentlyOnClientCancellation(t *testing.T) {
	releaseFirst, err := acquireDecoder(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer releaseFirst()
	releaseSecond, err := acquireDecoder(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer releaseSecond()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(encodeBody(t, "gzip", []byte("body")))).WithContext(ctx)
	request.Header.Set("Content-Encoding", "gzip")
	response := httptest.NewRecorder()
	defer func() {
		if recovered := recover(); recovered != http.ErrAbortHandler {
			t.Fatalf("panic = %#v, want http.ErrAbortHandler", recovered)
		}
		if response.Body.Len() != 0 {
			t.Fatalf("client cancellation wrote response %q", response.Body.String())
		}
	}()
	DecompressMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("canceled request reached downstream handler")
	})).ServeHTTP(response, request)
}

func TestDecompressMiddlewareWaitsForDecoderCapacity(t *testing.T) {
	releaseFirst, err := acquireDecoder(context.Background())
	if err != nil {
		t.Fatalf("acquire first decoder slot: %v", err)
	}
	defer releaseFirst()
	releaseSecond, err := acquireDecoder(context.Background())
	if err != nil {
		t.Fatalf("acquire second decoder slot: %v", err)
	}
	var releaseSecondOnce sync.Once
	defer releaseSecondOnce.Do(releaseSecond)

	called := make(chan struct{})
	handler := DecompressMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(called)
	}))
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(encodeBody(t, "gzip", []byte("body"))))
	req.Header.Set("Content-Encoding", "gzip")
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(rec, req)
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("compressed request completed while all decoder slots were occupied")
	case <-time.After(20 * time.Millisecond):
	}
	releaseSecondOnce.Do(releaseSecond)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("compressed request did not resume after decoder capacity became available")
	}
	select {
	case <-called:
	default:
		t.Fatal("next handler did not run after decoder capacity became available")
	}
}

func encodeBody(t *testing.T, encoding string, body []byte) []byte {
	t.Helper()
	if encoding == "identity" {
		return append([]byte(nil), body...)
	}
	var destination bytes.Buffer
	var writer io.WriteCloser
	var err error
	switch encoding {
	case "gzip":
		writer = gzip.NewWriter(&destination)
	case "deflate":
		writer = zlib.NewWriter(&destination)
	case "br":
		writer = brotli.NewWriter(&destination)
	case "zstd":
		writer, err = zstd.NewWriter(&destination, zstd.WithEncoderConcurrency(1))
	default:
		t.Fatalf("unsupported test encoding %q", encoding)
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return destination.Bytes()
}
