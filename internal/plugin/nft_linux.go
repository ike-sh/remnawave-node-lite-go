//go:build linux

package plugin

import (
	"fmt"
	"os/exec"
	"strings"
	"sync"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/netadmin"
)

type nftScriptRunner func(script string) error

type nftManager struct {
	mu      sync.RWMutex
	capable bool
	ready   bool
	run     nftScriptRunner
}

func newNFTManager() *nftManager {
	return &nftManager{
		capable: netadmin.HasCapNetAdmin(),
		run:     runNFTScript,
	}
}

func (m *nftManager) Initialize() error {
	if m == nil {
		return errNFTablesUnavailable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ready {
		return nil
	}
	if !m.capable || m.run == nil {
		return errNFTablesUnavailable
	}
	if err := m.run(renderNFTConfig(firewallConfig{})); err != nil {
		return fmt.Errorf("initialize nftables: %w", err)
	}
	m.ready = true
	return nil
}

func (m *nftManager) Available() bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.ready
}

func (m *nftManager) Apply(config firewallConfig) error {
	if m == nil {
		return errNFTablesUnavailable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.ready || m.run == nil {
		return errNFTablesUnavailable
	}
	if err := m.run(renderNFTConfig(config)); err != nil {
		return fmt.Errorf("apply nftables config: %w", err)
	}
	return nil
}

func (m *nftManager) BlockIPs(items []BlockIP) error {
	if m == nil {
		return errNFTablesUnavailable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.ready || m.run == nil {
		return errNFTablesUnavailable
	}
	script, err := renderNFTBlock(items)
	if err != nil {
		return err
	}
	if script == "" {
		return nil
	}
	if err := m.run(script); err != nil {
		return fmt.Errorf("block nftables addresses: %w", err)
	}
	return nil
}

func (m *nftManager) UnblockIPs(ips []string) error {
	if m == nil {
		return errNFTablesUnavailable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.ready || m.run == nil {
		return errNFTablesUnavailable
	}
	commands, err := renderNFTUnblock(ips)
	if err != nil {
		return err
	}
	for _, command := range commands {
		if err := m.run(command); err != nil && !isMissingNFTElement(err) {
			return fmt.Errorf("unblock nftables addresses: %w", err)
		}
	}
	return nil
}

func (m *nftManager) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.ready || m.run == nil {
		return nil
	}
	if err := m.run(renderNFTDeleteTables()); err != nil {
		return fmt.Errorf("delete nftables tables: %w", err)
	}
	m.ready = false
	return nil
}

func runNFTScript(script string) error {
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(strings.TrimSpace(script))
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	return &nftCommandError{err: err, output: strings.TrimSpace(string(output))}
}
