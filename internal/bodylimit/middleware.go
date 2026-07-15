package bodylimit

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/klauspost/compress/zstd"
)

const (
	defaultMaxBytes          = 256 << 20
	lowMemoryMaxBytes        = 16 << 20
	maxCompressedZstdBytes   = 64 << 20
	maxZstdWindowBytes       = 32 << 20
	maxConcurrentZstdDecodes = 2
	zstdSlotWait             = 5 * time.Second
)

var maxBytes atomic.Int64
var zstdDecoderSlots = make(chan struct{}, maxConcurrentZstdDecodes)
var errDecompressedBodyTooLarge = errors.New("http: decompressed request body too large")

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
	release func()

	closeOnce sync.Once
	closeErr  error
}

func (z *zstdReadCloser) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if z.limit >= 0 {
		remaining := z.limit - z.read
		if remaining == 0 {
			var extra [1]byte
			for {
				n, err := z.decoder.Read(extra[:])
				if n != 0 {
					return 0, errDecompressedBodyTooLarge
				}
				if err != nil {
					return 0, err
				}
			}
		}
		if int64(len(p)) > remaining {
			p = p[:remaining]
		}
	}
	n, err := z.decoder.Read(p)
	z.read += int64(n)
	return n, err
}

func (z *zstdReadCloser) Close() error {
	z.closeOnce.Do(func() {
		z.decoder.Close()
		if z.orig != nil {
			z.closeErr = z.orig.Close()
		}
		if z.release != nil {
			z.release()
		}
	})
	return z.closeErr
}

func acquireZstdDecoder(ctx context.Context) (func(), bool) {
	timer := time.NewTimer(zstdSlotWait)
	defer timer.Stop()
	select {
	case zstdDecoderSlots <- struct{}{}:
		var once sync.Once
		return func() {
			once.Do(func() { <-zstdDecoderSlots })
		}, true
	case <-ctx.Done():
		return nil, false
	case <-timer.C:
		return nil, false
	}
}

func newZstdDecoder(reader io.Reader, bodyLimit int64) (*zstd.Decoder, error) {
	windowLimit := bodyLimit
	if windowLimit < zstd.MinWindowSize {
		windowLimit = zstd.MinWindowSize
	}
	if windowLimit > maxZstdWindowBytes {
		windowLimit = maxZstdWindowBytes
	}
	return zstd.NewReader(
		reader,
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderLowmem(true),
		zstd.WithDecoderMaxMemory(uint64(windowLimit)),
	)
}

func DecompressMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.Body != http.NoBody && strings.EqualFold(r.Header.Get("Content-Encoding"), "zstd") {
			decompressedLimit := maxBytes.Load()
			release, ok := acquireZstdDecoder(r.Context())
			if !ok {
				w.Header().Set("Retry-After", "1")
				http.Error(w, "zstd decoder capacity unavailable", http.StatusServiceUnavailable)
				return
			}
			compressedCap := decompressedLimit
			if compressedCap > maxCompressedZstdBytes {
				compressedCap = maxCompressedZstdBytes
			}
			compressedBody := http.MaxBytesReader(w, r.Body, compressedCap)
			decoder, err := newZstdDecoder(compressedBody, decompressedLimit)
			if err != nil {
				release()
				_ = compressedBody.Close()
				http.Error(w, "invalid zstd body", http.StatusBadRequest)
				return
			}
			decoded := &zstdReadCloser{
				decoder: decoder,
				orig:    compressedBody,
				limit:   decompressedLimit,
				release: release,
			}
			defer decoded.Close()
			r.Body = decoded
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
