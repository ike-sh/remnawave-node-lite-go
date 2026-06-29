package asn

import (
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
