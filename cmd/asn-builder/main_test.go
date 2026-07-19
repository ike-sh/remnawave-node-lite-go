package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
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

func TestParseIPverseTarGZ(t *testing.T) {
	archive := makeIPverseArchive(t,
		testArchiveEntry{name: "as-ip-blocks-commit/README.md", body: "not JSON"},
		testArchiveEntry{name: "as-ip-blocks-commit/as/013335/aggregated.json", body: "not JSON"},
		testArchiveEntry{
			name: "as-ip-blocks-commit/as/13335/aggregated.json",
			body: `{
  "asn": 13335,
  "description": "ignored metadata",
  "prefixes": {"ipv4":["1.1.1.0/24"],"ipv6":["2606:4700::/32"]}
}`,
		},
		testArchiveEntry{
			name: "as-ip-blocks-commit/as/15169/aggregated.json",
			body: `{"asn":15169,"prefixes":{"ipv4":[],"ipv6":[]}}`,
		},
		testArchiveEntry{
			name: "as-ip-blocks-commit/as/16509/aggregated.json",
			body: `{"asn":16509,"prefixes":{"ipv4":["3.0.0.0/9"],"ipv6":[]}}`,
		},
	)

	entries, err := parseIPverseTarGZ(bytes.NewReader(archive))
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
	if !reflect.DeepEqual(v4, []string{"1.1.1.0/24"}) ||
		!reflect.DeepEqual(v6, []string{"2606:4700::/32"}) {
		t.Fatalf("AS13335 prefixes = %v / %v", v4, v6)
	}
	v4, v6 = db.PrefixesByASN(15169)
	if len(v4) != 0 || len(v6) != 0 {
		t.Fatalf("empty AS15169 record was retained: %v / %v", v4, v6)
	}
}

func TestParseIPverseASNPath(t *testing.T) {
	for _, test := range []struct {
		name string
		want uint32
		ok   bool
	}{
		{name: "snapshot/as/1/aggregated.json", want: 1, ok: true},
		{name: "snapshot/as/4294967295/aggregated.json", want: 4294967295, ok: true},
		{name: "as/1/aggregated.json"},
		{name: "/snapshot/as/1/aggregated.json"},
		{name: "./snapshot/as/1/aggregated.json"},
		{name: "snapshot/as/0/aggregated.json"},
		{name: "snapshot/as/01/aggregated.json"},
		{name: "snapshot/as/+1/aggregated.json"},
		{name: "snapshot/as/4294967296/aggregated.json"},
		{name: "snapshot/as/not-a-number/aggregated.json"},
		{name: strings.Repeat("r", maxIPversePathBytes+1) + "/as/1/aggregated.json"},
		{name: "snapshot/as/1/other.json"},
		{name: "snapshot/other/1/aggregated.json"},
		{name: "snapshot/as/1/extra/aggregated.json"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, ok := parseIPverseASNPath(test.name)
			if got != test.want || ok != test.ok {
				t.Fatalf("parseIPverseASNPath(%q) = %d, %t; want %d, %t", test.name, got, ok, test.want, test.ok)
			}
		})
	}
}

