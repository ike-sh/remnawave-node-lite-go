package plugin

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var errNFTablesUnavailable = errors.New("nftables unavailable")

type firewallConfig struct {
	ingressIPs  []string
	egressIPs   []string
	egressPorts []int
}

func (c firewallConfig) clone() firewallConfig {
	return firewallConfig{
		ingressIPs:  append([]string(nil), c.ingressIPs...),
		egressIPs:   append([]string(nil), c.egressIPs...),
		egressPorts: append([]int(nil), c.egressPorts...),
	}
}

type firewallBackend interface {
	Initialize(ctx context.Context) error
	Available() bool
	// Apply replaces static filter elements without disturbing timed torrent blocks.
	Apply(ctx context.Context, config firewallConfig) error
	// Reset recreates the owned tables and intentionally clears dynamic elements.
	Reset(ctx context.Context, config firewallConfig) error
	BlockIPs(ctx context.Context, items []BlockIP) error
	UnblockIPs(ctx context.Context, ips []string) error
	Close(ctx context.Context) error
}

type nftCommandError struct {
	err    error
	output string
}

func (e *nftCommandError) Error() string {
	if e.output == "" {
		return fmt.Sprintf("nft command: %v", e.err)
	}
	return fmt.Sprintf("nft command: %v: %s", e.err, e.output)
}

func (e *nftCommandError) Unwrap() error {
	return e.err
}

func isMissingNFTElement(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no such file or directory") ||
		strings.Contains(message, "no such element") ||
		strings.Contains(message, "element does not exist")
}

func isAmbiguousNFTNotFound(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no such file or directory") &&
		!strings.Contains(message, "no such element") &&
		!strings.Contains(message, "element does not exist")
}
