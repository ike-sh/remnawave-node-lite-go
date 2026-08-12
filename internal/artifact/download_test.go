package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDownloadVerifiesDigestAndActivatesAtomically(t *testing.T) {
	payload := []byte("verified core")
	digest := sha256.Sum256(payload)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	destination := filepath.Join(t.TempDir(), "artifact")
	result, err := Download(context.Background(), server.URL, destination, Options{
		Client:         server.Client(),
		ExpectedSHA256: hex.EncodeToString(digest[:]),
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if result.Size != int64(len(payload)) || result.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("unexpected result: %#v", result)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("artifact content = %q", got)
	}
}

func TestDownloadHashMismatchPreservesDestination(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("untrusted"))
	}))
	defer server.Close()

	destination := filepath.Join(t.TempDir(), "artifact")
	if err := os.WriteFile(destination, []byte("current"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := Download(context.Background(), server.URL, destination, Options{
		Client:         server.Client(),
		ExpectedSHA256: strings.Repeat("0", 64),
	})
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("expected digest error, got %v", err)
	}
	got, _ := os.ReadFile(destination)
	if string(got) != "current" {
		t.Fatalf("destination changed after failed verification: %q", got)
	}
	if _, statErr := os.Stat(destination + ".download"); !os.IsNotExist(statErr) {
		t.Fatalf("temporary download was not removed: %v", statErr)
	}
}

func TestDownloadRejectsHTTPAndOversizeBodies(t *testing.T) {
	if _, err := Download(context.Background(), "http://example.com/core", filepath.Join(t.TempDir(), "core"), Options{}); err == nil {
		t.Fatal("expected HTTP URL to be rejected")
	}

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("too large"))
	}))
	defer server.Close()
	_, err := Download(context.Background(), server.URL, filepath.Join(t.TempDir(), "core"), Options{
		Client:  server.Client(),
		MaxSize: 3,
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected size error, got %v", err)
	}
}

func TestDownloadIdleTimeoutRemovesTemporaryFile(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(150 * time.Millisecond)
		_, _ = w.Write([]byte("late"))
	}))
	defer server.Close()

	destination := filepath.Join(t.TempDir(), "core")
	_, err := Download(context.Background(), server.URL, destination, Options{
		Client:      server.Client(),
		IdleTimeout: 25 * time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "no artifact data") {
		t.Fatalf("expected idle timeout, got %v", err)
	}
	if _, statErr := os.Stat(destination + ".download"); !os.IsNotExist(statErr) {
		t.Fatalf("temporary download was not removed: %v", statErr)
	}
}
