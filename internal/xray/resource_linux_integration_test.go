//go:build linux

package xray

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/unixconfig"
)

const lowMemoryIntegrationEnv = "REMNANODE_LOW_MEMORY_INTEGRATION"

func TestLowMemoryRealCore(t *testing.T) {
	if os.Getenv(lowMemoryIntegrationEnv) != "1" {
		t.Skip("set " + lowMemoryIntegrationEnv + "=1 to run the real-core resource gate")
	}
	core := os.Getenv("RW_CORE_BIN")
	if core == "" {
		t.Fatal("RW_CORE_BIN is required")
	}
	if _, err := os.Stat(core); err != nil {
		t.Fatalf("stat rw-core: %v", err)
	}

	users := resourceUserCount(t)
	previousLimit := debug.SetMemoryLimit(180 << 20)
	defer debug.SetMemoryLimit(previousLimit)
	previousProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previousProcs)

	root := t.TempDir()
	manager, err := NewManager(Options{
		XrayBin:            core,
		GeoDir:             root,
		LogDir:             filepath.Join(root, "logs"),
		InternalSocketPath: filepath.Join(root, "internal.sock"),
		InternalRESTToken:  "resource-test-token",
		LowMemory:          true,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	server := &unixconfig.Server{
		Path:     filepath.Join(root, "internal.sock"),
		Token:    "resource-test-token",
		Provider: manager,
	}
	serverErr := make(chan error, 1)
	go func() { serverErr <- server.ListenAndServe(ctx) }()
	waitForResourceSocket(t, ctx, server.Path, serverErr)
	t.Cleanup(func() {
		_ = manager.Stop()
		cancel()
		select {
		case err := <-serverErr:
			if err != nil {
				t.Logf("internal server shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Log("internal server did not stop within 5s")
		}
	})

	resourceMemorySample(t, "idle")

	small := resourceStartRequest(1_000, false)
	if response := manager.Start(ctx, small); !response.IsStarted {
		t.Fatalf("start 1k-user core: %+v", response)
	}
	small = StartRequest{}
	assertResourceCoreRPCs(t, ctx, manager, 1_000)
	resourceMemorySample(t, "start-1k")

	unchanged := resourceStartRequest(1_000, false)
	if response := manager.Start(ctx, unchanged); !response.IsStarted {
		t.Fatalf("unchanged 1k-user sync: %+v", response)
	}
	unchanged = StartRequest{}
	resourceMemorySample(t, "sync-unchanged")

	large := resourceStartRequest(users, true)
	if response := manager.Start(ctx, large); !response.IsStarted {
		t.Fatalf("start %d-user core: %+v", users, response)
	}
	large = StartRequest{}
	assertResourceCoreRPCs(t, ctx, manager, users)
	resourceMemorySample(t, fmt.Sprintf("start-%dk", users/1_000))

	hotID := "ffffffff-ffff-4fff-8fff-ffffffffffff"
	added := manager.HandlerAddVlessUser(ctx, "resource-in", "hot-user", hotID, "", 0)
	if !added.OK || !manager.CommitUserAdded(added, "resource-in", hotID) {
		t.Fatalf("hot add failed: %+v", added)
	}
	if count, result := manager.HandlerGetInboundUsersCount(ctx, "resource-in"); !result.OK || count != int64(users+1) {
		t.Fatalf("count after hot add = %d result=%+v", count, result)
	}
	removed := manager.HandlerRemoveUser(ctx, "resource-in", "hot-user")
	if !removed.OK || !manager.CommitUserRemoved(removed, "resource-in", hotID) {
		t.Fatalf("hot remove failed: %+v", removed)
	}
	if _, err := manager.GetUsersIPList(ctx); err != nil {
		t.Fatalf("get users IP list: %v", err)
	}
	resourceMemorySample(t, "hot-update-and-stats")

	peak := resourceMemoryValue(t, "/sys/fs/cgroup/memory.peak", "/sys/fs/cgroup/memory/memory.max_usage_in_bytes")
	if limit := resourcePeakLimit(t); limit != 0 && peak > limit {
		t.Fatalf("cgroup peak %.1f MiB exceeds configured limit %.1f MiB", mib(peak), mib(limit))
	}
}

func resourceStartRequest(users int, force bool) StartRequest {
	clients := make([]any, 0, users)
	set := NewHashedSet()
	for index := range users {
		id := fmt.Sprintf("00000000-0000-4000-8000-%012x", index)
		set.Add(id)
		clients = append(clients, map[string]any{
			"email": fmt.Sprintf("resource-user-%d", index),
			"id":    id,
		})
	}
	return StartRequest{
		Internals: StartInternals{
			ForceRestart: force,
			Hashes: ConfigHash{
				EmptyConfig: "resource-base-v1",
				Inbounds: []InboundHash{{
					UsersCount: float64(users),
					Hash:       set.Hash64String(),
					Tag:        "resource-in",
				}},
			},
		},
		XrayConfig: map[string]any{
			"log": map[string]any{"loglevel": "warning"},
			"inbounds": []any{map[string]any{
				"tag":      "resource-in",
				"listen":   "127.0.0.1",
				"port":     float64(18_080),
				"protocol": "vless",
				"settings": map[string]any{
					"clients":    clients,
					"decryption": "none",
				},
			}},
			"outbounds": []any{map[string]any{
				"tag": "direct", "protocol": "freedom",
			}},
			"routing": map[string]any{"rules": []any{}},
		},
	}
}

func assertResourceCoreRPCs(t *testing.T, ctx context.Context, manager *Manager, users int) {
	t.Helper()
	if _, err := manager.GetSysStats(ctx); err != nil {
		t.Fatalf("get system stats: %v", err)
	}
	count, result := manager.HandlerGetInboundUsersCount(ctx, "resource-in")
	if !result.OK || count != int64(users) {
		t.Fatalf("inbound count = %d, want %d, result=%+v", count, users, result)
	}
}

func waitForResourceSocket(t *testing.T, ctx context.Context, path string, serverErr <-chan error) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case err := <-serverErr:
			t.Fatalf("internal server stopped before socket became ready: %v", err)
		case <-ctx.Done():
			t.Fatalf("wait for internal socket: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

func resourceMemorySample(t *testing.T, phase string) {
	t.Helper()
	debug.FreeOSMemory()
	time.Sleep(100 * time.Millisecond)
	current := resourceMemoryValue(t, "/sys/fs/cgroup/memory.current", "/sys/fs/cgroup/memory/memory.usage_in_bytes")
	peak := resourceMemoryValue(t, "/sys/fs/cgroup/memory.peak", "/sys/fs/cgroup/memory/memory.max_usage_in_bytes")
	rss := resourceProcessRSS(t)
	t.Logf("RESOURCE phase=%s cgroup_current_mib=%.1f cgroup_peak_mib=%.1f node_test_rss_mib=%.1f", phase, mib(current), mib(peak), mib(rss))
}

func resourceMemoryValue(t *testing.T, paths ...string) uint64 {
	t.Helper()
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		value, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
		if err == nil {
			return value
		}
	}
	t.Fatalf("cgroup memory metric unavailable: %v", paths)
	return 0
}

func resourceProcessRSS(t *testing.T) uint64 {
	t.Helper()
	raw, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Fatalf("read /proc/self/status: %v", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "VmRSS:" {
			value, err := strconv.ParseUint(fields[1], 10, 64)
			if err == nil {
				return value << 10
			}
		}
	}
	t.Fatal("VmRSS missing from /proc/self/status")
	return 0
}

func resourceUserCount(t *testing.T) int {
	t.Helper()
	raw := os.Getenv("REMNANODE_RESOURCE_USERS")
	if raw == "" {
		return 50_000
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		t.Fatalf("invalid REMNANODE_RESOURCE_USERS %q", raw)
	}
	return value
}

func resourcePeakLimit(t *testing.T) uint64 {
	t.Helper()
	raw := os.Getenv("REMNANODE_RESOURCE_MAX_PEAK_MIB")
	if raw == "" {
		return 0
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		t.Fatalf("invalid REMNANODE_RESOURCE_MAX_PEAK_MIB %q", raw)
	}
	return value << 20
}

func mib(value uint64) float64 {
	return float64(value) / (1 << 20)
}