func TestParseIPverseTarGZRejectsInvalidRecords(t *testing.T) {
	valid := `{"asn":13335,"prefixes":{"ipv4":["1.1.1.0/24"],"ipv6":[]}}`
	for _, test := range []struct {
		name    string
		entries []testArchiveEntry
		want    string
	}{
		{
			name: "mismatched ASN",
			entries: []testArchiveEntry{{
				name: "snapshot/as/13335/aggregated.json",
				body: `{"asn":15169,"prefixes":{"ipv4":[],"ipv6":[]}}`,
			}},
			want: "declares AS15169, want AS13335",
		},
		{
			name: "missing ASN",
			entries: []testArchiveEntry{{
				name: "snapshot/as/13335/aggregated.json",
				body: `{"prefixes":{"ipv4":[],"ipv6":[]}}`,
			}},
			want: "invalid ASN",
		},
		{
			name: "non-integer ASN",
			entries: []testArchiveEntry{{
				name: "snapshot/as/13335/aggregated.json",
				body: `{"asn":13335.5,"prefixes":{"ipv4":[],"ipv6":[]}}`,
			}},
			want: "invalid ASN",
		},
		{
			name: "missing prefixes",
			entries: []testArchiveEntry{{
				name: "snapshot/as/13335/aggregated.json",
				body: `{"asn":13335}`,
			}},
			want: "no prefixes object",
		},
		{
			name: "malformed JSON",
			entries: []testArchiveEntry{{
				name: "snapshot/as/13335/aggregated.json",
				body: `{"asn":`,
			}},
			want: "decode ipverse record",
		},
		{
			name: "trailing JSON",
			entries: []testArchiveEntry{{
				name: "snapshot/as/13335/aggregated.json",
				body: valid + ` {}`,
			}},
			want: "trailing data",
		},
		{
			name: "invalid IPv4 prefix",
			entries: []testArchiveEntry{{
				name: "snapshot/as/13335/aggregated.json",
				body: `{"asn":13335,"prefixes":{"ipv4":["2606:4700::/32"],"ipv6":[]}}`,
			}},
			want: "invalid IPv4 prefix",
		},
		{
			name: "invalid IPv6 prefix",
			entries: []testArchiveEntry{{
				name: "snapshot/as/13335/aggregated.json",
				body: `{"asn":13335,"prefixes":{"ipv4":[],"ipv6":["1.1.1.0/24"]}}`,
			}},
			want: "invalid IPv6 prefix",
		},
		{
			name: "duplicate ASN",
			entries: []testArchiveEntry{
				{name: "snapshot/as/13335/aggregated.json", body: valid},
				{name: "other-snapshot/as/13335/aggregated.json", body: valid},
			},
			want: "duplicate AS13335 records",
		},
		{
			name: "non-regular record",
			entries: []testArchiveEntry{{
				name:     "snapshot/as/13335/aggregated.json",
				typeflag: tar.TypeDir,
			}},
			want: "is not a regular file",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			archive := makeIPverseArchive(t, test.entries...)
			_, err := parseIPverseTarGZ(bytes.NewReader(archive))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestParseIPverseTarGZRejectsEmptyAndOversizedArchives(t *testing.T) {
	for _, test := range []struct {
		name    string
		entries []testArchiveEntry
		want    string
	}{
		{name: "empty", want: "no valid ASN entries"},
		{
			name: "ignored entries",
			entries: []testArchiveEntry{{
				name: "snapshot/as/013335/aggregated.json",
				body: "malformed but ignored",
			}},
			want: "no valid ASN entries",
		},
		{
			name: "empty prefix record",
			entries: []testArchiveEntry{{
				name: "snapshot/as/13335/aggregated.json",
				body: `{"asn":13335,"prefixes":{"ipv4":[],"ipv6":[]}}`,
			}},
			want: "no valid ASN entries",
		},
		{
			name: "oversized record",
			entries: []testArchiveEntry{{
				name: "snapshot/as/13335/aggregated.json",
				body: strings.Repeat(" ", maxIPverseRecordBytes+1),
			}},
			want: "limit is",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			archive := makeIPverseArchive(t, test.entries...)
			_, err := parseIPverseTarGZ(bytes.NewReader(archive))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestParseIPverseTarGZRejectsContainerErrors(t *testing.T) {
	validArchive := makeIPverseArchive(t, testArchiveEntry{
		name: "snapshot/as/13335/aggregated.json",
		body: `{"asn":13335,"prefixes":{"ipv4":["1.1.1.0/24"],"ipv6":[]}}`,
	})
	corruptChecksum := append([]byte(nil), validArchive...)
	corruptChecksum[len(corruptChecksum)-8] ^= 0xff

	for _, test := range []struct {
		name  string
		input []byte
		want  string
	}{
		{name: "invalid gzip", input: []byte("not gzip"), want: "open ipverse gzip stream"},
		{name: "invalid tar", input: gzipPayload(t, []byte("not tar")), want: "read ipverse tar archive"},
		{name: "invalid gzip checksum", input: corruptChecksum, want: "gzip"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseIPverseTarGZ(bytes.NewReader(test.input))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

type testArchiveEntry struct {
	name     string
	body     string
	typeflag byte
}

func makeIPverseArchive(t *testing.T, entries ...testArchiveEntry) []byte {
	t.Helper()
	var output bytes.Buffer
	compressed := gzip.NewWriter(&output)
	archive := tar.NewWriter(compressed)
	for _, entry := range entries {
		typeflag := entry.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		header := &tar.Header{
			Name:     entry.name,
			Mode:     0o644,
			Size:     int64(len(entry.body)),
			Typeflag: typeflag,
		}
		if err := archive.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func gzipPayload(t *testing.T, payload []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	compressed := gzip.NewWriter(&output)
	if _, err := compressed.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
