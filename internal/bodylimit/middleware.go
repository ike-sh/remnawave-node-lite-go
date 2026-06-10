package bodylimit

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/klauspost/compress/zstd"
)

const defaultMaxBytes = 1000 << 20
const lowMemoryMaxBytes = 64 << 20
const maxCompressedZstdBytes = 64 << 20

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
	limit   int64
	read    int64
}

func (z *zstdReadCloser) Read(p []byte) (int, error) {
	if z.limit > 0 {
		remaining := z.limit - z.read
		if remaining <= 0 {
			return 0, fmt.Errorf("http: decompressed request body too large")
		}
		if int64(len(p)) > remaining {
			p = p[:remaining]
		}
	}
	n, err := z.decoder.Read(p)
	z.read += int64(n)
	if z.limit > 0 && z.read > z.limit {
		return n, fmt.Errorf("http: decompressed request body too large")
	}
	return n, err
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
			decompressedLimit := maxBytes.Load()
			compressedCap := decompressedLimit
			if compressedCap > maxCompressedZstdBytes {
				compressedCap = maxCompressedZstdBytes
			}
			compressedBody := http.MaxBytesReader(w, r.Body, compressedCap)
			decoder, err := zstd.NewReader(compressedBody)
			if err != nil {
				http.Error(w, "invalid zstd body", http.StatusBadRequest)
				return
			}
			r.Body = &zstdReadCloser{
				decoder: decoder,
				orig:    r.Body,
				limit:   decompressedLimit,
			}
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
