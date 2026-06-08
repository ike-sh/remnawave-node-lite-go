//go:build linux

package netadmin

import (
	"os"
	"strconv"
	"strings"
)

const capNetAdminBit = 12

func HasCapNetAdmin() bool {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "CapEff:") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "CapEff:"))
		caps, err := strconv.ParseUint(value, 16, 64)
		if err != nil {
			return false
		}
		return caps&(1<<capNetAdminBit) != 0
	}
	return false
}
