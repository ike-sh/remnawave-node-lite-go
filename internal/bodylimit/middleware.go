package bodylimit

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/klauspost/compress/zstd"
)

const defaultMaxBytes = 1000 << 20
const lowMemoryMaxBytes = 64 << 20

var maxBytes atomic.Int64

func init() {
	maxBytes.Store(defaultMaxBytes)
}

func Configure(lowMemory bool, bodyLimitMB int) {
	if bodyLimitMB > 0 {
		maxBytes.Store(int64(bodyLimitMB) << 20)
		return
	}
	if lowMemory {
		maxBytes.Store(lowMemoryMaxBytes)
		return
	}
	maxBytes.Store(defaultMaxBytes)
}

func MaxBytesLimit() int64 {
	return maxBytes.Load()
}

type zstdReadCloser struct {
	decoder *zstd.Decoder
	orig    io.ReadCloser
}

func (z *zstdReadCloser) Read(p []byte) (int, error) {
	return z.decoder.Read(p)
}

func (z *zstdReadCloser) Close() error {
	z.decoder.Close()
	if z.orig != nil {
		return z.orig.Close()
	}
	return nil
}

func DecompressMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.Body != http.NoBody && strings.EqualFold(r.Header.Get("Content-Encoding"), "zstd") {
			decoder, err := zstd.NewReader(r.Body)
			if err != nil {
				http.Error(w, "invalid zstd body", http.StatusBadRequest)
				return
			}
			r.Body = &zstdReadCloser{decoder: decoder, orig: r.Body}
			r.Header.Del("Content-Encoding")
			r.ContentLength = -1
		}
		next.ServeHTTP(w, r)
	})
}

func LimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.Body != http.NoBody {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes.Load())
		}
		next.ServeHTTP(w, r)
	})
}
