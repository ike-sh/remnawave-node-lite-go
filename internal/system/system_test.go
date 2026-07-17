package system

import (
	"runtime"
	"testing"
)

func TestNodeArchitectureMatchesProcessArchNames(t *testing.T) {
	// Oracle: Node.js os.arch()/process.arch documented values.
	tests := map[string]string{
		"386":     "ia32",
		"amd64":   "x64",
		"arm":     "arm",
		"arm64":   "arm64",
		"mipsle":  "mipsel",
		"ppc64le": "ppc64",
		"riscv64": "riscv64",
		"s390x":   "s390x",
	}
	for goarch, want := range tests {
		if got := nodeArchitecture(goarch); got != want {
			t.Errorf("nodeArchitecture(%q) = %q, want %q", goarch, got, want)
		}
	}
}

func TestNodePlatformMatchesProcessPlatformNames(t *testing.T) {
	tests := map[string]string{
		"darwin":  "darwin",
		"linux":   "linux",
		"windows": "win32",
		"illumos": "sunos",
		"solaris": "sunos",
	}
	for goos, want := range tests {
		if got := nodePlatform(goos); got != want {
			t.Errorf("nodePlatform(%q) = %q, want %q", goos, got, want)
		}
	}
}

func TestNodeSystemTypeMatchesOSNames(t *testing.T) {
	tests := map[string]string{
		"aix":     "AIX",
		"darwin":  "Darwin",
		"freebsd": "FreeBSD",
		"linux":   "Linux",
		"openbsd": "OpenBSD",
		"solaris": "SunOS",
		"windows": "Windows_NT",
	}
	for goos, want := range tests {
		if got := nodeSystemType(goos); got != want {
			t.Errorf("nodeSystemType(%q) = %q, want %q", goos, got, want)
		}
	}
}

func TestGetInfoUsesKernelIdentity(t *testing.T) {
	info := GetInfo()
	if info.Arch != nodeArchitecture(runtime.GOARCH) {
		t.Fatalf("arch = %q, want Node name for %q", info.Arch, runtime.GOARCH)
	}
	if info.Platform != nodePlatform(runtime.GOOS) {
		t.Fatalf("platform = %q, want Node name for %q", info.Platform, runtime.GOOS)
	}
	if info.Type != nodeSystemType(runtime.GOOS) {
		t.Fatalf("type = %q, want %q", info.Type, nodeSystemType(runtime.GOOS))
	}
	if info.Release == "" || info.Version == "" {
		t.Fatalf("kernel identity is incomplete: release=%q version=%q", info.Release, info.Version)
	}
	if info.Release == runtime.GOOS || info.Version == runtime.Version() {
		t.Fatalf("kernel fields contain Go runtime placeholders: %+v", info)
	}
}
