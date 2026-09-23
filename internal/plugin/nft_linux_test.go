//go:build linux

package plugin

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"remnawave-node-lite-go/internal/netadmin"
)

func TestNFTManagerWithoutCapIsUnavailable(t *testing.T) {
	if netadmin.HasCapNetAdmin() {
		t.Skip("container has CAP_NET_ADMIN")
	}
	if manager := newNFTManager(); manager.Available() {
		t.Fatal("manager available without CAP_NET_ADMIN")
	}
}

func TestNFTMissingExecutableReturnsError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := runNFTScript("add table ip remnanode"); err == nil {
		t.Fatal("missing nft executable must fail")
	}
}

func TestBlockIPRejectsNegativeTimeoutBeforeNFTInvocation(t *testing.T) {
	manager := &nftManager{available: true}
	if err := manager.blockIP("203.0.113.1", -1); err == nil {
		t.Fatal("negative timeout must not become a permanent block")
	}
}

func TestNFTablesIntegration(t *testing.T) {
	if os.Getenv("REMNANODE_NFT_INTEGRATION") != "1" {
		t.Skip("set REMNANODE_NFT_INTEGRATION=1 inside an isolated network namespace")
	}

	manager := newNFTManager()
	if !manager.Available() {
		t.Fatal("CAP_NET_ADMIN is unavailable")
	}
	if err := manager.recreateTables(); err != nil {
		t.Fatalf("recreate tables: %v", err)
	}
	if err := manager.recreateTables(); err != nil {
		t.Fatalf("repeat recreate tables: %v", err)
	}
	if err := manager.syncIngressFilter(nil); err != nil {
		t.Fatalf("empty ingress: %v", err)
	}
	if err := manager.syncEgressFilter(nil, nil); err != nil {
		t.Fatalf("empty egress: %v", err)
	}
	if err := manager.blockIP("192.0.2.10", 30); err != nil {
		t.Fatalf("block IPv4: %v", err)
	}
	if err := manager.blockIP("192.0.2.10", 30); err != nil {
		t.Fatalf("repeat block IPv4: %v", err)
	}
	if err := manager.blockIP("2001:db8::10", 30); err != nil {
		t.Fatalf("block IPv6: %v", err)
	}
	if err := manager.syncIngressFilter([]string{"198.51.100.0/24", "198.51.100.10"}); err != nil {
		t.Fatalf("sync ingress filter: %v", err)
	}
	if err := manager.syncEgressFilter([]string{"203.0.113.0/24", "2001:db8:1::/48"}, []int{65534, 65534}); err != nil {
		t.Fatalf("sync egress filter: %v", err)
	}
	if err := manager.unblockIP("192.0.2.10"); err != nil {
		t.Fatalf("unblock IPv4: %v", err)
	}
	if err := manager.unblockIP("192.0.2.10"); err != nil {
		t.Fatalf("repeat unblock IPv4: %v", err)
	}
	if err := manager.unblockIP("2001:db8::10"); err != nil {
		t.Fatalf("unblock IPv6: %v", err)
	}
	if err := manager.recreateTables(); err != nil {
		t.Fatalf("recreate after sync: %v", err)
	}
	listing, err := exec.Command("nft", "list", "set", "ip", tableName, torrentBlockerSet).CombinedOutput()
	if err != nil {
		t.Fatalf("list recreated set: %v: %s", err, listing)
	}
	if strings.Contains(string(listing), "192.0.2.10") {
		t.Fatalf("stale block after recreate: %s", listing)
	}
	withReply := newNFTManager(NFTOptions{Logging: false, AcceptReplyTraffic: true})
	if !withReply.Available() {
		t.Fatal("optional conntrack reply rule could not be created")
	}
	chain, err := exec.Command("nft", "list", "chain", "ip", tableName, "input").CombinedOutput()
	if err != nil {
		t.Fatalf("list reply chain: %v: %s", err, chain)
	}
	if !strings.Contains(string(chain), "ct direction reply accept") || strings.Contains(string(chain), " log ") {
		t.Fatalf("reply/logging options not applied: %s", chain)
	}
	if err := withReply.deleteTables(); err != nil {
		t.Fatalf("shutdown table cleanup: %v", err)
	}
	if err := exec.Command("nft", "list", "table", "ip", tableName).Run(); err == nil {
		t.Fatal("IPv4 table remains after cleanup")
	}
	if err := exec.Command("nft", "list", "table", "ip6", tableNameV6).Run(); err == nil {
		t.Fatal("IPv6 table remains after cleanup")
	}
}
