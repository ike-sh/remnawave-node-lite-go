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
package main

import (
	"bufio"
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
	in := flag.String("in", "", "input ip2asn TSV path (default: stdin)")
	out := flag.String("out", "asn-prefixes.bin", "output .bin path")
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

	f, err := os.Create(*out)
	if err != nil {
		log.Fatalf("create output: %v", err)
	}
	defer f.Close()
	if err := asn.Write(f, entries); err != nil {
		log.Fatalf("write database: %v", err)
	}
	fmt.Printf("wrote %d ASN entries to %s\n", len(entries), *out)
}
