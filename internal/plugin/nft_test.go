package plugin

import "testing"

func TestNilNFTManagerIsUnavailable(t *testing.T) {
	var manager *nftManager
	if manager.Available() {
		t.Fatal("nil nft manager must be unavailable")
	}
}
