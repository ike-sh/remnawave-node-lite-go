package plugin

import (
	"errors"
	"math"
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
		"flags timeout; size 16384;",
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

func TestRenderNFTStaticUpdateDoesNotTouchDynamicSetsOrTables(t *testing.T) {
	t.Parallel()

	script := renderNFTStaticUpdate(firewallConfig{
		ingressIPs:  []string{"192.0.2.0/24"},
		egressPorts: []int{443},
	})
	for _, fragment := range []string{
		"flush set ip remnanode ingress-filter-ip",
		"flush set ip6 remnanode6 egress-filter-port6",
		"add element ip remnanode ingress-filter-ip { 192.0.2.0/24 }",
		"add element ip remnanode egress-filter-port { 443 }",
	} {
		if !strings.Contains(script, fragment) {
			t.Errorf("static update missing %q:\n%s", fragment, script)
		}
	}
	for _, forbidden := range []string{"torrent-blocker", "delete table", "table ip remnanode {"} {
		if strings.Contains(script, forbidden) {
			t.Errorf("static update contains %q:\n%s", forbidden, script)
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

func TestRenderNFTBlockRendersZeroTimeoutAsPermanent(t *testing.T) {
	t.Parallel()

	script, err := renderNFTBlock([]BlockIP{{IP: "203.0.113.1", Timeout: 0}})
	if err != nil {
		t.Fatal(err)
	}
	if script != "add element ip remnanode torrent-blocker { 203.0.113.1 }" {
		t.Fatalf("permanent block script = %q", script)
	}
}

func TestRenderNFTBlockAcceptsLongTimeout(t *testing.T) {
	t.Parallel()

	for _, timeout := range []float64{31 * 24 * 60 * 60, 1e20} {
		script, err := renderNFTBlock([]BlockIP{{IP: "203.0.113.1", Timeout: timeout}})
		if err != nil {
			t.Fatalf("timeout %v was rejected: %v", timeout, err)
		}
		if !strings.Contains(script, " timeout ") {
			t.Fatalf("timeout %v was omitted from script: %s", timeout, script)
		}
	}
}

func TestRenderNFTBlockRejectsUnsafeTimeout(t *testing.T) {
	t.Parallel()

	for _, timeout := range []float64{-1, math.Inf(1), math.NaN()} {
		if _, err := renderNFTBlock([]BlockIP{{IP: "203.0.113.1", Timeout: timeout}}); err == nil {
			t.Fatalf("timeout %v was accepted", timeout)
		}
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

func TestRenderNFTUnblockIsolatesEveryAddressTransaction(t *testing.T) {
	t.Parallel()

	commands, err := renderNFTUnblock([]string{"203.0.113.1", "203.0.113.2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 4 {
		t.Fatalf("commands = %d, want 4: %#v", len(commands), commands)
	}
	for _, command := range commands {
		if strings.Contains(command, "203.0.113.1, 203.0.113.2") {
			t.Fatalf("addresses share one rollback domain: %q", command)
		}
	}
}

func TestNFTMutationLimitsAreEnforcedBeforeRendering(t *testing.T) {
	t.Parallel()

	blocks := make([]BlockIP, maxNFTBlockBatch+1)
	if _, err := renderNFTBlock(blocks); err == nil {
		t.Fatal("oversized block batch was accepted")
	}
	unblocks := make([]string, maxNFTUnblockBatch+1)
	if _, err := renderNFTUnblock(unblocks); err == nil {
		t.Fatal("oversized unblock batch was accepted")
	}
	if err := validateNFTScript(strings.Repeat("x", maxNFTScriptBytes+1)); err == nil {
		t.Fatal("oversized nft script was accepted")
	}
}

func TestNFTCommandErrorIncludesCommandOutput(t *testing.T) {
	t.Parallel()
	err := &nftCommandError{err: errors.New("exit status 1"), output: "syntax error"}
	if got := err.Error(); !strings.Contains(got, "exit status 1") || !strings.Contains(got, "syntax error") {
		t.Fatalf("error = %q", got)
	}
}

func TestMissingNFTElementErrorsAreIdempotent(t *testing.T) {
	t.Parallel()

	for _, message := range []string{
		"Error: No such file or directory",
		"Error: No such element",
		"Error: element does not exist",
	} {
		if !isMissingNFTElement(errors.New(message)) {
			t.Errorf("missing element error was not recognized: %q", message)
		}
	}
	if isMissingNFTElement(errors.New("Error: Operation not permitted")) {
		t.Fatal("unrelated nft error was treated as a missing element")
	}
	if !isAmbiguousNFTNotFound(errors.New("Error: No such file or directory")) {
		t.Fatal("generic nft ENOENT was not classified as ambiguous")
	}
	if isAmbiguousNFTNotFound(errors.New("Error: No such element")) {
		t.Fatal("explicit missing-element error was classified as ambiguous")
	}
}

func TestNFTStructureProbeIsBoundedAndCoversOwnedSets(t *testing.T) {
	t.Parallel()

	script := renderNFTStructureProbe()
	if err := validateNFTScript(script); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"list set ip remnanode torrent-blocker",
		"list set ip remnanode ingress-filter-ip",
		"list set ip remnanode egress-filter-ip",
		"list set ip remnanode egress-filter-port",
		"list chain ip remnanode input",
		"list chain ip remnanode forward",
		"list chain ip remnanode output",
		"list set ip6 remnanode6 torrent-blocker6",
		"list set ip6 remnanode6 ingress-filter-ip6",
		"list set ip6 remnanode6 egress-filter-ip6",
		"list set ip6 remnanode6 egress-filter-port6",
		"list chain ip6 remnanode6 input",
		"list chain ip6 remnanode6 forward",
		"list chain ip6 remnanode6 output",
	} {
		if !strings.Contains(script, fragment) {
			t.Errorf("structure probe missing %q", fragment)
		}
	}
}
