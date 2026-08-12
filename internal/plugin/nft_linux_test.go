//go:build linux

package plugin

import "testing"

func TestBlockIPRejectsNegativeTimeoutBeforeNFTInvocation(t *testing.T) {
	manager := &nftManager{available: true}
	if err := manager.blockIP("203.0.113.1", -1); err == nil {
		t.Fatal("negative timeout must not become a permanent block")
	}
}
