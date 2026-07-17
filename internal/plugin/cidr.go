package plugin

import (
	"net/netip"
	"slices"
	"strings"

	"go4.org/netipx"
)

// normalizeFilterPrefixes parses a mixed list of plain IPs and CIDRs, groups them
// by address family, and returns de-duplicated, non-overlapping CIDR strings.
//
// nftables interval sets reject overlapping or duplicate elements: a single
// conflicting pair aborts the whole `add element` batch and silently disables the
// filter. Merging through netipx yields a minimal, conflict-free element set and
// also lets us feed CIDRs that the previous plain-IP path discarded.
func normalizeFilterPrefixes(items []string) (v4, v6 []string) {
	var b4, b6 netipx.IPSetBuilder
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(item); err == nil {
			prefix = normalizeIPPrefix(prefix)
			if prefix.Addr().Is4() {
				b4.AddPrefix(prefix)
			} else {
				b6.AddPrefix(prefix)
			}
			continue
		}
		if addr, err := netip.ParseAddr(item); err == nil {
			addr = addr.Unmap()
			if addr.Is4() {
				b4.Add(addr)
			} else {
				b6.Add(addr)
			}
		}
	}
	return prefixStrings(&b4), prefixStrings(&b6)
}

func normalizeIPPrefix(prefix netip.Prefix) netip.Prefix {
	prefix = prefix.Masked()
	if prefix.Addr().Is4In6() && prefix.Bits() >= 96 {
		return netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96).Masked()
	}
	return prefix
}

type ipMatcher struct {
	prefixes []string
	set      *netipx.IPSet
}

func newIPMatcher(items []string) ipMatcher {
	v4, v6 := normalizeFilterPrefixes(items)
	prefixes := make([]string, 0, len(v4)+len(v6))
	prefixes = append(prefixes, v4...)
	prefixes = append(prefixes, v6...)
	var builder netipx.IPSetBuilder
	for _, value := range prefixes {
		prefix, err := netip.ParsePrefix(value)
		if err == nil {
			builder.AddPrefix(prefix)
		}
	}
	set, _ := builder.IPSet()
	return ipMatcher{prefixes: prefixes, set: set}
}

func (m ipMatcher) contains(raw string) bool {
	if m.set == nil {
		return false
	}
	address, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil || address.Zone() != "" {
		return false
	}
	if m.set.Contains(address) {
		return true
	}
	return address.Is4In6() && m.set.Contains(address.Unmap())
}

func (m ipMatcher) equal(other ipMatcher) bool {
	return slices.Equal(m.prefixes, other.prefixes)
}

func prefixStrings(b *netipx.IPSetBuilder) []string {
	set, err := b.IPSet()
	if err != nil || set == nil {
		return nil
	}
	prefixes := set.Prefixes()
	out := make([]string, 0, len(prefixes))
	for _, p := range prefixes {
		out = append(out, p.String())
	}
	return out
}
