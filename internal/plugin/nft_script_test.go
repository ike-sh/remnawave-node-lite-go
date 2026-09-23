package plugin

import (
	"strings"
	"testing"
)

func TestNFTScriptOfficialFlagsAndFamilies(t *testing.T) {
	for _, tc := range []struct {
		name           string
		options        NFTOptions
		logging, reply bool
	}{
		{"official-default", defaultNFTOptions(), true, false},
		{"logging-disabled-replies-enabled", NFTOptions{Logging: false, AcceptReplyTraffic: true}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := buildNFTScript(tc.options)
			for _, needle := range []string{"table ip remnanode {", "table ip6 remnanode6 {", "flags interval", "flags timeout", "ip saddr @ingress-filter-ip", "ip6 saddr @ingress-filter-ip6", "tcp dport @egress-filter-port", "udp dport @egress-filter-port6"} {
				if !strings.Contains(script, needle) {
					t.Fatalf("missing %q", needle)
				}
			}
			if got := strings.Count(script, "ct direction reply accept"); (got == 4) != tc.reply {
				t.Fatalf("reply rules = %d", got)
			}
			if got := strings.Count(script, "log prefix"); (got == 8) != tc.logging {
				t.Fatalf("logging rules = %d", got)
			}
			if strings.Count(script, "table ip remnanode {") != 1 || strings.Count(script, "table ip6 remnanode6 {") != 1 {
				t.Fatal("duplicate table creation")
			}
		})
	}
}

func TestUnblockTargetsTorrentAndIngressForBothFamilies(t *testing.T) {
	for _, tc := range []struct{ ip, wantTable, wantTorrent, wantIngress string }{
		{"192.0.2.1", tableName, torrentBlockerSet, ingressFilterIPSet},
		{"2001:db8::1", tableNameV6, torrentBlockerSetV6, ingressFilterIPSetV6},
	} {
		table, torrent, ok := ipTableAndTorrentSet(tc.ip)
		if !ok || table != tc.wantTable || torrent != tc.wantTorrent {
			t.Fatalf("torrent mapping %s: %s/%s", tc.ip, table, torrent)
		}
		table, ingress, ok := ipTableAndIngressSet(tc.ip)
		if !ok || table != tc.wantTable || ingress != tc.wantIngress {
			t.Fatalf("ingress mapping %s: %s/%s", tc.ip, table, ingress)
		}
	}
}

func TestNFTPortElementsDeduplicateAndSort(t *testing.T) {
	if got := formatPortElements([]int{443, 80, 443, 80}); got != "80, 443" {
		t.Fatalf("ports = %q", got)
	}
	if got := formatPortElements(nil); got != "" {
		t.Fatalf("empty ports = %q", got)
	}
}
