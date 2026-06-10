package xray

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRotateLogIfNeededSkipsSmallFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "xray.out.log")
	if err := os.WriteFile(path, []byte("small"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	rotateLogIfNeeded(path, 1024)

	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Fatalf("expected no backup for small file, stat err=%v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "small" {
		t.Fatalf("original file must be untouched, got %q err=%v", data, err)
	}
}

func TestRotateLogIfNeededRotatesAndTruncates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "xray.err.log")
	payload := bytes.Repeat([]byte("x"), 2048)
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	rotateLogIfNeeded(path, 1024)

	backup, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if !bytes.Equal(backup, payload) {
		t.Fatalf("backup must contain previous content (%d bytes), got %d bytes", len(payload), len(backup))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat original: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("original must be truncated to 0, got %d", info.Size())
	}
}

func TestRotateLogIfNeededReplacesOldBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "xray.out.log")
	if err := os.WriteFile(path+".1", []byte("old backup"), 0o644); err != nil {
		t.Fatalf("write old backup: %v", err)
	}
	payload := bytes.Repeat([]byte("y"), 2048)
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	rotateLogIfNeeded(path, 1024)

	backup, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if !bytes.Equal(backup, payload) {
		t.Fatalf("backup must be replaced with new content, got %q", backup[:16])
	}
}
