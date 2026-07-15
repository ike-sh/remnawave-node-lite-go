package plugin

import (
	"errors"
	"fmt"
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
	Initialize() error
	Available() bool
	Apply(config firewallConfig) error
	BlockIPs(items []BlockIP) error
	UnblockIPs(ips []string) error
	Close() error
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
