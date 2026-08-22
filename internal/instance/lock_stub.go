//go:build !linux

package instance

// Lock is a no-op on platforms without Linux abstract Unix sockets.
type Lock struct{}

func Acquire() (*Lock, bool, error) { return &Lock{}, true, nil }
func (l *Lock) Close() error        { return nil }
