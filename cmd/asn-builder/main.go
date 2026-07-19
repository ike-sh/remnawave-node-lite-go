// Command asn-builder converts ASN prefix datasets into the compact
// asn-prefixes.bin consumed at runtime to resolve plugin `asList` shared lists.
//
// Supported inputs are the TAB-separated ip2asn "combined" format, Remnawave's
// combined JSON format, and commit-pinned ipverse/as-ip-blocks tar.gz archives.
// IP ranges are merged into minimal CIDR sets per ASN via netipx.
//
// Usage:
//
//	gunzip -c ip2asn-combined.tsv.gz | go run ./cmd/asn-builder -out asn-prefixes.bin
//	go run ./cmd/asn-builder -in ip2asn-combined.tsv -out asn-prefixes.bin
//	go run ./cmd/asn-builder -format remnawave-json -in asn-prefixes.json -out asn-prefixes.bin
//	go run ./cmd/asn-builder -format ipverse-tar-gz -in as-ip-blocks.tar.gz -out asn-prefixes.bin
package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
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

const (
	maxIPversePathBytes         = 512
	maxIPverseRecordBytes       = 1 << 20
	maxIPverseArchiveRecords    = 250_000
	maxIPverseArchivePrefixes   = 4_000_000
	maxIPversePrefixesPerFamily = 1<<16 - 1
)

func main() {
	in := flag.String("in", "", "input path (default: stdin)")
	out := flag.String("out", "asn-prefixes.bin", "output .bin path")
	format := flag.String("format", "auto", "input format: auto, ip2asn-tsv, remnawave-json, or ipverse-tar-gz")
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
		lowerPath := strings.ToLower(*in)
		if strings.HasSuffix(lowerPath, ".tar.gz") || strings.HasSuffix(lowerPath, ".tgz") {
			selectedFormat = "ipverse-tar-gz"
		} else if strings.EqualFold(filepath.Ext(*in), ".json") {
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
	case "ipverse-tar-gz":
		entries, err = parseIPverseTarGZ(reader)
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
		entry, err := parsePrefixEntry(uint32(number), record.IPv4, record.IPv6)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

type ipverseRecord struct {
	ASN      json.Number      `json:"asn"`
	Prefixes *ipversePrefixes `json:"prefixes"`
}

type ipversePrefixes struct {
	IPv4 []string `json:"ipv4"`
	IPv6 []string `json:"ipv6"`
}

func parseIPverseTarGZ(reader io.Reader) ([]asn.Entry, error) {
	compressed, err := gzip.NewReader(reader)
	if err != nil {
		return nil, fmt.Errorf("open ipverse gzip stream: %w", err)
	}
	defer compressed.Close()

	archive := tar.NewReader(compressed)
	entries := make([]asn.Entry, 0, 100_000)
	seen := make(map[uint32]string, 100_000)
	matchedRecords := 0
	totalPrefixes := 0

	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read ipverse tar archive: %w", err)
		}

		pathASN, ok := parseIPverseASNPath(header.Name)
		if !ok {
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return nil, fmt.Errorf("ipverse record %q is not a regular file", header.Name)
		}
		if header.Size > maxIPverseRecordBytes {
			return nil, fmt.Errorf("ipverse record %q is %d bytes; limit is %d", header.Name, header.Size, maxIPverseRecordBytes)
		}
		matchedRecords++
		if matchedRecords > maxIPverseArchiveRecords {
			return nil, fmt.Errorf("ipverse archive contains more than %d ASN records", maxIPverseArchiveRecords)
		}

		record, err := decodeIPverseRecord(archive)
		if err != nil {
			return nil, fmt.Errorf("decode ipverse record %q: %w", header.Name, err)
		}
		rawASN := record.ASN.String()
		recordASN, err := strconv.ParseUint(rawASN, 10, 32)
		if err != nil || recordASN == 0 {
			return nil, fmt.Errorf("ipverse record %q has invalid ASN %q", header.Name, rawASN)
		}
		if uint32(recordASN) != pathASN {
			return nil, fmt.Errorf("ipverse record %q declares AS%d, want AS%d from path", header.Name, recordASN, pathASN)
		}
		if record.Prefixes == nil {
			return nil, fmt.Errorf("ipverse record %q has no prefixes object", header.Name)
		}
		if previous, exists := seen[pathASN]; exists {
			return nil, fmt.Errorf("duplicate AS%d records: %q and %q", pathASN, previous, header.Name)
		}
		seen[pathASN] = header.Name

		ipv4Count := len(record.Prefixes.IPv4)
		ipv6Count := len(record.Prefixes.IPv6)
		if ipv4Count > maxIPversePrefixesPerFamily || ipv6Count > maxIPversePrefixesPerFamily {
			return nil, fmt.Errorf(
				"ipverse record %q exceeds %d prefixes per address family",
				header.Name,
				maxIPversePrefixesPerFamily,
			)
		}
		if ipv4Count > maxIPverseArchivePrefixes-totalPrefixes ||
			ipv6Count > maxIPverseArchivePrefixes-totalPrefixes-ipv4Count {
			return nil, fmt.Errorf("ipverse archive contains more than %d prefixes", maxIPverseArchivePrefixes)
		}
		totalPrefixes += ipv4Count + ipv6Count
		if ipv4Count == 0 && ipv6Count == 0 {
			continue
		}

		entry, err := parsePrefixEntry(pathASN, record.Prefixes.IPv4, record.Prefixes.IPv6)
		if err != nil {
			return nil, fmt.Errorf("ipverse record %q: %w", header.Name, err)
		}
		entries = append(entries, entry)
	}

	if _, err := io.Copy(io.Discard, compressed); err != nil {
		return nil, fmt.Errorf("finish ipverse gzip stream: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("ipverse archive contains no valid ASN entries")
	}
	return entries, nil
}

func parseIPverseASNPath(name string) (uint32, bool) {
	if len(name) > maxIPversePathBytes {
		return 0, false
	}
	parts := strings.Split(name, "/")
	if len(parts) != 4 || parts[0] == "" || parts[0] == "." || parts[0] == ".." ||
		parts[1] != "as" || parts[3] != "aggregated.json" {
		return 0, false
	}
	number, err := strconv.ParseUint(parts[2], 10, 32)
	if err != nil || number == 0 || strconv.FormatUint(number, 10) != parts[2] {
		return 0, false
	}
	return uint32(number), true
}

func decodeIPverseRecord(reader io.Reader) (ipverseRecord, error) {
	var record ipverseRecord
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()
	if err := decoder.Decode(&record); err != nil {
		return record, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return record, fmt.Errorf("trailing data")
		}
		return record, fmt.Errorf("trailing data: %w", err)
	}
	return record, nil
}

func parsePrefixEntry(number uint32, rawIPv4, rawIPv6 []string) (asn.Entry, error) {
	entry := asn.Entry{ASN: number}
	for _, rawPrefix := range rawIPv4 {
		prefix, err := netip.ParsePrefix(rawPrefix)
		if err != nil || !prefix.Addr().Is4() {
			return asn.Entry{}, fmt.Errorf("AS%d invalid IPv4 prefix %q", number, rawPrefix)
		}
		entry.IPv4 = append(entry.IPv4, prefix)
	}
	for _, rawPrefix := range rawIPv6 {
		prefix, err := netip.ParsePrefix(rawPrefix)
		if err != nil || prefix.Addr().Is4() {
			return asn.Entry{}, fmt.Errorf("AS%d invalid IPv6 prefix %q", number, rawPrefix)
		}
		entry.IPv6 = append(entry.IPv6, prefix)
	}
	return entry, nil
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
