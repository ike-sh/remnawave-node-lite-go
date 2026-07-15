package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/asn"
)

func TestParseRemnawaveJSON(t *testing.T) {
	entries, err := parseRemnawaveJSON(strings.NewReader(`{
  "13335": {"ipv4":["1.1.1.0/24"],"ipv6":["2606:4700::/32"]},
  "15169": {"ipv4":["8.8.8.0/24"],"ipv6":[]}
}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}

	path := filepath.Join(t.TempDir(), "asn-prefixes.bin")
	if err := writeDatabase(path, entries); err != nil {
		t.Fatal(err)
	}
	db, err := asn.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	v4, v6 := db.PrefixesByASN(13335)
	if !reflect.DeepEqual(v4, []string{"1.1.1.0/24"}) {
		t.Fatalf("AS13335 IPv4 = %v", v4)
	}
	if !reflect.DeepEqual(v6, []string{"2606:4700::/32"}) {
		t.Fatalf("AS13335 IPv6 = %v", v6)
	}
}

func TestParseRemnawaveJSONRejectsInvalidData(t *testing.T) {
	for _, input := range []string{
		`{"invalid":{"ipv4":[],"ipv6":[]}}`,
		`{"13335":{"ipv4":["not-a-prefix"],"ipv6":[]}}`,
		`{"13335":{"ipv4":["2606:4700::/32"],"ipv6":[]}}`,
		`{"13335":{"ipv4":[],"ipv6":["1.1.1.0/24"]}}`,
		`{"13335":{"ipv4":[],"ipv6":[]}} {"15169":{"ipv4":[],"ipv6":[]}}`,
	} {
		if _, err := parseRemnawaveJSON(strings.NewReader(input)); err == nil {
			t.Fatalf("parse succeeded for %s", input)
		}
	}
}
