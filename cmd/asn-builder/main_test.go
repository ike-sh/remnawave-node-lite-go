package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"remnawave-node-lite-go/internal/asn"
)

func TestOfficialJSONConversionRetainsIPv4IPv6(t *testing.T) {
	input := `{"13335":{"ipv4":["1.0.0.0/24","1.1.1.0/24"],"ipv6":["2606:4700::/32"]},"15169":{"ipv4":["8.8.8.0/24"],"ipv6":["2001:4860::/32"]}}`
	entries, err := parseOfficialJSON(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "asn-prefixes.bin")
	writeEntries(path, entries)
	db, err := asn.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	v4, v6 := db.PrefixesByASN(13335)
	if !reflect.DeepEqual(v4, []string{"1.0.0.0/24", "1.1.1.0/24"}) || !reflect.DeepEqual(v6, []string{"2606:4700::/32"}) {
		t.Fatalf("ASN 13335 = %v / %v", v4, v6)
	}
	v4, v6 = db.PrefixesByASN(15169)
	if !reflect.DeepEqual(v4, []string{"8.8.8.0/24"}) || !reflect.DeepEqual(v6, []string{"2001:4860::/32"}) {
		t.Fatalf("ASN 15169 = %v / %v", v4, v6)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestOfficialJSONRejectsBadASNAndPrefix(t *testing.T) {
	for _, input := range []string{`{"0":{"ipv4":[],"ipv6":[]}}`, `{"1":{"ipv4":["bad"],"ipv6":[]}}`, `{"1":{"ipv4":["2001:db8::/32"],"ipv6":[]}}`, `[]`} {
		if _, err := parseOfficialJSON(strings.NewReader(input)); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
}
