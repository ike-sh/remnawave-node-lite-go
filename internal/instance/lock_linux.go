//go:build linux

package instance

import (
	"errors"
	"net"
	"sync"
	"syscall"
)

const lockSocket = "\x00rwnode-lock"

// Lock holds the process-wide abstract Unix socket used by the official node.
type Lock struct {
	listener net.Listener
	once     sync.Once
}

// Acquire returns acquired=false only when another node already owns the lock.
// Other listen errors are reported to the caller, which may continue just like
// the upstream best-effort guard.
func Acquire() (*Lock, bool, error) {
	listener, err := net.Listen("unix", lockSocket)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return nil, false, nil
		}
		return nil, true, err
	}
	lock := &Lock{listener: listener}
	go lock.rejectConnections()
	return lock, true, nil
}

func (l *Lock) rejectConnections() {
	for {
		connection, err := l.listener.Accept()
		if err != nil {
			return
		}
		_ = connection.Close()
	}
}

func (l *Lock) Close() error {
	var err error
	l.once.Do(func() { err = l.listener.Close() })
	return err
}
