package vision

import "net"

func validateIP(ip string) bool {
	return net.ParseIP(ip) != nil
}
