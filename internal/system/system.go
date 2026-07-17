package system

import (
	"bufio"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

var processStartedAt = time.Now()

type kernelIdentity struct {
	Release string
	Type    string
	Version string
}

var currentKernelIdentity = sync.OnceValue(readKernelIdentity)

type NetworkInterface struct {
	Interface     string `json:"interface"`
	RxBytesPerSec int64  `json:"rxBytesPerSec"`
	TxBytesPerSec int64  `json:"txBytesPerSec"`
	RxTotal       int64  `json:"rxTotal"`
	TxTotal       int64  `json:"txTotal"`
}

type Info struct {
	Arch              string   `json:"arch"`
	CPUs              int      `json:"cpus"`
	CPUModel          string   `json:"cpuModel"`
	MemoryTotal       uint64   `json:"memoryTotal"`
	Hostname          string   `json:"hostname"`
	Platform          string   `json:"platform"`
	Release           string   `json:"release"`
	Type              string   `json:"type"`
	Version           string   `json:"version"`
	NetworkInterfaces []string `json:"networkInterfaces"`
}

type Stats struct {
	MemoryFree uint64            `json:"memoryFree"`
	MemoryUsed uint64            `json:"memoryUsed"`
	Uptime     float64           `json:"uptime"`
	LoadAvg    []float64         `json:"loadAvg"`
	Interface  *NetworkInterface `json:"interface"`
}

type Snapshot struct {
	Info  Info  `json:"info"`
	Stats Stats `json:"stats"`
}

func GetInfo() Info {
	hostname, _ := os.Hostname()
	kernel := currentKernelIdentity()
	return Info{
		Arch:              nodeArchitecture(runtime.GOARCH),
		CPUs:              runtime.NumCPU(),
		CPUModel:          cpuModel(),
		MemoryTotal:       memoryTotal(),
		Hostname:          hostname,
		Platform:          nodePlatform(runtime.GOOS),
		Release:           kernel.Release,
		Type:              kernel.Type,
		Version:           kernel.Version,
		NetworkInterfaces: networkInterfaces(),
	}
}

func nodeArchitecture(goarch string) string {
	switch goarch {
	case "386":
		return "ia32"
	case "amd64":
		return "x64"
	case "mipsle":
		return "mipsel"
	case "ppc64le":
		return "ppc64"
	default:
		return goarch
	}
}

func nodePlatform(goos string) string {
	switch goos {
	case "windows":
		return "win32"
	case "illumos", "solaris":
		return "sunos"
	default:
		return goos
	}
}

func readKernelIdentity() kernelIdentity {
	var name unix.Utsname
	if err := unix.Uname(&name); err == nil {
		return kernelIdentity{
			Release: unix.ByteSliceToString(name.Release[:]),
			Type:    unix.ByteSliceToString(name.Sysname[:]),
			Version: unix.ByteSliceToString(name.Version[:]),
		}
	}
	return kernelIdentity{Type: nodeSystemType(runtime.GOOS)}
}

func nodeSystemType(goos string) string {
	switch goos {
	case "aix":
		return "AIX"
	case "darwin":
		return "Darwin"
	case "freebsd":
		return "FreeBSD"
	case "linux", "android":
		return "Linux"
	case "openbsd":
		return "OpenBSD"
	case "illumos", "solaris":
		return "SunOS"
	case "windows":
		return "Windows_NT"
	default:
		return goos
	}
}

func GetStats() Stats {
	free, total := memoryFreeAndTotal()
	used := uint64(0)
	if total > free {
		used = total - free
	}
	return Stats{
		MemoryFree: free,
		MemoryUsed: used,
		Uptime:     uptime(),
		LoadAvg:    loadAvg(),
		Interface:  defaultMonitor.GetDefaultInterface(),
	}
}

func GetSnapshot() Snapshot {
	return Snapshot{
		Info:  GetInfo(),
		Stats: GetStats(),
	}
}

func networkInterfaces() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return []string{}
	}
	names := make([]string, 0, len(ifaces))
	for _, iface := range ifaces {
		names = append(names, iface.Name)
	}
	return names
}

func memoryTotal() uint64 {
	_, total := memoryFreeAndTotal()
	return total
}

func memoryFreeAndTotal() (uint64, uint64) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		return 0, mem.Sys
	}
	defer file.Close()

	var free, available, total uint64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		value *= 1024
		switch fields[0] {
		case "MemTotal:":
			total = value
		case "MemFree:":
			free = value
		case "MemAvailable:":
			available = value
		}
	}
	if available > 0 {
		free = available
	}
	return free, total
}

func loadAvg() []float64 {
	raw, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return []float64{0, 0, 0}
	}
	fields := strings.Fields(string(raw))
	values := []float64{0, 0, 0}
	for i := 0; i < len(values) && i < len(fields); i++ {
		if parsed, err := strconv.ParseFloat(fields[i], 64); err == nil {
			values[i] = parsed
		}
	}
	return values
}

func uptime() float64 {
	raw, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return time.Since(processStartedAt).Seconds()
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return time.Since(processStartedAt).Seconds()
	}
	parsed, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return time.Since(processStartedAt).Seconds()
	}
	return parsed
}

func cpuModel() string {
	file, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return "unknown"
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "model name") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return "unknown"
}
