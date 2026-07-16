//go:build linux

package plugin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/executil"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/netadmin"
)

const (
	nftCommandTimeout = 15 * time.Second
	nftOutputLimit    = 8 << 10
)

type nftScriptRunner func(ctx context.Context, script string) error

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

func (m *nftManager) Initialize(ctx context.Context) error {
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
	if err := m.run(ctx, renderNFTConfig(firewallConfig{})); err != nil {
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

func (m *nftManager) Apply(ctx context.Context, config firewallConfig) error {
	if m == nil {
		return errNFTablesUnavailable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.ready || m.run == nil {
		return errNFTablesUnavailable
	}
	if err := m.run(ctx, renderNFTConfig(config)); err != nil {
		return fmt.Errorf("apply nftables config: %w", err)
	}
	return nil
}

func (m *nftManager) BlockIPs(ctx context.Context, items []BlockIP) error {
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
	if err := m.run(ctx, script); err != nil {
		return fmt.Errorf("block nftables addresses: %w", err)
	}
	return nil
}

func (m *nftManager) UnblockIPs(ctx context.Context, ips []string) error {
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
		if err := m.run(ctx, command); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || !isMissingNFTElement(err) {
				return fmt.Errorf("unblock nftables addresses: %w", err)
			}
		}
	}
	return nil
}

func (m *nftManager) Close(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.ready || m.run == nil {
		return nil
	}
	if err := m.run(ctx, renderNFTDeleteTables()); err != nil {
		return fmt.Errorf("delete nftables tables: %w", err)
	}
	m.ready = false
	return nil
}

func runNFTScript(parent context.Context, script string) error {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, nftCommandTimeout)
	defer cancel()
	result, err := executil.Run(ctx, strings.NewReader(strings.TrimSpace(script)), nftOutputLimit, "nft", "-f", "-")
	if err == nil {
		return nil
	}
	output := strings.TrimSpace(string(result.DiagnosticOutput()))
	if result.AnyTruncated() {
		output += "..."
	}
	return &nftCommandError{err: err, output: output}
}
