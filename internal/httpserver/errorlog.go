package httpserver

import (
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"
)

const (
	httpErrorLogInterval = 30 * time.Second
	httpErrorLogBurst    = 20
)

type boundedErrorLogWriter struct {
	mu sync.Mutex

	dst         io.Writer
	interval    time.Duration
	burst       int
	now         func() time.Time
	windowStart time.Time
	emitted     int
	suppressed  int
}

func newHTTPErrorLogger() *log.Logger {
	writer := &boundedErrorLogWriter{
		dst:      os.Stderr,
		interval: httpErrorLogInterval,
		burst:    httpErrorLogBurst,
		now:      time.Now,
	}
	return log.New(writer, "", log.LstdFlags)
}

func (w *boundedErrorLogWriter) Write(message []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	now := w.now()
	if w.windowStart.IsZero() || now.Sub(w.windowStart) >= w.interval {
		if w.suppressed != 0 {
			summary := fmt.Sprintf(
				"%s http: suppressed %d server errors in the previous %s\n",
				now.Format("2006/01/02 15:04:05"),
				w.suppressed,
				w.interval,
			)
			if _, err := io.WriteString(w.dst, summary); err != nil {
				return 0, err
			}
		}
		w.windowStart = now
		w.emitted = 0
		w.suppressed = 0
	}

	if w.emitted >= w.burst {
		w.suppressed++
		return len(message), nil
	}
	w.emitted++
	n, err := w.dst.Write(message)
	if err == nil && n != len(message) {
		err = io.ErrShortWrite
	}
	return n, err
}
