package bodylimit

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestDecompressMiddlewareZstd(t *testing.T) {
	original := []byte(`{"hello":"world"}`)
	compressed := compressZstd(t, original)

	var got []byte
	handler := DecompressMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			t.Fatal(readErr)
		}
		got = body
	}))

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(compressed))
	req.Header.Set("Content-Encoding", "zstd")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !bytes.Equal(got, original) {
		t.Fatalf("decoded body = %q, want %q", got, original)
	}
}

func TestZstdReadCloserAllowsExactLimitAndRejectsOverflow(t *testing.T) {
	for _, test := range []struct {
		name    string
		body    []byte
		limit   int64
		wantErr error
	}{
		{name: "exact", body: []byte("1234"), limit: 4},
		{name: "overflow", body: []byte("12345"), limit: 4, wantErr: errDecompressedBodyTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			compressed := compressZstd(t, test.body)
			decoder, err := newZstdDecoder(bytes.NewReader(compressed), test.limit)
			if err != nil {
				t.Fatal(err)
			}
			reader := &zstdReadCloser{decoder: decoder, orig: io.NopCloser(bytes.NewReader(nil)), limit: test.limit}
			defer reader.Close()
			got, err := io.ReadAll(reader)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("read error = %v, want %v", err, test.wantErr)
			}
			if test.wantErr == nil && !bytes.Equal(got, test.body) {
				t.Fatalf("decoded body = %q, want %q", got, test.body)
			}
		})
	}
}

func TestLowMemoryBodyLimit(t *testing.T) {
	Configure(true, 0)
	t.Cleanup(func() { Configure(false, 0) })
	if got := MaxBytesLimit(); got != lowMemoryMaxBytes {
		t.Fatalf("low-memory body limit = %d, want %d", got, lowMemoryMaxBytes)
	}
}

func TestZstdDecoderSlotsAreBoundedAndCancelable(t *testing.T) {
	releaseFirst, ok := acquireZstdDecoder(context.Background())
	if !ok {
		t.Fatal("failed to acquire first decoder slot")
	}
	defer releaseFirst()
	releaseSecond, ok := acquireZstdDecoder(context.Background())
	if !ok {
		t.Fatal("failed to acquire second decoder slot")
	}
	defer releaseSecond()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if release, acquired := acquireZstdDecoder(ctx); acquired {
		release()
		t.Fatal("acquired decoder beyond configured capacity")
	}
}

func compressZstd(t *testing.T, body []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	encoder, err := zstd.NewWriter(&compressed, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := encoder.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}
