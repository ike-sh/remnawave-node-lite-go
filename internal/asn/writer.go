package asn

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net/netip"
	"sort"
)

const (
	maxPrefixesPerFamily = 1<<16 - 1
	writerBufferSize     = 64 << 10
)

// Entry holds one ASN's prefixes for serialization.
type Entry struct {
	ASN  uint32
	IPv4 []netip.Prefix
	IPv6 []netip.Prefix
}

// Write serializes entries into the version-1 DB binary format consumed by
// Open/DB. Entries are sorted by ASN so the reader can binary-search the index.
// Version 1 stores each address-family count as uint16, so Write rejects more
// than 65,535 IPv4 or IPv6 prefixes for one ASN before emitting any bytes.
func Write(w io.Writer, entries []Entry) error {
	if uint64(len(entries)) > uint64(^uint32(0)) {
		return fmt.Errorf("ASN database v1 supports at most %d entries", uint64(^uint32(0)))
	}
	sorted := append([]Entry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ASN < sorted[j].ASN })
	prepared := make([]Entry, len(sorted))
	for index, entry := range sorted {
		if index > 0 && sorted[index-1].ASN == entry.ASN {
			return fmt.Errorf("duplicate ASN entry: AS%d", entry.ASN)
		}
		entry.IPv4 = normalizePrefixes(entry.IPv4, true)
		entry.IPv6 = normalizePrefixes(entry.IPv6, false)
		if len(entry.IPv4) > maxPrefixesPerFamily {
			return fmt.Errorf("AS%d has %d IPv4 prefixes; ASN database v1 supports at most %d", entry.ASN, len(entry.IPv4), maxPrefixesPerFamily)
		}
		if len(entry.IPv6) > maxPrefixesPerFamily {
			return fmt.Errorf("AS%d has %d IPv6 prefixes; ASN database v1 supports at most %d", entry.ASN, len(entry.IPv6), maxPrefixesPerFamily)
		}
		prepared[index] = entry
	}

	count := uint32(len(prepared))
	buffered := bufio.NewWriterSize(w, writerBufferSize)
	header := make([]byte, headerLen)
	copy(header[:8], magic)
	binary.LittleEndian.PutUint32(header[8:12], count)
	if err := writeAll(buffered, header); err != nil {
		return err
	}

	dataOff := uint64(headerLen) + uint64(count)*entryLen
	index := make([]byte, 0, int(count)*entryLen)

	for _, e := range prepared {
		entry := make([]byte, entryLen)
		binary.LittleEndian.PutUint32(entry[0:4], e.ASN)
		binary.LittleEndian.PutUint64(entry[4:12], dataOff)
		binary.LittleEndian.PutUint16(entry[12:14], uint16(len(e.IPv4)))
		binary.LittleEndian.PutUint16(entry[14:16], uint16(len(e.IPv6)))
		index = append(index, entry...)
		dataOff += uint64(len(e.IPv4)*v4Record + len(e.IPv6)*v6Record)
	}

	if err := writeAll(buffered, index); err != nil {
		return err
	}
	var record [v6Record]byte
	for _, entry := range prepared {
		for _, prefix := range entry.IPv4 {
			address := prefix.Addr().As4()
			copy(record[:4], address[:])
			record[4] = byte(prefix.Bits())
			if err := writeAll(buffered, record[:v4Record]); err != nil {
				return err
			}
		}
		for _, prefix := range entry.IPv6 {
			address := prefix.Addr().As16()
			copy(record[:16], address[:])
			record[16] = byte(prefix.Bits())
			if err := writeAll(buffered, record[:v6Record]); err != nil {
				return err
			}
		}
	}
	return buffered.Flush()
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) != 0 {
		written, err := w.Write(data)
		if written < 0 || written > len(data) {
			return io.ErrShortWrite
		}
		if written > 0 {
			data = data[written:]
		}
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
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
	sort.Slice(out, func(i, j int) bool {
		if comparison := out[i].Addr().Compare(out[j].Addr()); comparison != 0 {
			return comparison < 0
		}
		return out[i].Bits() < out[j].Bits()
	})
	unique := out[:0]
	for _, prefix := range out {
		if len(unique) == 0 || prefix != unique[len(unique)-1] {
			unique = append(unique, prefix)
		}
	}
	return unique
}
