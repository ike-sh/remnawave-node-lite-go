// Command asn-builder converts an ip2asn dataset into the compact asn-prefixes.bin
// consumed at runtime to resolve plugin `asList` shared lists.
//
// Input can be the official remnawave/asn-index JSON (ASN keys, IPv4/IPv6
// prefix arrays) or the TAB-separated ip2asn "combined" format. TSV IP ranges
// are merged into minimal CIDR sets per ASN via netipx.
//
// Usage:
//
//	gunzip -c ip2asn-combined.tsv.gz | go run ./cmd/asn-builder -out asn-prefixes.bin
//	go run ./cmd/asn-builder -in ip2asn-combined.tsv -out asn-prefixes.bin
//	go run ./cmd/asn-builder -format json -in asn-prefixes.json -out asn-prefixes.bin
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
	"strconv"
	"strings"

	"go4.org/netipx"

	"remnawave-node-lite-go/internal/asn"
)

func main() {
	in := flag.String("in", "", "input path (default: stdin)")
	out := flag.String("out", "asn-prefixes.bin", "output .bin path")
	format := flag.String("format", "tsv", "input format: tsv or json (official asn-index JSON)")
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
	if *format == "json" {
		entries, err := parseOfficialJSON(reader)
		if err != nil {
			log.Fatalf("parse official ASN JSON: %v", err)
		}
		writeEntries(*out, entries)
		return
	}
	if *format != "tsv" {
		log.Fatalf("unsupported input format %q", *format)
	}

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
		log.Fatalf("read input at line %d: %v", line, err)
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

	writeEntries(*out, entries)
}

func writeEntries(path string, entries []asn.Entry) {
	f, err := os.Create(path)
	if err != nil {
		log.Fatalf("create output: %v", err)
	}
	defer f.Close()
	if err := asn.Write(f, entries); err != nil {
		log.Fatalf("write database: %v", err)
	}
	fmt.Printf("wrote %d ASN entries to %s\n", len(entries), path)
}

// parseOfficialJSON reads the same ASN→IPv4/IPv6 records as the official LMDB
// asset, one JSON member at a time so installation-time conversion stays small.
func parseOfficialJSON(reader io.Reader) ([]asn.Entry, error) {
	decoder := json.NewDecoder(reader)
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("expected top-level object: %v", err)
	}
	var entries []asn.Entry
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		asnNumber, err := strconv.ParseUint(key.(string), 10, 32)
		if err != nil || asnNumber == 0 {
			return nil, fmt.Errorf("invalid ASN key %q", key)
		}
		var value struct {
			IPv4 []string `json:"ipv4"`
			IPv6 []string `json:"ipv6"`
		}
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		entry := asn.Entry{ASN: uint32(asnNumber)}
		for _, raw := range value.IPv4 {
			prefix, err := netip.ParsePrefix(raw)
			if err != nil || !prefix.Addr().Is4() {
				return nil, fmt.Errorf("ASN %d invalid IPv4 prefix %q", asnNumber, raw)
			}
			entry.IPv4 = append(entry.IPv4, prefix.Masked())
		}
		for _, raw := range value.IPv6 {
			prefix, err := netip.ParsePrefix(raw)
			if err != nil || !prefix.Addr().Is6() {
				return nil, fmt.Errorf("ASN %d invalid IPv6 prefix %q", asnNumber, raw)
			}
			entry.IPv6 = append(entry.IPv6, prefix.Masked())
		}
		entries = append(entries, entry)
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	return entries, nil
}
