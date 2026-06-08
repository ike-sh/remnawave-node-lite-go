//go:build linux

package plugin

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"remnawave-node-lite-go/internal/netadmin"
)

type nftManager struct {
	available bool
}

func newNFTManager() *nftManager {
	manager := &nftManager{available: netadmin.HasCapNetAdmin()}
	if manager.available {
		_ = manager.recreateTables()
	}
	return manager
}

func (m *nftManager) Available() bool {
	return m.available && m != nil
}

func (m *nftManager) recreateTables() error {
	if !m.available {
		return fmt.Errorf("nftables unavailable")
	}
	script := fmt.Sprintf(`
delete table ip %s
delete table ip6 %s
table ip %s {
	set %s { type ipv4_addr; flags timeout; }
	set %s { type ipv4_addr; }
	set %s { type ipv4_addr; }
	set %s { type inet_service; }

	chain input {
		type filter hook input priority -10; policy accept;
		ip saddr @%s drop
		ip saddr @%s drop
	}

	chain forward {
		type filter hook forward priority -10; policy accept;
		ip saddr @%s drop
		ip saddr @%s drop
	}

	chain output {
		type filter hook output priority -10; policy accept;
		ip daddr @%s drop
		tcp dport @%s drop
		udp dport @%s drop
	}
}

table ip6 %s {
	set %s { type ipv6_addr; flags timeout; }
	set %s { type ipv6_addr; }
	set %s { type ipv6_addr; }
	set %s { type inet_service; }

	chain input {
		type filter hook input priority -10; policy accept;
		ip6 saddr @%s drop
		ip6 saddr @%s drop
	}

	chain forward {
		type filter hook forward priority -10; policy accept;
		ip6 saddr @%s drop
		ip6 saddr @%s drop
	}

	chain output {
		type filter hook output priority -10; policy accept;
		ip6 daddr @%s drop
		tcp dport @%s drop
		udp dport @%s drop
	}
}
`, tableName, tableNameV6,
		tableName,
		torrentBlockerSet, ingressFilterIPSet, egressFilterIPSet, egressFilterPortSet,
		ingressFilterIPSet, torrentBlockerSet,
		ingressFilterIPSet, torrentBlockerSet,
		egressFilterIPSet, egressFilterPortSet, egressFilterPortSet,
		tableNameV6,
		torrentBlockerSetV6, ingressFilterIPSetV6, egressFilterIPSetV6, egressFilterPortSetV6,
		ingressFilterIPSetV6, torrentBlockerSetV6,
		ingressFilterIPSetV6, torrentBlockerSetV6,
		egressFilterIPSetV6, egressFilterPortSetV6, egressFilterPortSetV6)
	return runNFTScript(script)
}

func (m *nftManager) blockIP(ip string, timeoutSeconds int) error {
	if !m.available {
		return fmt.Errorf("nftables unavailable")
	}
	table, set, ok := ipTableAndTorrentSet(ip)
	if !ok {
		return fmt.Errorf("invalid ip: %s", ip)
	}
	element := formatNFTElement(ip, timeoutSeconds)
	script := fmt.Sprintf("add element %s %s %s { %s }", tableFamily(table), table, set, element)
	return runNFTScript(script)
}

func (m *nftManager) unblockIP(ip string) error {
	if !m.available {
		return fmt.Errorf("nftables unavailable")
	}
	table, set, ok := ipTableAndTorrentSet(ip)
	if !ok {
		return fmt.Errorf("invalid ip: %s", ip)
	}
	script := fmt.Sprintf("delete element %s %s %s { %s }", tableFamily(table), table, set, ip)
	return runNFTScript(script)
}

func (m *nftManager) syncIngressFilter(ips []string) error {
	if !m.available {
		return nil
	}
	v4, v6 := splitIPVersions(ips)
	if len(v4) > 0 {
		if err := runNFTScript(fmt.Sprintf(
			"add element ip %s %s { %s }",
			tableName, ingressFilterIPSet, strings.Join(v4, ", "),
		)); err != nil {
			return err
		}
	}
	if len(v6) > 0 {
		if err := runNFTScript(fmt.Sprintf(
			"add element ip6 %s %s { %s }",
			tableNameV6, ingressFilterIPSetV6, strings.Join(v6, ", "),
		)); err != nil {
			return err
		}
	}
	return nil
}

func (m *nftManager) syncEgressFilter(ips []string, ports []int) error {
	if !m.available {
		return nil
	}
	v4, v6 := splitIPVersions(ips)
	if len(v4) > 0 {
		if err := runNFTScript(fmt.Sprintf(
			"add element ip %s %s { %s }",
			tableName, egressFilterIPSet, strings.Join(v4, ", "),
		)); err != nil {
			return err
		}
	}
	if len(v6) > 0 {
		if err := runNFTScript(fmt.Sprintf(
			"add element ip6 %s %s { %s }",
			tableNameV6, egressFilterIPSetV6, strings.Join(v6, ", "),
		)); err != nil {
			return err
		}
	}
	if len(ports) == 0 {
		return nil
	}
	portItems := formatPortElements(ports)
	if err := runNFTScript(fmt.Sprintf(
		"add element ip %s %s { %s }",
		tableName, egressFilterPortSet, portItems,
	)); err != nil {
		return err
	}
	return runNFTScript(fmt.Sprintf(
		"add element ip6 %s %s { %s }",
		tableNameV6, egressFilterPortSetV6, portItems,
	))
}

func tableFamily(table string) string {
	if table == tableNameV6 {
		return "ip6"
	}
	return "ip"
}

func formatNFTElement(ip string, timeoutSeconds int) string {
	if timeoutSeconds > 0 {
		return fmt.Sprintf("%s timeout %ds", ip, timeoutSeconds)
	}
	return ip
}

func formatPortElements(ports []int) string {
	items := make([]string, 0, len(ports))
	for _, port := range ports {
		items = append(items, strconv.Itoa(port))
	}
	return strings.Join(items, ", ")
}

func runNFTScript(script string) error {
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(strings.TrimSpace(script))
	return cmd.Run()
}
