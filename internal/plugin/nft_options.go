package plugin

// NFTOptions mirrors the nftables runtime switches in upstream Node 3.4.1.
type NFTOptions struct {
	Logging            bool
	AcceptReplyTraffic bool
}

func defaultNFTOptions() NFTOptions { return NFTOptions{Logging: true} }
