//go:build linux

package plugin

import (
	"fmt"
	"os/exec"
	"strings"

	"remnawave-node-lite-go/internal/netadmin"
)

type nftManager struct {
	available bool
	options   NFTOptions
}

func newNFTManager(options ...NFTOptions) *nftManager {
	opts := defaultNFTOptions()
	if len(options) > 0 {
		opts = options[0]
	}
	manager := &nftManager{available: netadmin.HasCapNetAdmin(), options: opts}
	if manager.available {
		if _, err := exec.LookPath("nft"); err != nil {
			manager.available = false
		} else if err := manager.recreateTables(); err != nil {
			manager.available = false
		}
	}
	return manager
}

func (m *nftManager) Available() bool {
	return m != nil && m.available
}

func (m *nftManager) recreateTables() error {
	if !m.available {
		return fmt.Errorf("nftables unavailable")
	}
	return runNFTScript(buildNFTScript(m.options))
}

func (m *nftManager) deleteTables() error {
	if !m.available {
		return nil
	}
	return runNFTScript(fmt.Sprintf("delete table ip %s\ndelete table ip6 %s", tableName, tableNameV6))
}

func (m *nftManager) blockIP(ip string, timeoutSeconds int) error {
	if !m.available {
		return fmt.Errorf("nftables unavailable")
	}
	if timeoutSeconds < 0 {
		return fmt.Errorf("timeout must be zero or positive")
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
	if err := deleteNFTElement(table, set, ip); err != nil {
		return err
	}
	_, ingressSet, _ := ipTableAndIngressSet(ip)
	return deleteNFTElement(table, ingressSet, ip)
}

// The upstream removeAddresses operation ignores absent elements. Keep table
// and capability failures visible by checking that the target set still exists.
func deleteNFTElement(table, set, ip string) error {
	family := tableFamily(table)
	script := fmt.Sprintf("delete element %s %s %s { %s }", family, table, set, ip)
	err := runNFTScript(script)
	if err != nil && (strings.Contains(err.Error(), "No such file or directory") || strings.Contains(err.Error(), "element does not exist")) {
		if listErr := exec.Command("nft", "list", "set", family, table, set).Run(); listErr == nil {
			return nil
		}
	}
	return err
}

func (m *nftManager) syncIngressFilter(ips []string) error {
	if !m.available {
		return nil
	}
	v4, v6 := normalizeFilterPrefixes(ips)
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
	v4, v6 := normalizeFilterPrefixes(ips)
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

func runNFTScript(script string) error {
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(strings.TrimSpace(script))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("nft -f -: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
