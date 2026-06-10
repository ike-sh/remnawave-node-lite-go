package xray

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	// maxLogSize triggers rotation per log file; one ".1" backup is kept,
	// so worst-case disk usage is ~2x maxLogSize per file.
	maxLogSize       = 10 << 20
	logCheckInterval = 10 * time.Minute
)

var rotatedLogFiles = []string{"xray.out.log", "xray.err.log"}

// StartLogRotation periodically rotates rw-core log files so long-running
// nodes never fill small VPS disks. Rotation is copy+truncate: the O_APPEND
// descriptor held by the running rw-core process stays valid because every
// append write seeks to the (new) end of file.
func (m *Manager) StartLogRotation(ctx context.Context) {
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
	for _, name := range rotatedLogFiles {
		rotateLogIfNeeded(filepath.Join(m.logDir, name), maxLogSize)
	}
}

// rotateLogIfNeeded copies path to path+".1" (replacing the previous backup)
// and truncates path in place once it reaches maxSize. Lines written between
// copy and truncate may be lost, which is acceptable for diagnostic logs.
func rotateLogIfNeeded(path string, maxSize int64) {
	info, err := os.Stat(path)
	if err != nil || info.Size() < maxSize {
		return
	}
	if err := copyFile(path, path+".1"); err != nil {
		return
	}
	_ = os.Truncate(path, 0)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
