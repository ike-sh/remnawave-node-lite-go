package plugin

import (
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

func renderNFTConfig(config firewallConfig) string {
	ingressV4, ingressV6 := normalizeFilterPrefixes(config.ingressIPs)
	egressV4, egressV6 := normalizeFilterPrefixes(config.egressIPs)
	ports := normalizedPorts(config.egressPorts)

	base := fmt.Sprintf(`
add table ip %s
add table ip6 %s
delete table ip %s
delete table ip6 %s
table ip %s {
	set %s { type ipv4_addr; flags timeout; }
	set %s { type ipv4_addr; flags interval; }
	set %s { type ipv4_addr; flags interval; }
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
	set %s { type ipv6_addr; flags interval; }
	set %s { type ipv6_addr; flags interval; }
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
		tableName, tableNameV6,
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

	commands := []string{strings.TrimSpace(base)}
	appendElementCommand(&commands, "ip", tableName, ingressFilterIPSet, ingressV4)
	appendElementCommand(&commands, "ip6", tableNameV6, ingressFilterIPSetV6, ingressV6)
	appendElementCommand(&commands, "ip", tableName, egressFilterIPSet, egressV4)
	appendElementCommand(&commands, "ip6", tableNameV6, egressFilterIPSetV6, egressV6)
	if len(ports) != 0 {
		items := make([]string, len(ports))
		for i, port := range ports {
			items[i] = strconv.Itoa(port)
		}
		appendElementCommand(&commands, "ip", tableName, egressFilterPortSet, items)
		appendElementCommand(&commands, "ip6", tableNameV6, egressFilterPortSetV6, items)
	}
	return strings.Join(commands, "\n")
}

func renderNFTDeleteTables() string {
	return fmt.Sprintf(`
add table ip %s
add table ip6 %s
delete table ip %s
delete table ip6 %s
`, tableName, tableNameV6, tableName, tableNameV6)
}

func renderNFTBlock(items []BlockIP) (string, error) {
	type timedAddress struct {
		address string
		timeout float64
	}
	v4 := make(map[string]timedAddress)
	v6 := make(map[string]timedAddress)
	for _, item := range items {
		addr, err := netip.ParseAddr(strings.TrimSpace(item.IP))
		if err != nil {
			return "", fmt.Errorf("invalid ip %q", item.IP)
		}
		addr = addr.Unmap()
		entry := timedAddress{address: addr.String(), timeout: item.Timeout}
		if addr.Is4() {
			v4[entry.address] = entry
		} else {
			v6[entry.address] = entry
		}
	}

	commands := make([]string, 0, 2)
	appendTimedElements := func(family, table, set string, entries map[string]timedAddress) {
		keys := make([]string, 0, len(entries))
		for key := range entries {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		elements := make([]string, 0, len(keys))
		for _, key := range keys {
			entry := entries[key]
			element := entry.address
			if entry.timeout > 0 {
				element += " timeout " + strconv.FormatFloat(entry.timeout, 'f', -1, 64) + "s"
			}
			elements = append(elements, element)
		}
		appendElementCommand(&commands, family, table, set, elements)
	}
	appendTimedElements("ip", tableName, torrentBlockerSet, v4)
	appendTimedElements("ip6", tableNameV6, torrentBlockerSetV6, v6)
	return strings.Join(commands, "\n"), nil
}

// renderNFTUnblock returns one command per set. The runner can ignore a
// missing-element error for one set while still removing the address from the
// other set, matching official removal from torrent and ingress filters.
func renderNFTUnblock(ips []string) ([]string, error) {
	v4 := make(map[string]struct{})
	v6 := make(map[string]struct{})
	for _, raw := range ips {
		addr, err := netip.ParseAddr(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("invalid ip %q", raw)
		}
		addr = addr.Unmap()
		if addr.Is4() {
			v4[addr.String()] = struct{}{}
		} else {
			v6[addr.String()] = struct{}{}
		}
	}

	commands := make([]string, 0, 4)
	appendDelete := func(family, table, set string, addresses map[string]struct{}) {
		items := make([]string, 0, len(addresses))
		for address := range addresses {
			items = append(items, address)
		}
		sort.Strings(items)
		if len(items) != 0 {
			commands = append(commands, fmt.Sprintf("delete element %s %s %s { %s }", family, table, set, strings.Join(items, ", ")))
		}
	}
	appendDelete("ip", tableName, torrentBlockerSet, v4)
	appendDelete("ip", tableName, ingressFilterIPSet, v4)
	appendDelete("ip6", tableNameV6, torrentBlockerSetV6, v6)
	appendDelete("ip6", tableNameV6, ingressFilterIPSetV6, v6)
	return commands, nil
}

func appendElementCommand(commands *[]string, family, table, set string, elements []string) {
	if len(elements) == 0 {
		return
	}
	*commands = append(*commands, fmt.Sprintf("add element %s %s %s { %s }", family, table, set, strings.Join(elements, ", ")))
}

func normalizedPorts(ports []int) []int {
	set := make(map[int]struct{}, len(ports))
	for _, port := range ports {
		if port >= 1 && port <= 65535 {
			set[port] = struct{}{}
		}
	}
	out := make([]int, 0, len(set))
	for port := range set {
		out = append(out, port)
	}
	sort.Ints(out)
	return out
}
