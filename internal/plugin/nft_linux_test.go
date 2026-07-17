//go:build linux

package plugin

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNFTManagerMixedMissingUnblockStillRemovesPresentAddress(t *testing.T) {
	t.Parallel()

	present := map[string]bool{"203.0.113.10": true}
	manager := &nftManager{
		capable: true,
		owned:   true,
		healthy: true,
		run: func(_ context.Context, script string) error {
			for address := range present {
				if strings.Contains(script, "{ "+address+" }") && present[address] {
					present[address] = false
					return nil
				}
			}
			return &nftCommandError{err: errors.New("exit status 1"), output: "No such element"}
		},
	}
	if err := manager.UnblockIPs(context.Background(), []string{"203.0.113.10", "203.0.113.11"}); err != nil {
		t.Fatal(err)
	}
	if present["203.0.113.10"] {
		t.Fatal("present address survived a mixed present/missing unblock")
	}
}

func TestNFTManagerSingleMissingUnblockIsIdempotent(t *testing.T) {
	t.Parallel()

	calls := 0
	manager := &nftManager{
		capable: true,
		owned:   true,
		healthy: true,
		run: func(context.Context, string) error {
			calls++
			return &nftCommandError{err: errors.New("exit status 1"), output: "No such element"}
		},
	}
	if err := manager.UnblockIPs(context.Background(), []string{"203.0.113.10"}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("runner calls = %d, want torrent and ingress sets", calls)
	}
}

func TestNFTManagerAmbiguousMissingElementVerifiesStructure(t *testing.T) {
	t.Parallel()

	calls := 0
	manager := &nftManager{
		capable: true,
		owned:   true,
		healthy: true,
		run: func(_ context.Context, script string) error {
			calls++
			if script == renderNFTStructureProbe() {
				return nil
			}
			return &nftCommandError{err: errors.New("exit status 1"), output: "No such file or directory"}
		},
	}
	if err := manager.UnblockIPs(context.Background(), []string{"203.0.113.10"}); err != nil {
		t.Fatal(err)
	}
	if calls != 3 || !manager.Available() {
		t.Fatalf("runner calls=%d available=%v, want two deletes and one healthy probe", calls, manager.Available())
	}
}

func TestNFTManagerMissingStructureDegradesAmbiguousUnblock(t *testing.T) {
	t.Parallel()

	calls := 0
	manager := &nftManager{
		capable: true,
		owned:   true,
		healthy: true,
		run: func(context.Context, string) error {
			calls++
			return &nftCommandError{err: errors.New("exit status 1"), output: "No such file or directory"}
		},
	}
	if err := manager.UnblockIPs(context.Background(), []string{"203.0.113.10"}); err == nil {
		t.Fatal("unblock accepted a missing nftables structure")
	}
	if calls != 3 || manager.Available() {
		t.Fatalf("runner calls=%d available=%v, want a failed structure probe and degraded backend", calls, manager.Available())
	}
}

func TestNFTManagerResetRecoversUnhealthyOwnedTables(t *testing.T) {
	t.Parallel()

	calls := 0
	manager := &nftManager{
		capable: true,
		owned:   true,
		healthy: false,
		run: func(context.Context, string) error {
			calls++
			return nil
		},
	}
	if err := manager.Reset(context.Background(), firewallConfig{}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !manager.Available() {
		t.Fatalf("reset calls=%d available=%v, want one successful recovery", calls, manager.Available())
	}
}

func TestNFTManagerCloseDeletesUnhealthyOwnedTables(t *testing.T) {
	t.Parallel()

	var script string
	manager := &nftManager{
		capable: true,
		owned:   true,
		healthy: false,
		run: func(_ context.Context, value string) error {
			script = value
			return nil
		},
	}
	if err := manager.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if script != renderNFTDeleteTables() || manager.owned || manager.healthy {
		t.Fatalf("close state: script=%q owned=%v healthy=%v", script, manager.owned, manager.healthy)
	}
}

func TestNFTManagerDynamicMutationFailureRemainsRetryable(t *testing.T) {
	t.Parallel()

	calls := 0
	manager := &nftManager{
		capable: true,
		owned:   true,
		healthy: true,
		run: func(context.Context, string) error {
			calls++
			if calls == 1 {
				return errors.New("transient nft failure")
			}
			return nil
		},
	}
	items := []BlockIP{{IP: "203.0.113.10", Timeout: 60}}
	if err := manager.BlockIPs(context.Background(), items); err == nil {
		t.Fatal("first dynamic mutation unexpectedly succeeded")
	}
	if !manager.Available() {
		t.Fatal("single dynamic transaction failure poisoned the static table state")
	}
	if err := manager.BlockIPs(context.Background(), items); err != nil {
		t.Fatalf("retry dynamic mutation: %v", err)
	}
}

func TestNFTManagerMissingStructureDegradesDynamicBlock(t *testing.T) {
	t.Parallel()

	manager := &nftManager{
		capable: true,
		owned:   true,
		healthy: true,
		run: func(context.Context, string) error {
			return &nftCommandError{err: errors.New("exit status 1"), output: "No such file or directory"}
		},
	}
	items := []BlockIP{{IP: "203.0.113.10", Timeout: 60}}
	if err := manager.BlockIPs(context.Background(), items); err == nil {
		t.Fatal("dynamic block accepted a missing nftables structure")
	}
	if manager.Available() {
		t.Fatal("missing nftables structure left the backend healthy")
	}
}

func TestNFTManagerUnblockFailureRemainsRetryable(t *testing.T) {
	t.Parallel()

	calls := 0
	manager := &nftManager{
		capable: true,
		owned:   true,
		healthy: true,
		run: func(context.Context, string) error {
			calls++
			if calls == 1 {
				return errors.New("transient nft failure")
			}
			return nil
		},
	}
	items := []string{"203.0.113.10"}
	if err := manager.UnblockIPs(context.Background(), items); err == nil {
		t.Fatal("first unblock unexpectedly succeeded")
	}
	if !manager.Available() {
		t.Fatal("ordinary unblock failure poisoned the static table state")
	}
	if err := manager.UnblockIPs(context.Background(), items); err != nil {
		t.Fatalf("retry unblock: %v", err)
	}
}
