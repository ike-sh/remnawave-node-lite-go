package asn

import (
	"encoding/binary"
	"io"
	"net/netip"
	"sort"
)

// Entry holds one ASN's prefixes for serialization.
type Entry struct {
	ASN  uint32
	IPv4 []netip.Prefix
	IPv6 []netip.Prefix
}

// Write serializes entries into the DB binary format consumed by Open/DB.
// Entries are sorted by ASN so the reader can binary-search the index.
func Write(w io.Writer, entries []Entry) error {
	sorted := append([]Entry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ASN < sorted[j].ASN })

	count := uint32(len(sorted))
	header := make([]byte, headerLen)
	copy(header[:8], magic)
	binary.LittleEndian.PutUint32(header[8:12], count)
	if _, err := w.Write(header); err != nil {
		return err
	}

	dataOff := uint64(headerLen) + uint64(count)*entryLen
	index := make([]byte, 0, int(count)*entryLen)
	var data []byte

	for _, e := range sorted {
		v4 := normalizePrefixes(e.IPv4, true)
		v6 := normalizePrefixes(e.IPv6, false)

		entry := make([]byte, entryLen)
		binary.LittleEndian.PutUint32(entry[0:4], e.ASN)
		binary.LittleEndian.PutUint64(entry[4:12], dataOff)
		binary.LittleEndian.PutUint16(entry[12:14], uint16(len(v4)))
		binary.LittleEndian.PutUint16(entry[14:16], uint16(len(v6)))
		index = append(index, entry...)

		for _, p := range v4 {
			a := p.Addr().As4()
			data = append(data, a[:]...)
			data = append(data, byte(p.Bits()))
		}
		for _, p := range v6 {
			a := p.Addr().As16()
			data = append(data, a[:]...)
			data = append(data, byte(p.Bits()))
		}
		dataOff += uint64(len(v4)*v4Record + len(v6)*v6Record)
	}

	if _, err := w.Write(index); err != nil {
		return err
	}
	_, err := w.Write(data)
	return err
}

func normalizePrefixes(prefixes []netip.Prefix, wantV4 bool) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(prefixes))
	for _, p := range prefixes {
		if !p.IsValid() {
			continue
		}
		p = p.Masked()
		if p.Addr().Is4() != wantV4 {
			continue
		}
		out = append(out, p)
	}
	return out
}
