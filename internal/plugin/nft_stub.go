//go:build !linux

package plugin

import "context"

type nftManager struct{}

func newNFTManager() *nftManager { return &nftManager{} }

func (*nftManager) Initialize(context.Context) error            { return errNFTablesUnavailable }
func (*nftManager) Available() bool                             { return false }
func (*nftManager) Apply(context.Context, firewallConfig) error { return errNFTablesUnavailable }
func (*nftManager) Reset(context.Context, firewallConfig) error { return errNFTablesUnavailable }
func (*nftManager) BlockIPs(context.Context, []BlockIP) error   { return errNFTablesUnavailable }
func (*nftManager) UnblockIPs(context.Context, []string) error  { return errNFTablesUnavailable }
func (*nftManager) Close(context.Context) error                 { return nil }
