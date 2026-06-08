package system

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type interfaceSample struct {
	rxBytes   uint64
	txBytes   uint64
	timestamp time.Time
}

// NetworkMonitor polls /proc/net/dev on Linux for default interface rates.
type NetworkMonitor struct {
	mu            sync.RWMutex
	available     bool
	defaultIface  string
	previous      map[string]interfaceSample
	current       *NetworkInterface
	pollInterval  time.Duration
	stop          chan struct{}
}

var defaultMonitor = NewNetworkMonitor()

func DefaultNetworkMonitor() *NetworkMonitor {
	return defaultMonitor
}

func NewNetworkMonitor() *NetworkMonitor {
	m := &NetworkMonitor{
		pollInterval: time.Second,
		stop:         make(chan struct{}),
	}
	m.available = fileExists("/proc/net/dev")
	if m.available {
		m.defaultIface = resolveDefaultInterface()
		m.previous = readProcNetDev()
		go m.loop()
	}
	return m
}

func (m *NetworkMonitor) Stop() {
	select {
	case <-m.stop:
	default:
		close(m.stop)
	}
}

func (m *NetworkMonitor) GetDefaultInterface() *NetworkInterface {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.current == nil {
		return nil
	}
	copy := *m.current
	return &copy
}

func (m *NetworkMonitor) loop() {
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			m.tick()
		}
	}
}

func (m *NetworkMonitor) tick() {
	current := readProcNetDev()
	now := time.Now()

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.defaultIface != "" {
		if cur, ok := current[m.defaultIface]; ok {
			if prev, ok := m.previous[m.defaultIface]; ok && !prev.timestamp.IsZero() {
				elapsed := now.Sub(prev.timestamp).Seconds()
				if elapsed > 0 {
					rxRate := float64(cur.rxBytes-prev.rxBytes) / elapsed
					txRate := float64(cur.txBytes-prev.txBytes) / elapsed
					if rxRate < 0 {
						rxRate = 0
					}
					if txRate < 0 {
						txRate = 0
					}
					m.current = &NetworkInterface{
						Interface:     m.defaultIface,
						RxBytesPerSec: int64(rxRate),
						TxBytesPerSec: int64(txRate),
						RxTotal:       int64(cur.rxBytes),
						TxTotal:       int64(cur.txBytes),
					}
				}
			}
		}
	}

	for name, sample := range current {
		sample.timestamp = now
		m.previous[name] = sample
	}
}

func readProcNetDev() map[string]interfaceSample {
	result := map[string]interfaceSample{}
	file, err := os.Open("/proc/net/dev")
	if err != nil {
		return result
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		if lineNo <= 2 {
			continue
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 10 {
			continue
		}
		iface := strings.TrimSuffix(parts[0], ":")
		rx, _ := strconv.ParseUint(parts[1], 10, 64)
		tx, _ := strconv.ParseUint(parts[9], 10, 64)
		result[iface] = interfaceSample{rxBytes: rx, txBytes: tx, timestamp: time.Now()}
	}
	return result
}

func resolveDefaultInterface() string {
	raw, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == "00000000" {
			return fields[0]
		}
	}
	return ""
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
