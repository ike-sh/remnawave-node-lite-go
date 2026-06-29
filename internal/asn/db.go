// Package asn provides read-only lookups from an ASN→prefixes database file.
//
// It mirrors upstream remnawave/node 2.8.0, which resolves plugin `asList`
// shared lists (AS numbers) into IPv4/IPv6 CIDR prefixes via an on-disk LMDB.
// To keep this node a single CGO-free binary suited to low-memory VPSes, we use
// a compact, sorted binary format queried with ReadAt + binary search instead of
// loading the whole dataset into memory. A missing/invalid file degrades
// gracefully: Available() reports false and lookups return empty, matching
// upstream's "ASN not found → skip" behaviour.
package asn

import (
	"encoding/binary"
	"fmt"
	"io"
	"net/netip"
	"os"
)

const (
	// magic identifies the file format; the trailing byte is the format version.
	magic = "RWASNDB\x01"

	headerLen = 16
	entryLen  = 16 // asn(4) + dataOff(8) + v4Count(2) + v6Count(2)
	v4Record  = 5  // 4-byte address + 1-byte prefix length
	v6Record  = 17 // 16-byte address + 1-byte prefix length
)

// DB is a read-only handle to an ASN prefixes database.
type DB struct {
	f     *os.File
	count uint32
}

// Open opens the database at path. The file is kept open for ReadAt lookups.
func Open(path string) (*DB, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	hdr := make([]byte, headerLen)
	if _, err := io.ReadFull(f, hdr); err != nil {
		f.Close()
		return nil, fmt.Errorf("read header: %w", err)
	}
	if string(hdr[:8]) != magic {
		f.Close()
		return nil, fmt.Errorf("invalid asn database magic")
	}
	return &DB{f: f, count: binary.LittleEndian.Uint32(hdr[8:12])}, nil
}

// Close releases the underlying file handle.
func (d *DB) Close() error {
	if d == nil || d.f == nil {
		return nil
	}
	return d.f.Close()
}

// Available reports whether the database holds at least one entry.
func (d *DB) Available() bool {
	return d != nil && d.f != nil && d.count > 0
}

// PrefixesByASN returns the IPv4 and IPv6 CIDR strings for asn. Unknown ASNs and
// any read error yield empty slices so callers can treat lookups as best-effort.
func (d *DB) PrefixesByASN(asn uint32) (ipv4, ipv6 []string) {
	if !d.Available() {
		return nil, nil
	}
	entry := make([]byte, entryLen)
	lo, hi := uint32(0), d.count
	for lo < hi {
		mid := lo + (hi-lo)/2
		off := int64(headerLen) + int64(mid)*entryLen
		if _, err := d.f.ReadAt(entry, off); err != nil {
			return nil, nil
		}
		cur := binary.LittleEndian.Uint32(entry[0:4])
		switch {
		case cur < asn:
			lo = mid + 1
		case cur > asn:
			hi = mid
		default:
			dataOff := binary.LittleEndian.Uint64(entry[4:12])
			v4n := int(binary.LittleEndian.Uint16(entry[12:14]))
			v6n := int(binary.LittleEndian.Uint16(entry[14:16]))
			return d.readBlob(int64(dataOff), v4n, v6n)
		}
	}
	return nil, nil
}

func (d *DB) readBlob(off int64, v4n, v6n int) (ipv4, ipv6 []string) {
	buf := make([]byte, v4n*v4Record+v6n*v6Record)
	if _, err := d.f.ReadAt(buf, off); err != nil {
		return nil, nil
	}
	pos := 0
	ipv4 = make([]string, 0, v4n)
	for i := 0; i < v4n; i++ {
		var a [4]byte
		copy(a[:], buf[pos:pos+4])
		bits := int(buf[pos+4])
		pos += v4Record
		ipv4 = append(ipv4, netip.PrefixFrom(netip.AddrFrom4(a), bits).String())
	}
	ipv6 = make([]string, 0, v6n)
	for i := 0; i < v6n; i++ {
		var a [16]byte
		copy(a[:], buf[pos:pos+16])
		bits := int(buf[pos+16])
		pos += v6Record
		ipv6 = append(ipv6, netip.PrefixFrom(netip.AddrFrom16(a), bits).String())
	}
	return ipv4, ipv6
}
