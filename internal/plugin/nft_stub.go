//go:build !linux

package plugin

type nftManager struct{}

func newNFTManager() *nftManager {
	return &nftManager{}
}

func (m *nftManager) Available() bool {
	return false
}

func (m *nftManager) recreateTables() error {
	return nil
}

func (m *nftManager) blockIP(ip string, timeoutSeconds int) error {
	return nil
}

func (m *nftManager) unblockIP(ip string) error {
	return nil
}

func (m *nftManager) syncIngressFilter(ips []string) error { return nil }

func (m *nftManager) syncEgressFilter(ips []string, ports []int) error { return nil }
