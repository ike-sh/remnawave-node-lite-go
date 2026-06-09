package version

import (
	"os"
	"strings"
	"testing"
)

func TestContractVersionMatchesBaselineFile(t *testing.T) {
	raw, err := os.ReadFile("contract.version")
	if err != nil {
		t.Fatalf("read contract.version: %v", err)
	}
	want := strings.TrimSpace(string(raw))
	if ContractVersion != want {
		t.Fatalf("ContractVersion = %q, contract.version = %q", ContractVersion, want)
	}
}

func TestReportedNodeVersionEnvOverride(t *testing.T) {
	t.Setenv("NODE_CONTRACT_VERSION", "3.0.0")
	if got := ReportedNodeVersion(); got != "3.0.0" {
		t.Fatalf("ReportedNodeVersion() = %q, want 3.0.0", got)
	}
}

func TestReportedNodeVersionDefault(t *testing.T) {
	t.Setenv("NODE_CONTRACT_VERSION", "")
	if got := ReportedNodeVersion(); got != ContractVersion {
		t.Fatalf("ReportedNodeVersion() = %q, want default %q", got, ContractVersion)
	}
}

func TestStringIncludesBothVersions(t *testing.T) {
	got := String()
	if got == "" {
		t.Fatal("String() returned empty")
	}
	if !strings.Contains(got, Version) {
		t.Fatalf("String() missing lite version: %q", got)
	}
	if !strings.Contains(got, ContractVersion) {
		t.Fatalf("String() missing contract version: %q", got)
	}
}
