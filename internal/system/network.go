package system

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	routeFlagUp     = 0x1
	routeFlagReject = 0x200
)

type interfaceSample struct {
	rxBytes   uint64
	txBytes   uint64
	timestamp time.Time
}

// NetworkMonitor polls /proc/net/dev on Linux for default interface rates.
type NetworkMonitor struct {
	mu           sync.RWMutex
	available    bool
	defaultIface string
	previous     map[string]interfaceSample
	current      *NetworkInterface
	pollInterval time.Duration
	stop         chan struct{}
	stopOnce     sync.Once
}

var defaultMonitor = NewNetworkMonitor()

func DefaultNetworkMonitor() *NetworkMonitor {
	return defaultMonitor
}

func NewNetworkMonitor() *NetworkMonitor {
	m := &NetworkMonitor{
		// 3s keeps Panel stats fresh enough while cutting idle wakeups to 1/3.
		pollInterval: 3 * time.Second,
		stop:         make(chan struct{}),
	}
	m.available = fileExists("/proc/net/dev")
	if m.available {
		m.defaultIface = resolveDefaultInterface()
		m.previous = readProcNetDev()
		stampInterfaceSamples(m.previous, time.Now())
		go m.loop()
	}
	return m
}

func (m *NetworkMonitor) Stop() {
	m.stopOnce.Do(func() {
		if m.stop != nil {
			close(m.stop)
		}
	})
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
	m.updateSamplesForInterface(readProcNetDev(), time.Now(), resolveDefaultInterface())
}

func (m *NetworkMonitor) updateSamples(current map[string]interfaceSample, now time.Time) {
	m.mu.RLock()
	defaultIface := m.defaultIface
	m.mu.RUnlock()
	m.updateSamplesForInterface(current, now, defaultIface)
}

func (m *NetworkMonitor) updateSamplesForInterface(current map[string]interfaceSample, now time.Time, defaultIface string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.defaultIface = defaultIface
	m.current = nil
	if defaultIface != "" {
		if cur, ok := current[defaultIface]; ok {
			if prev, ok := m.previous[defaultIface]; ok && !prev.timestamp.IsZero() {
				elapsed := now.Sub(prev.timestamp).Seconds()
				// uint64 counter wrap / interface reset makes cur < prev; the
				// unsigned subtraction would yield an absurdly large rate, so
				// skip that sample instead.
				if elapsed > 0 && cur.rxBytes >= prev.rxBytes && cur.txBytes >= prev.txBytes {
					rxRate := float64(cur.rxBytes-prev.rxBytes) / elapsed
					txRate := float64(cur.txBytes-prev.txBytes) / elapsed
					m.current = &NetworkInterface{
						Interface:     defaultIface,
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
		current[name] = sample
	}
	m.previous = current
}

func stampInterfaceSamples(samples map[string]interfaceSample, now time.Time) {
	for name, sample := range samples {
		sample.timestamp = now
		samples[name] = sample
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
		result[iface] = interfaceSample{rxBytes: rx, txBytes: tx}
	}
	return result
}

func resolveDefaultInterface() string {
	raw, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return ""
	}
	return parseDefaultInterface(raw)
}

func parseDefaultInterface(raw []byte) string {
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	var columns map[string]int
	bestInterface := ""
	var bestMetric uint64
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 {
			continue
		}
		if columns == nil {
			columns = make(map[string]int, len(fields))
			for index, name := range fields {
				columns[name] = index
			}
			if _, ok := columns["Iface"]; !ok {
				return ""
			}
			continue
		}

		ifaceIndex, ifaceOK := columns["Iface"]
		destinationIndex, destinationOK := columns["Destination"]
		flagsIndex, flagsOK := columns["Flags"]
		metricIndex, metricOK := columns["Metric"]
		maskIndex, maskOK := columns["Mask"]
		if !ifaceOK || !destinationOK || !flagsOK || !metricOK || !maskOK {
			return ""
		}
		maxIndex := max(ifaceIndex, destinationIndex, flagsIndex, metricIndex, maskIndex)
		if maxIndex >= len(fields) {
			continue
		}
		iface := fields[ifaceIndex]
		// Linux emits blackhole and other device-less routes with "*" as
		// the interface while still marking them RTF_UP.
		if iface == "" || iface == "*" {
			continue
		}
		destination, err := strconv.ParseUint(fields[destinationIndex], 16, 32)
		if err != nil || destination != 0 {
			continue
		}
		mask, err := strconv.ParseUint(fields[maskIndex], 16, 32)
		if err != nil || mask != 0 {
			continue
		}
		flags, err := strconv.ParseUint(fields[flagsIndex], 16, 32)
		if err != nil || flags&routeFlagUp == 0 || flags&routeFlagReject != 0 {
			continue
		}
		metric, err := strconv.ParseUint(fields[metricIndex], 10, 64)
		if err != nil {
			continue
		}
		if bestInterface == "" || metric < bestMetric {
			bestInterface = iface
			bestMetric = metric
		}
	}
	return bestInterface
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
