package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/contract"
)

const tokenEnvironment = "REMNANODE_CONTRACT_TOKEN"

type targetFlags []contract.ProbeTarget

func (targets *targetFlags) String() string {
	values := make([]string, 0, len(*targets))
	for _, target := range *targets {
		values = append(values, target.Name+"="+target.BaseURL)
	}
	return strings.Join(values, ",")
}

func (targets *targetFlags) Set(value string) error {
	name, baseURL, ok := strings.Cut(value, "=")
	name = strings.TrimSpace(name)
	baseURL = strings.TrimSpace(baseURL)
	if !ok || name == "" || baseURL == "" {
		return errors.New("target must be name=https://host:port")
	}
	for _, target := range *targets {
		if target.Name == name {
			return fmt.Errorf("duplicate target name %q", name)
		}
	}
	if err := validateTargetURL(baseURL); err != nil {
		return err
	}
	*targets = append(*targets, contract.ProbeTarget{Name: name, BaseURL: baseURL})
	return nil
}

type probeTargetReport struct {
	Name    string                 `json:"name"`
	URL     string                 `json:"url"`
	Results []contract.ProbeResult `json:"results"`
}

type probeComparison struct {
	Baseline    string                     `json:"baseline"`
	Candidate   string                     `json:"candidate"`
	Differences []contract.ProbeDifference `json:"differences"`
}

type probeReport struct {
	ContractVersion string              `json:"contractVersion"`
	ContractCommit  string              `json:"contractCommit"`
	GeneratedAt     time.Time           `json:"generatedAt"`
	Routes          []string            `json:"routes"`
	Targets         []probeTargetReport `json:"targets"`
	Comparisons     []probeComparison   `json:"comparisons,omitempty"`
}

