package xray

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	maxLogSize       = 10 << 20
	logCheckInterval = 10 * time.Second
)

// rw-core streams use cappedLogWriter and rotate before a write crosses the
// limit. OpenRC owns its output descriptors, so those two files are checked on
// a short interval instead.
var periodicRotatedLogFiles = []string{"openrc.log", "openrc.err.log"}

// StartLogRotation bounds logs written directly by the OpenRC supervisor.
func (m *Manager) StartLogRotation(ctx context.Context) {
	m.rotateLogs()
	ticker := time.NewTicker(logCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.rotateLogs()
		}
	}
}

func (m *Manager) rotateLogs() {
	m.logRotateMu.Lock()
	defer m.logRotateMu.Unlock()
	for _, name := range periodicRotatedLogFiles {
		path := filepath.Join(m.logDir, name)
		if err := rotateLogIfNeeded(path, maxLogSize); err != nil {
			slog.Warn("failed to rotate service log", "path", path, "error", err)
		}
	}
}

// rotateLogIfNeeded archives at most the last maxSize bytes and always attempts
// to truncate the source, even when the diagnostic backup cannot be written.
func rotateLogIfNeeded(path string, maxSize int64) error {
	if maxSize <= 0 {
		return fmt.Errorf("log size limit must be positive")
	}
	backupErr := boundArchivedLog(path+".1", maxSize)
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return backupErr
	}
	if err != nil {
		return errors.Join(backupErr, err)
	}
	if info.Size() < maxSize {
		return backupErr
	}
	archiveErr := archiveLogTail(path, path+".1", maxSize, info.Mode().Perm())
	if archiveErr == nil {
		// A successful archive replaced any oversized legacy backup.
		backupErr = nil
	}
	truncateErr := os.Truncate(path, 0)
	return errors.Join(backupErr, archiveErr, truncateErr)
}

func boundArchivedLog(path string, maxSize int64) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Size() <= maxSize {
		return nil
	}
	if err := archiveLogTail(path, path, maxSize, info.Mode().Perm()); err != nil {
		// Full disks may not have room for a bounded temporary copy. Shrinking
		// in place loses the newest tail but immediately restores the hard cap.
		if truncateErr := os.Truncate(path, maxSize); truncateErr != nil {
			return errors.Join(err, truncateErr)
		}
		slog.Warn("legacy log backup truncated without preserving its tail", "path", path, "error", err)
	}
	return nil
}

func archiveLogTail(src, dst string, maxSize int64, mode os.FileMode) error {
	if maxSize <= 0 {
		return fmt.Errorf("archive size limit must be positive")
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}
	start := info.Size() - maxSize
	if start < 0 {
		start = 0
	}
	if _, err := in.Seek(start, io.SeekStart); err != nil {
		return err
	}

	// A fixed staging path is reused after crashes so interrupted rotations
	// cannot accumulate an unbounded set of random temporary files.
	tmpPath := dst + ".tmp"
	_ = os.Remove(tmpPath)
	tmp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()

	if _, err := io.Copy(tmp, io.LimitReader(in, maxSize)); err != nil {
		return err
	}
	if mode == 0 {
		mode = 0o600
	}
	if err := tmp.Chmod(mode); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		return err
	}
	return nil
}

type cappedLogWriter struct {
	mu      sync.Mutex
	path    string
	file    *os.File
	size    int64
	maxSize int64
}

func openCappedLogWriter(path string, maxSize int64) (*cappedLogWriter, error) {
	if maxSize <= 0 {
		return nil, fmt.Errorf("log size limit must be positive")
	}
	if err := rotateLogIfNeeded(path, maxSize); err != nil {
		slog.Warn("failed to archive oversized rw-core log; source was truncated when possible", "path", path, "error", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if info.Size() > maxSize {
		file.Close()
		return nil, fmt.Errorf("log %s remains above limit: %d > %d", path, info.Size(), maxSize)
	}
	if err := verifyLogBound(path+".1", maxSize); err != nil {
		file.Close()
		return nil, err
	}
	return &cappedLogWriter{path: path, file: file, size: info.Size(), maxSize: maxSize}, nil
}

func verifyLogBound(path string, maxSize int64) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Size() > maxSize {
		return fmt.Errorf("log backup %s remains above limit: %d > %d", path, info.Size(), maxSize)
	}
	return nil
}

func (w *cappedLogWriter) Write(payload []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return 0, os.ErrClosed
	}
	originalLen := len(payload)
	if w.maxSize > 0 && w.size+int64(len(payload)) > w.maxSize {
		if err := w.rotateLocked(); err != nil {
			return 0, err
		}
	}
	discarded := 0
	if w.maxSize > 0 && int64(len(payload)) > w.maxSize {
		discarded = len(payload) - int(w.maxSize)
		payload = payload[discarded:]
	}
	n, err := w.file.Write(payload)
	w.size += int64(n)
	if err != nil {
		return discarded + n, err
	}
	if n != len(payload) {
		return discarded + n, io.ErrShortWrite
	}
	return originalLen, nil
}

func (w *cappedLogWriter) rotateLocked() error {
	info, err := w.file.Stat()
	if err != nil {
		return err
	}
	archiveErr := archiveLogTail(w.path, w.path+".1", w.maxSize, info.Mode().Perm())
	if err := w.file.Truncate(0); err != nil {
		return errors.Join(archiveErr, err)
	}
	if _, err := w.file.Seek(0, io.SeekEnd); err != nil {
		return errors.Join(archiveErr, err)
	}
	w.size = 0
	if archiveErr != nil {
		slog.Warn("failed to archive rw-core log before truncation", "path", w.path, "error", archiveErr)
	}
	return nil
}

func (w *cappedLogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}
