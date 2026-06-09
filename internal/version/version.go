package version

import (
	"fmt"
	"os"
	"strings"
)

// Version is the lite-go release version (overridable via -ldflags at build time).
var Version = "0.8.14"

// ContractVersion is the upstream @remnawave/node version reported to Panel as nodeVersion.
// Default must stay in sync with contract.version and contract-sync CI.
// Overridable via -ldflags at build time.
var ContractVersion = "2.7.0"

const releaseRepo = "ike-sh/remnawave-node-lite-go"

// ReportedNodeVersion returns the nodeVersion sent to Panel.
// Priority: NODE_CONTRACT_VERSION env > ContractVersion (build-time default).
// Mirrors upstream reading package.json version at bootstrap.
func ReportedNodeVersion() string {
	if v := strings.TrimSpace(os.Getenv("NODE_CONTRACT_VERSION")); v != "" {
		return v
	}
	return ContractVersion
}

func String() string {
	return fmt.Sprintf("remnawave-node-lite-go %s (contract %s)", Version, ReportedNodeVersion())
}

func ReleaseAssetURL(tag, arch string) string {
	return fmt.Sprintf(
		"https://github.com/%s/releases/download/%s/remnanode-lite_linux_%s.tar.gz",
		releaseRepo,
		tag,
		arch,
	)
}

func InstallScriptURL(tag, script string) string {
	return fmt.Sprintf(
		"https://raw.githubusercontent.com/%s/%s/scripts/%s",
		releaseRepo,
		tag,
		script,
	)
}
