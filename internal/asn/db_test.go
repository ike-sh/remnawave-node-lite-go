package asn

import (
	"bytes"
	"encoding/binary"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWriteAndQuery(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "asn-prefixes.bin")

	entries := []Entry{
		{ASN: 15169, IPv4: []netip.Prefix{netip.MustParsePrefix("8.8.8.0/24")}},
		{
			ASN:  13335,
			IPv4: []netip.Prefix{netip.MustParsePrefix("1.1.1.0/24")},
			IPv6: []netip.Prefix{netip.MustParsePrefix("2606:4700::/32")},
		},
	}

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := Write(f, entries); err != nil {
		t.Fatal(err)
	}
	f.Close()

	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if !db.Available() {
		t.Fatal("expected database to be available")
	}

	v4, v6 := db.PrefixesByASN(13335)
	if !reflect.DeepEqual(v4, []string{"1.1.1.0/24"}) {
		t.Errorf("asn 13335 ipv4 = %v", v4)
	}
	if !reflect.DeepEqual(v6, []string{"2606:4700::/32"}) {
		t.Errorf("asn 13335 ipv6 = %v", v6)
	}

	v4, _ = db.PrefixesByASN(15169)
	if !reflect.DeepEqual(v4, []string{"8.8.8.0/24"}) {
		t.Errorf("asn 15169 ipv4 = %v", v4)
	}

	v4, v6 = db.PrefixesByASN(99999)
	if len(v4) != 0 || len(v6) != 0 {
		t.Errorf("unknown asn should resolve empty, got %v / %v", v4, v6)
	}
}

func TestOpenMissingFile(t *testing.T) {
	t.Parallel()
	if _, err := Open(filepath.Join(t.TempDir(), "absent.bin")); err == nil {
		t.Fatal("expected error opening a missing database")
	}
}

func TestWriteAcceptsMaximumVersionOnePrefixCount(t *testing.T) {
	t.Parallel()

	prefixes := testIPv4Prefixes(maxPrefixesPerFamily)
	var output bytes.Buffer
	if err := Write(&output, []Entry{{ASN: 64512, IPv4: prefixes}}); err != nil {
		t.Fatalf("write %d prefixes: %v", len(prefixes), err)
	}
	index := output.Bytes()[headerLen : headerLen+entryLen]
	if got := binary.LittleEndian.Uint16(index[12:14]); got != uint16(maxPrefixesPerFamily) {
		t.Fatalf("encoded prefix count = %d, want %d", got, maxPrefixesPerFamily)
	}
}

func TestWriteRejectsVersionOnePrefixCountOverflowBeforeWriting(t *testing.T) {
	t.Parallel()

	prefixes := testIPv4Prefixes(maxPrefixesPerFamily + 1)
	var output bytes.Buffer
	if err := Write(&output, []Entry{{ASN: 64512, IPv4: prefixes}}); err == nil {
		t.Fatalf("write %d prefixes succeeded; want v1 count overflow error", len(prefixes))
	}
	if output.Len() != 0 {
		t.Fatalf("overflowing database emitted %d bytes before returning error", output.Len())
	}
}

func TestWriteCanonicalizesPrefixOrderAndDuplicates(t *testing.T) {
	t.Parallel()

	prefixA := netip.MustParsePrefix("203.0.113.0/24")
	prefixB := netip.MustParsePrefix("198.51.100.0/24")
	var first bytes.Buffer
	if err := Write(&first, []Entry{{ASN: 64512, IPv4: []netip.Prefix{prefixA, prefixB, prefixA}}}); err != nil {
		t.Fatal(err)
	}
	var second bytes.Buffer
	if err := Write(&second, []Entry{{ASN: 64512, IPv4: []netip.Prefix{prefixB, prefixA}}}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("equivalent prefix sets produced different database bytes")
	}
}

func TestWriteRejectsDuplicateASNBeforeWriting(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	err := Write(&output, []Entry{{ASN: 64512}, {ASN: 64512}})
	if err == nil {
		t.Fatal("duplicate ASN entries were accepted")
	}
	if output.Len() != 0 {
		t.Fatalf("duplicate ASN database emitted %d bytes before returning error", output.Len())
	}
}

func testIPv4Prefixes(count int) []netip.Prefix {
	prefixes := make([]netip.Prefix, count)
	for index := range prefixes {
		address := [4]byte{
			byte(uint32(index) >> 24),
			byte(uint32(index) >> 16),
			byte(uint32(index) >> 8),
			byte(index),
		}
		prefixes[index] = netip.PrefixFrom(netip.AddrFrom4(address), 32)
	}
	return prefixes
}
