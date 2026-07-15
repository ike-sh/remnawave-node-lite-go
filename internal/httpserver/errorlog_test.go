package httpserver

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestBoundedErrorLogWriterLimitsBurstAndReportsSuppression(t *testing.T) {
	var output bytes.Buffer
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	writer := &boundedErrorLogWriter{
		dst:      &output,
		interval: 30 * time.Second,
		burst:    2,
		now:      func() time.Time { return now },
	}

	for i := 1; i <= 4; i++ {
		message := []byte(fmt.Sprintf("error-%d\n", i))
		if n, err := writer.Write(message); err != nil || n != len(message) {
			t.Fatalf("Write(%d) = (%d, %v), want (%d, nil)", i, n, err, len(message))
		}
	}
	if got := output.String(); got != "error-1\nerror-2\n" {
		t.Fatalf("limited output = %q", got)
	}

	now = now.Add(30 * time.Second)
	if _, err := writer.Write([]byte("next-window\n")); err != nil {
		t.Fatalf("write next window: %v", err)
	}
	got := output.String()
	if !strings.Contains(got, "suppressed 2 server errors in the previous 30s") {
		t.Fatalf("suppression summary missing from %q", got)
	}
	if !strings.HasSuffix(got, "next-window\n") {
		t.Fatalf("next-window message missing from %q", got)
	}
}
