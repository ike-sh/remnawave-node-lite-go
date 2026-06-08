//go:build !linux

package netadmin

func KillSocketsByIP(ip string) error {
	return nil
}
