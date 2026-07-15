// Command asn-builder converts an ip2asn dataset into the compact asn-prefixes.bin
// consumed at runtime to resolve plugin `asList` shared lists.
//
// Input is the TAB-separated ip2asn "combined" format from https://iptoasn.com/
// (range_start, range_end, AS_number, country_code, AS_description). IP ranges
// are merged into minimal CIDR sets per ASN via netipx.
//
// Usage:
//
//	gunzip -c ip2asn-combined.tsv.gz | go run ./cmd/asn-builder -out asn-prefixes.bin
//	go run ./cmd/asn-builder -in ip2asn-combined.tsv -out asn-prefixes.bin
//	go run ./cmd/asn-builder -format remnawave-json -in asn-prefixes.json -out asn-prefixes.bin
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"go4.org/netipx"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/asn"
)

func main() {
	in := flag.String("in", "", "input ip2asn TSV path (default: stdin)")
	out := flag.String("out", "asn-prefixes.bin", "output .bin path")
	format := flag.String("format", "auto", "input format: auto, ip2asn-tsv, or remnawave-json")
	flag.Parse()

	reader := io.Reader(os.Stdin)
	if *in != "" {
		f, err := os.Open(*in)
		if err != nil {
			log.Fatalf("open input: %v", err)
		}
		defer f.Close()
		reader = f
	}

	selectedFormat := *format
	if selectedFormat == "auto" {
		if strings.EqualFold(filepath.Ext(*in), ".json") {
			selectedFormat = "remnawave-json"
		} else {
			selectedFormat = "ip2asn-tsv"
		}
	}

	var entries []asn.Entry
	var err error
	switch selectedFormat {
	case "ip2asn-tsv":
		entries, err = parseIP2ASN(reader)
	case "remnawave-json":
		entries, err = parseRemnawaveJSON(reader)
	default:
		err = fmt.Errorf("unsupported input format %q", selectedFormat)
	}
	if err != nil {
		log.Fatal(err)
	}
	if err := writeDatabase(*out, entries); err != nil {
		log.Fatalf("write database: %v", err)
	}
	fmt.Printf("wrote %d ASN entries to %s\n", len(entries), *out)
}

func parseIP2ASN(reader io.Reader) ([]asn.Entry, error) {
	builders := map[uint32]*netipx.IPSetBuilder{}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) < 3 {
			continue
		}
		asn64, err := strconv.ParseUint(strings.TrimSpace(fields[2]), 10, 32)
		if err != nil || asn64 == 0 {
			continue
		}
		start, err1 := netip.ParseAddr(strings.TrimSpace(fields[0]))
		end, err2 := netip.ParseAddr(strings.TrimSpace(fields[1]))
		if err1 != nil || err2 != nil {
			continue
		}
		r := netipx.IPRangeFrom(start, end)
		if !r.IsValid() {
			continue
		}
		asn := uint32(asn64)
		b := builders[asn]
		if b == nil {
			b = &netipx.IPSetBuilder{}
			builders[asn] = b
		}
		for _, p := range r.Prefixes() {
			b.AddPrefix(p)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read input at line %d: %w", line, err)
	}

	entries := make([]asn.Entry, 0, len(builders))
	for number, b := range builders {
		set, err := b.IPSet()
		if err != nil {
			continue
		}
		entry := asn.Entry{ASN: number}
		for _, p := range set.Prefixes() {
			if p.Addr().Is4() {
				entry.IPv4 = append(entry.IPv4, p)
			} else {
				entry.IPv6 = append(entry.IPv6, p)
			}
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

func parseRemnawaveJSON(reader io.Reader) ([]asn.Entry, error) {
	var records map[string]struct {
		IPv4 []string `json:"ipv4"`
		IPv6 []string `json:"ipv6"`
	}
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(&records); err != nil {
		return nil, fmt.Errorf("decode remnawave ASN JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("decode remnawave ASN JSON: trailing data")
		}
		return nil, fmt.Errorf("decode remnawave ASN JSON trailing data: %w", err)
	}

	entries := make([]asn.Entry, 0, len(records))
	for rawASN, record := range records {
		number, err := strconv.ParseUint(rawASN, 10, 32)
		if err != nil || number == 0 {
			return nil, fmt.Errorf("invalid ASN key %q", rawASN)
		}
		entry := asn.Entry{ASN: uint32(number)}
		for _, rawPrefix := range record.IPv4 {
			prefix, err := netip.ParsePrefix(rawPrefix)
			if err != nil || !prefix.Addr().Is4() {
				return nil, fmt.Errorf("AS%d invalid IPv4 prefix %q", number, rawPrefix)
			}
			entry.IPv4 = append(entry.IPv4, prefix)
		}
		for _, rawPrefix := range record.IPv6 {
			prefix, err := netip.ParsePrefix(rawPrefix)
			if err != nil || prefix.Addr().Is4() {
				return nil, fmt.Errorf("AS%d invalid IPv6 prefix %q", number, rawPrefix)
			}
			entry.IPv6 = append(entry.IPv6, prefix)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func writeDatabase(path string, entries []asn.Entry) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".asn-prefixes-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := asn.Write(f, entries); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
