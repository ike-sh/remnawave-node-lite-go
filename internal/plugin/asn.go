package plugin

import (
	"strconv"
	"strings"
)

// ASNResolver resolves an AS number into its IPv4 and IPv6 CIDR prefixes.
// It is implemented by internal/asn.DB and is nil when no ASN database is
// available, in which case asList shared lists resolve to empty (degraded),
// matching upstream's "ASN not found → skip" behaviour.
type ASNResolver interface {
	PrefixesByASN(asn uint32) (ipv4, ipv6 []string)
}

func toASNSlice(value any) []uint32 {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]uint32, 0, len(items))
	for _, item := range items {
		if asn, ok := parseASN(item); ok {
			out = append(out, asn)
		}
	}
	return out
}

func parseASN(item any) (uint32, bool) {
	switch v := item.(type) {
	case float64:
		if v > 0 && v <= 4294967295 && v == float64(uint64(v)) {
			return uint32(v), true
		}
	case int:
		if v > 0 {
			return uint32(v), true
		}
	case int64:
		if v > 0 && v <= 4294967295 {
			return uint32(v), true
		}
	case string:
		s := strings.TrimSpace(v)
		s = strings.TrimPrefix(s, "AS")
		s = strings.TrimPrefix(s, "as")
		if n, err := strconv.ParseUint(s, 10, 32); err == nil && n > 0 {
			return uint32(n), true
		}
	}
	return 0, false
}
