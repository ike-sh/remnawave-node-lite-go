//go:build !linux

package netadmin

func HasCapNetAdmin() bool {
	return false
}
