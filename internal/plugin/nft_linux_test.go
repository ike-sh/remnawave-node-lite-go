//go:build linux

package plugin

import (
	"os"
	"testing"
)

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
	if err := manager.blockIP("192.0.2.10", 30); err != nil {
		t.Fatalf("block IPv4: %v", err)
	}
	if err := manager.blockIP("2001:db8::10", 30); err != nil {
		t.Fatalf("block IPv6: %v", err)
	}
	if err := manager.syncIngressFilter([]string{"198.51.100.0/24", "198.51.100.10"}); err != nil {
		t.Fatalf("sync ingress filter: %v", err)
	}
	if err := manager.syncEgressFilter([]string{"203.0.113.0/24", "2001:db8:1::/48"}, []int{65534}); err != nil {
		t.Fatalf("sync egress filter: %v", err)
	}
	if err := manager.unblockIP("192.0.2.10"); err != nil {
		t.Fatalf("unblock IPv4: %v", err)
	}
	if err := manager.unblockIP("2001:db8::10"); err != nil {
		t.Fatalf("unblock IPv6: %v", err)
	}
}
