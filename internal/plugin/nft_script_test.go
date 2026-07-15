package plugin

import (
	"errors"
	"strings"
	"testing"
)

func TestRenderNFTConfigBuildsOneCompleteTransaction(t *testing.T) {
	t.Parallel()

	script := renderNFTConfig(firewallConfig{
		ingressIPs:  []string{"10.0.0.1", "10.0.0.0/24", "2001:db8::1"},
		egressIPs:   []string{"192.0.2.1", "2001:db8:1::/64"},
		egressPorts: []int{443, 80, 443},
	})

	for _, fragment := range []string{
		"delete table ip remnanode",
		"delete table ip6 remnanode6",
		"table ip remnanode {",
		"table ip6 remnanode6 {",
		"add element ip remnanode ingress-filter-ip { 10.0.0.0/24 }",
		"add element ip6 remnanode6 ingress-filter-ip6 { 2001:db8::1/128 }",
		"add element ip remnanode egress-filter-ip { 192.0.2.1/32 }",
		"add element ip6 remnanode6 egress-filter-ip6 { 2001:db8:1::/64 }",
		"add element ip remnanode egress-filter-port { 80, 443 }",
		"add element ip6 remnanode6 egress-filter-port6 { 80, 443 }",
	} {
		if !strings.Contains(script, fragment) {
			t.Errorf("script missing %q:\n%s", fragment, script)
		}
	}
}

func TestRenderNFTBlockBatchesFamiliesAndDeduplicates(t *testing.T) {
	t.Parallel()

	script, err := renderNFTBlock([]BlockIP{
		{IP: "203.0.113.1", Timeout: 30},
		{IP: "203.0.113.1", Timeout: 60},
		{IP: "2001:db8::1", Timeout: 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(script, "203.0.113.1") != 1 || !strings.Contains(script, "203.0.113.1 timeout 60s") {
		t.Fatalf("IPv4 batch was not deduplicated with latest timeout:\n%s", script)
	}
	if !strings.Contains(script, "add element ip6 remnanode6 torrent-blocker6 { 2001:db8::1 }") {
		t.Fatalf("IPv6 batch missing:\n%s", script)
	}
}

func TestRenderNFTBlockRejectsInvalidIP(t *testing.T) {
	t.Parallel()
	if _, err := renderNFTBlock([]BlockIP{{IP: "not-an-ip", Timeout: 1}}); err == nil {
		t.Fatal("invalid IP was accepted")
	}
}

func TestRenderNFTBlockPreservesOfficialNegativeTimeout(t *testing.T) {
	t.Parallel()

	script, err := renderNFTBlock([]BlockIP{{IP: "203.0.113.1", Timeout: -1}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "203.0.113.1 timeout -1s") {
		t.Fatalf("negative timeout was silently converted to permanent block:\n%s", script)
	}
}

func TestRenderNFTUnblockRemovesTorrentAndIngressElements(t *testing.T) {
	t.Parallel()

	commands, err := renderNFTUnblock([]string{"203.0.113.1", "2001:db8::1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 4 {
		t.Fatalf("commands = %d, want 4: %#v", len(commands), commands)
	}
	joined := strings.Join(commands, "\n")
	for _, set := range []string{torrentBlockerSet, ingressFilterIPSet, torrentBlockerSetV6, ingressFilterIPSetV6} {
		if !strings.Contains(joined, set) {
			t.Errorf("unblock commands missing set %q: %s", set, joined)
		}
	}
}

func TestNFTCommandErrorIncludesCommandOutput(t *testing.T) {
	t.Parallel()
	err := &nftCommandError{err: errors.New("exit status 1"), output: "syntax error"}
	if got := err.Error(); !strings.Contains(got, "exit status 1") || !strings.Contains(got, "syntax error") {
		t.Fatalf("error = %q", got)
	}
}
