package xray

import (
	"bytes"
	"context"
	"errors"
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

	if err := rotateLogIfNeeded(path, 1024); err != nil {
		t.Fatalf("rotate: %v", err)
	}

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

	if err := rotateLogIfNeeded(path, 1024); err != nil {
		t.Fatalf("rotate: %v", err)
	}

	backup, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if !bytes.Equal(backup, payload[len(payload)-1024:]) {
		t.Fatalf("backup must contain the bounded tail (%d bytes), got %d bytes", 1024, len(backup))
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

	if err := rotateLogIfNeeded(path, 1024); err != nil {
		t.Fatalf("rotate: %v", err)
	}

	backup, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if !bytes.Equal(backup, payload[len(payload)-1024:]) {
		t.Fatalf("backup must be replaced with new content, got %q", backup[:16])
	}
}

func TestRotateLogIfNeededBoundsLegacyBackupWithoutCurrentLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "xray.out.log")
	backup := bytes.Repeat([]byte("legacy"), 512)
	if err := os.WriteFile(path+".1", backup, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := rotateLogIfNeeded(path, 1024); err != nil {
		t.Fatalf("bound legacy backup: %v", err)
	}
	got, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, backup[len(backup)-1024:]) {
		t.Fatalf("bounded backup = %d bytes, want trailing 1024", len(got))
	}
}

func TestLegacyBackupFallsBackToInPlaceTruncateWhenArchiveCannotStage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "openrc.log")
	backupPath := path + ".1"
	if err := os.WriteFile(backupPath, bytes.Repeat([]byte("z"), 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	// A non-empty directory at the fixed staging path makes remove/open fail
	// without relying on filesystem permissions or available disk capacity.
	if err := os.Mkdir(backupPath+".tmp", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupPath+".tmp", "keep"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := rotateLogIfNeeded(path, 1024); err != nil {
		t.Fatalf("bound legacy backup: %v", err)
	}
	info, err := os.Stat(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 1024 {
		t.Fatalf("fallback backup size = %d, want 1024", info.Size())
	}
}

func TestVerifyLogBoundRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "xray.out.log.1")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 2048), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyLogBound(path, 1024); err == nil {
		t.Fatal("oversized backup passed hard-bound verification")
	}
}

func TestArchiveLogTailReusesAndRemovesFixedTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "openrc.log")
	dst := src + ".1"
	if err := os.WriteFile(src, bytes.Repeat([]byte("x"), 2048), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst+".tmp", []byte("interrupted rotation"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := archiveLogTail(src, dst, 1024, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dst + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rotation temporary file remains: %v", err)
	}
}

func TestStartLogRotationChecksExistingOpenRCLogsImmediately(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "openrc.log")
	payload := bytes.Repeat([]byte("z"), maxLogSize)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("write oversized log: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	manager := &Manager{logDir: dir}
	manager.StartLogRotation(ctx)

	backup, err := os.Stat(path + ".1")
	if err != nil {
		t.Fatalf("stat immediate backup: %v", err)
	}
	if backup.Size() != int64(len(payload)) {
		t.Fatalf("backup size = %d, want %d", backup.Size(), len(payload))
	}
	current, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat current log: %v", err)
	}
	if current.Size() != 0 {
		t.Fatalf("current log size = %d, want 0", current.Size())
	}
}

func TestCappedLogWriterRotatesBeforeCrossingLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "xray.out.log")
	writer, err := openCappedLogWriter(path, 1024)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	t.Cleanup(func() { _ = writer.Close() })

	first := bytes.Repeat([]byte("a"), 700)
	second := bytes.Repeat([]byte("b"), 700)
	if _, err := writer.Write(first); err != nil {
		t.Fatalf("write first: %v", err)
	}
	if _, err := writer.Write(second); err != nil {
		t.Fatalf("write second: %v", err)
	}

	backup, err := os.ReadFile(path + ".1")
	if err != nil || !bytes.Equal(backup, first) {
		t.Fatalf("backup = %d bytes, err=%v", len(backup), err)
	}
	current, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(current, second) {
		t.Fatalf("current = %d bytes, err=%v", len(current), err)
	}
}

func TestCappedLogWriterBoundsSingleLargeWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "xray.err.log")
	writer, err := openCappedLogWriter(path, 1024)
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("z"), 4096)
	if n, err := writer.Write(payload); err != nil || n != len(payload) {
		t.Fatalf("Write = (%d, %v), want (%d, nil)", n, err, len(payload))
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile(path)
	if err != nil || len(current) != 1024 {
		t.Fatalf("bounded current = %d bytes, err=%v", len(current), err)
	}
}
