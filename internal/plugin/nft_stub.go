//go:build !linux

package plugin

type nftManager struct{}

func newNFTManager() *nftManager { return &nftManager{} }

func (*nftManager) Initialize() error          { return errNFTablesUnavailable }
func (*nftManager) Available() bool            { return false }
func (*nftManager) Apply(firewallConfig) error { return errNFTablesUnavailable }
func (*nftManager) BlockIPs([]BlockIP) error   { return errNFTablesUnavailable }
func (*nftManager) UnblockIPs([]string) error  { return errNFTablesUnavailable }
func (*nftManager) Close() error               { return nil }