type routeListing struct {
	ID            string `json:"id"`
	Method        string `json:"method"`
	Path          string `json:"path"`
	SafeByDefault bool   `json:"safeByDefault"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("contract-probe", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var targets targetFlags
	var (
		routeSelection   string
		allowMutating    bool
		listRoutes       bool
		caPath           string
		certPath         string
		keyPath          string
		tokenFile        string
		serverName       string
		overallTimeout   time.Duration
		requestTimeout   time.Duration
		maxResponseBytes int64
		pretty           bool
	)
	flags.Var(&targets, "target", "probe target, repeat as name=https://host:port; first target is the baseline")
	flags.StringVar(&routeSelection, "routes", "", "comma-separated route IDs; empty selects the safe profile, 'all' selects every route")
	flags.BoolVar(&allowMutating, "allow-mutating", false, "allow routes that mutate or drain node state")
	flags.BoolVar(&listRoutes, "list", false, "list route IDs and exit")
	flags.StringVar(&caPath, "ca", os.Getenv("REMNANODE_CONTRACT_CA"), "CA PEM used to verify the node certificate")
	flags.StringVar(&certPath, "cert", os.Getenv("REMNANODE_CONTRACT_CERT"), "mTLS client certificate PEM")
	flags.StringVar(&keyPath, "key", os.Getenv("REMNANODE_CONTRACT_KEY"), "mTLS client private key PEM")
	flags.StringVar(&tokenFile, "token-file", "", "file containing the JWT; otherwise use "+tokenEnvironment)
	flags.StringVar(&serverName, "server-name", "", "optional TLS server-name override")
	flags.DurationVar(&overallTimeout, "timeout", 2*time.Minute, "overall probe timeout")
	flags.DurationVar(&requestTimeout, "request-timeout", 15*time.Second, "timeout per request")
	flags.Int64Var(&maxResponseBytes, "max-response-bytes", contract.DefaultProbeResponseLimit, "maximum response body size")
	flags.BoolVar(&pretty, "pretty", true, "pretty-print JSON output")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: contract-probe [options]")
		fmt.Fprintln(stderr, "JWT is read from REMNANODE_CONTRACT_TOKEN or -token-file and is never emitted.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected arguments: %s\n", strings.Join(flags.Args(), " "))
		return 2
	}
	if listRoutes {
		if err := writeRouteListing(stdout, pretty); err != nil {
			fmt.Fprintf(stderr, "write route list: %v\n", err)
			return 2
		}
		return 0
	}
	if len(targets) == 0 {
		fmt.Fprintln(stderr, "at least one -target is required")
		return 2
	}
	if overallTimeout <= 0 || requestTimeout <= 0 || maxResponseBytes <= 0 {
		fmt.Fprintln(stderr, "timeouts and max-response-bytes must be positive")
		return 2
	}

	routes, err := selectRoutes(routeSelection, allowMutating)
	if err != nil {
		fmt.Fprintf(stderr, "select routes: %v\n", err)
		return 2
	}
	token, err := readToken(tokenFile)
	if err != nil {
		fmt.Fprintf(stderr, "read JWT: %v\n", err)
		return 2
	}
	client, err := newProbeClient(caPath, certPath, keyPath, serverName, requestTimeout)
	if err != nil {
		fmt.Fprintf(stderr, "configure mTLS: %v\n", err)
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, overallTimeout)
	defer cancel()

	prober := contract.Prober{
		Client:           client,
		BearerToken:      token,
		MaxResponseBytes: maxResponseBytes,
	}
	report := probeReport{
		ContractVersion: contract.OfficialNodeVersion,
		ContractCommit:  contract.OfficialNodeCommit,
		GeneratedAt:     time.Now().UTC(),
		Routes:          routeIDs(routes),
		Targets:         make([]probeTargetReport, 0, len(targets)),
	}
	for _, target := range targets {
		report.Targets = append(report.Targets, probeTargetReport{
			Name:    target.Name,
			URL:     target.BaseURL,
			Results: prober.Run(ctx, target, routes),
		})
		if ctx.Err() != nil {
			break
		}
	}
	if len(report.Targets) > 1 {
		baseline := report.Targets[0]
		for _, candidate := range report.Targets[1:] {
			report.Comparisons = append(report.Comparisons, probeComparison{
				Baseline:    baseline.Name,
				Candidate:   candidate.Name,
				Differences: contract.CompareProbeResults(baseline.Results, candidate.Results),
			})
		}
	}

	encoder := json.NewEncoder(stdout)
	if pretty {
		encoder.SetIndent("", "  ")
	}
	if err := encoder.Encode(report); err != nil {
		fmt.Fprintf(stderr, "write report: %v\n", err)
		return 2
	}
	if probeReportFailed(report, len(targets)) {
		return 1
	}
	return 0
}

func selectRoutes(selection string, allowMutating bool) ([]contract.RouteContract, error) {
	selection = strings.TrimSpace(selection)
	if selection == "" {
		return contract.DefaultProbeRoutes(), nil
	}

	available := make(map[string]contract.RouteContract)
	for _, route := range contract.OfficialRoutes() {
		available[route.ID] = route
	}
	requested := make([]string, 0)
	if selection == "all" {
		for routeID := range available {
			requested = append(requested, routeID)
		}
		sort.Strings(requested)
	} else {
		requested = strings.Split(selection, ",")
	}

	seen := make(map[string]struct{}, len(requested))
	routes := make([]contract.RouteContract, 0, len(requested))
	for _, rawID := range requested {
		routeID := strings.TrimSpace(rawID)
		if routeID == "" {
			return nil, errors.New("route list contains an empty ID")
		}
		if _, duplicate := seen[routeID]; duplicate {
			return nil, fmt.Errorf("duplicate route ID %q", routeID)
		}
		seen[routeID] = struct{}{}
		route, ok := available[routeID]
		if !ok {
			return nil, fmt.Errorf("unknown route ID %q", routeID)
		}
		if !route.SafeForProbe() && !allowMutating {
			return nil, fmt.Errorf("route %q can mutate node state; pass -allow-mutating explicitly", routeID)
		}
		routes = append(routes, route)
	}
	return routes, nil
}

func readToken(path string) (string, error) {
	var raw []byte
	var err error
	if path != "" {
		raw, err = os.ReadFile(path)
		if err != nil {
			return "", err
		}
	} else {
		raw = []byte(os.Getenv(tokenEnvironment))
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", errors.New("token is empty; set " + tokenEnvironment + " or -token-file")
	}
	if strings.ContainsAny(token, "\r\n") {
		return "", errors.New("token must contain exactly one line")
	}
	return token, nil
}

func newProbeClient(caPath, certPath, keyPath, serverName string, timeout time.Duration) (*http.Client, error) {
	if caPath == "" || certPath == "" || keyPath == "" {
		return nil, errors.New("-ca, -cert, and -key are required")
	}
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("read CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("CA PEM contains no certificates")
	}
	certificate, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("load client certificate: %w", err)
	}
	tlsConfig := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		RootCAs:      roots,
		Certificates: []tls.Certificate{certificate},
		ServerName:   serverName,
	}
	dialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          4,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		DisableCompression:    true,
		TLSClientConfig:       tlsConfig,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("redirects are not allowed")
		},
	}, nil
}

func validateTargetURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid target URL: %w", err)
	}
	if parsed.Scheme != "https" || parsed.Host == "" {
		return errors.New("target URL must use https and include a host")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.Trim(parsed.Path, "/") != "" {
		return errors.New("target URL must contain only https scheme and host")
	}
	return nil
}

func routeIDs(routes []contract.RouteContract) []string {
	result := make([]string, 0, len(routes))
	for _, route := range routes {
		result = append(result, route.ID)
	}
	return result
}

func writeRouteListing(writer io.Writer, pretty bool) error {
	routes := contract.OfficialRoutes()
	listing := make([]routeListing, 0, len(routes))
	for _, route := range routes {
		listing = append(listing, routeListing{
			ID:            route.ID,
			Method:        route.Method,
			Path:          route.Path,
			SafeByDefault: route.SafeForProbe(),
		})
	}
	encoder := json.NewEncoder(writer)
	if pretty {
		encoder.SetIndent("", "  ")
	}
	return encoder.Encode(listing)
}

func probeReportFailed(report probeReport, requestedTargets int) bool {
	if len(report.Targets) != requestedTargets {
		return true
	}
	for _, target := range report.Targets {
		for _, result := range target.Results {
			if !result.ValidContractResponse() {
				return true
			}
		}
	}
	for _, comparison := range report.Comparisons {
		if len(comparison.Differences) != 0 {
			return true
		}
	}
	return false
}
