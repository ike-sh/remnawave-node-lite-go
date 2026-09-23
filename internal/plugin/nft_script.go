package plugin

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// buildNFTScript recreates both address families in one nft transaction. The
// official 3.4.1 settings default to logged ingress drops and stateless rules.
func buildNFTScript(options NFTOptions) string {
	type family struct{ name, table, addr, torrent, ingress, egress, port string }
	families := []family{
		{"ip", tableName, "ip", torrentBlockerSet, ingressFilterIPSet, egressFilterIPSet, egressFilterPortSet},
		{"ip6", tableNameV6, "ip6", torrentBlockerSetV6, ingressFilterIPSetV6, egressFilterIPSetV6, egressFilterPortSetV6},
	}
	var b strings.Builder
	for _, f := range families {
		fmt.Fprintf(&b, "add table %s %s\ndelete table %s %s\n", f.name, f.table, f.name, f.table)
	}
	for _, f := range families {
		addrType := "ipv4_addr"
		if f.name == "ip6" {
			addrType = "ipv6_addr"
		}
		fmt.Fprintf(&b, "table %s %s {\n", f.name, f.table)
		fmt.Fprintf(&b, "\tset %s { type %s; flags timeout; }\n", f.torrent, addrType)
		for _, set := range []string{f.ingress, f.egress} {
			fmt.Fprintf(&b, "\tset %s { type %s; flags interval; }\n", set, addrType)
		}
		fmt.Fprintf(&b, "\tset %s { type inet_service; }\n", f.port)
		for _, chain := range []string{"input", "forward"} {
			fmt.Fprintf(&b, "\tchain %s {\n\t\ttype filter hook %s priority -10; policy accept;\n", chain, chain)
			if options.AcceptReplyTraffic {
				b.WriteString("\t\tct direction reply accept\n")
			}
			for _, set := range []string{f.ingress, f.torrent} {
				fmt.Fprintf(&b, "\t\t%s saddr @%s ", f.addr, set)
				if options.Logging {
					fmt.Fprintf(&b, "log prefix \"%s: \" ", set)
				}
				b.WriteString("drop\n")
			}
			b.WriteString("\t}\n")
		}
		fmt.Fprintf(&b, "\tchain output {\n\t\ttype filter hook output priority -10; policy accept;\n\t\t%s daddr @%s drop\n\t\ttcp dport @%s drop\n\t\tudp dport @%s drop\n\t}\n", f.addr, f.egress, f.port, f.port)
		b.WriteString("}\n")
	}
	return b.String()
}

func formatPortElements(ports []int) string {
	unique := make(map[int]struct{}, len(ports))
	for _, port := range ports {
		unique[port] = struct{}{}
	}
	sorted := make([]int, 0, len(unique))
	for port := range unique {
		sorted = append(sorted, port)
	}
	sort.Ints(sorted)
	items := make([]string, 0, len(sorted))
	for _, port := range sorted {
		items = append(items, strconv.Itoa(port))
	}
	return strings.Join(items, ", ")
}
