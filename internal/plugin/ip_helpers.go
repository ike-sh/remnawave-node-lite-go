package plugin

import "net"

const (
	tableNameV6           = "remnanode6"
	torrentBlockerSetV6   = "torrent-blocker6"
	ingressFilterIPSetV6  = "ingress-filter-ip6"
	egressFilterIPSetV6   = "egress-filter-ip6"
	egressFilterPortSetV6 = "egress-filter-port6"
)

func ipTableAndTorrentSet(ip string) (table, set string, ok bool) {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return "", "", false
	}
	if parsed.To4() != nil {
		return tableName, torrentBlockerSet, true
	}
	return tableNameV6, torrentBlockerSetV6, true
}
