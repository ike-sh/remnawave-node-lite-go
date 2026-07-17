//go:build linux

package plugin

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

const (
	nftIntegrationEnv = "REMNANODE_NFT_INTEGRATION"
	nftNetNSChildEnv  = "REMNANODE_NFT_NETNS_CHILD"
)

func TestNFTManagerInNetworkNamespace(t *testing.T) {
	if os.Getenv(nftIntegrationEnv) != "1" {
		t.Skip("set REMNANODE_NFT_INTEGRATION=1 to run the isolated nftables test")
	}
	if _, err := exec.LookPath("nft"); err != nil {
		t.Fatalf("nft executable is required: %v", err)
	}
	if os.Getenv(nftNetNSChildEnv) != "1" {
		runNFTIntegrationChild(t)
		return
	}

	manager := newNFTManager()
	if !manager.capable {
		t.Fatal("network namespace child does not have CAP_NET_ADMIN")
	}
	if err := manager.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	defer func() {
		if err := manager.Close(context.Background()); err != nil {
			t.Errorf("cleanup Close: %v", err)
		}
	}()

	first := firewallConfig{
		ingressIPs:  []string{"10.0.0.0/8", "2001:db8::/32"},
		egressIPs:   []string{"192.0.2.0/24", "2001:db8:1::/48"},
		egressPorts: []int{53, 443},
	}
	if err := manager.Apply(context.Background(), first); err != nil {
		t.Fatalf("Apply first plan: %v", err)
	}
	assertNFTContains(t, "list", "set", "ip", tableName, ingressFilterIPSet, "10.0.0.0/8")
	assertNFTContains(t, "list", "set", "ip6", tableNameV6, ingressFilterIPSetV6, "2001:db8::/32")
	assertNFTContains(t, "list", "set", "ip", tableName, egressFilterPortSet, "53", "443")

	second := firewallConfig{
		ingressIPs:  []string{"172.16.0.0/12"},
		egressIPs:   []string{"198.51.100.0/24"},
		egressPorts: []int{25},
	}
	if err := manager.Apply(context.Background(), second); err != nil {
		t.Fatalf("replace existing plan: %v", err)
	}
	assertNFTContains(t, "list", "set", "ip", tableName, ingressFilterIPSet, "172.16.0.0/12")
	assertNFTNotContains(t, "list", "set", "ip", tableName, ingressFilterIPSet, "10.0.0.0/8")

	blocks := []BlockIP{
		{IP: "203.0.113.10", Timeout: 60},
		{IP: "2001:db8:ffff::10", Timeout: 0},
	}
	if err := manager.BlockIPs(context.Background(), blocks); err != nil {
		t.Fatalf("BlockIPs: %v", err)
	}
	if err := manager.BlockIPs(context.Background(), []BlockIP{{IP: "203.0.113.10", Timeout: 120}}); err != nil {
		t.Fatalf("retry BlockIPs: %v", err)
	}
	assertNFTContains(t, "list", "set", "ip", tableName, torrentBlockerSet, "203.0.113.10")
	assertNFTContains(t, "list", "set", "ip6", tableNameV6, torrentBlockerSetV6, "2001:db8:ffff::10")
	third := firewallConfig{ingressIPs: []string{"10.10.0.0/16"}, egressPorts: []int{8443}}
	if err := manager.Apply(context.Background(), third); err != nil {
		t.Fatalf("static update with dynamic blocks: %v", err)
	}
	assertNFTContains(t, "list", "set", "ip", tableName, torrentBlockerSet, "203.0.113.10")
	assertNFTContains(t, "list", "set", "ip6", tableNameV6, torrentBlockerSetV6, "2001:db8:ffff::10")
	assertNFTContains(t, "list", "set", "ip", tableName, ingressFilterIPSet, "10.10.0.0/16")

	// The missing IPv4 address must not roll back removal of the present one.
	if err := manager.UnblockIPs(context.Background(), []string{"203.0.113.11", "203.0.113.10", "2001:db8:ffff::10"}); err != nil {
		t.Fatalf("UnblockIPs: %v", err)
	}
	if err := manager.UnblockIPs(context.Background(), []string{"203.0.113.10", "2001:db8:ffff::10"}); err != nil {
		t.Fatalf("retry UnblockIPs: %v", err)
	}
	assertNFTNotContains(t, "list", "set", "ip", tableName, torrentBlockerSet, "203.0.113.10")
	assertNFTNotContains(t, "list", "set", "ip6", tableNameV6, torrentBlockerSetV6, "2001:db8:ffff::10")

	if err := manager.Reset(context.Background(), second); err != nil {
		t.Fatalf("recreate committed plan: %v", err)
	}
	assertNFTContains(t, "list", "set", "ip", tableName, ingressFilterIPSet, "172.16.0.0/12")

	if err := manager.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	assertNFTTableMissing(t, "ip", tableName)
	assertNFTTableMissing(t, "ip6", tableNameV6)
}

func runNFTIntegrationChild(t *testing.T) {
	t.Helper()
	unshare, err := exec.LookPath("unshare")
	if err != nil {
		t.Fatalf("unshare executable is required: %v", err)
	}
	args := []string{"--net"}
	if os.Geteuid() != 0 {
		args = append([]string{"--user", "--map-root-user"}, args...)
	}
	args = append(args, os.Args[0], "-test.run=^TestNFTManagerInNetworkNamespace$", "-test.count=1", "-test.v")
	command := exec.Command(unshare, args...)
	command.Env = append(os.Environ(), nftNetNSChildEnv+"=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("isolated nftables test failed: %v\n%s", err, output)
	}
	t.Logf("isolated nftables test output:\n%s", output)
}

func assertNFTContains(t *testing.T, args ...string) {
	t.Helper()
	if len(args) < 6 {
		t.Fatal("assertNFTContains requires five command arguments and expected fragments")
	}
	// The first five values form `nft list set`; remaining values are fragments.
	commandArgs := args[:5]
	wants := args[5:]
	output, err := exec.Command("nft", commandArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("nft %s: %v\n%s", strings.Join(commandArgs, " "), err, output)
	}
	text := string(output)
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Errorf("nft %s output missing %q:\n%s", strings.Join(commandArgs, " "), want, text)
		}
	}
}

func assertNFTNotContains(t *testing.T, args ...string) {
	t.Helper()
	if len(args) < 6 {
		t.Fatal("assertNFTNotContains requires five command arguments and expected fragments")
	}
	commandArgs := args[:5]
	unwanted := args[5:]
	output, err := exec.Command("nft", commandArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("nft %s: %v\n%s", strings.Join(commandArgs, " "), err, output)
	}
	text := string(output)
	for _, value := range unwanted {
		if strings.Contains(text, value) {
			t.Errorf("nft %s output unexpectedly contains %q:\n%s", strings.Join(commandArgs, " "), value, text)
		}
	}
}

func assertNFTTableMissing(t *testing.T, family, table string) {
	t.Helper()
	output, err := exec.Command("nft", "list", "table", family, table).CombinedOutput()
	if err == nil {
		t.Fatalf("nft table %s %s still exists:\n%s", family, table, output)
	}
}
