package plugin

import (
	"fmt"
	"strconv"
)

// These limits keep one plugin mutation comfortably bounded on the supported
// 512 MiB nodes. They are intentionally shared by validation, planning, and
// nft rendering so no later stage can expand an already accepted request
// without a corresponding budget.
const (
	maxPluginHashDepth        = 64
	maxPluginHashTokens       = 65_536
	maxPluginHashOutputBytes  = 2 << 20
	maxPluginConfigBytes      = 2 << 20
	maxSharedLists            = 128
	maxSharedListItems        = 8_192
	maxTotalSharedListItems   = 16_384
	maxASNItems               = 1_024
	maxResolvedIPItems        = 32_768
	maxFilterItems            = 16_384
	maxIgnoreItems            = 8_192
	maxRuleTags               = 256
	maxPluginStringBytes      = 512
	maxDynamicNFTElements     = 16_384
	maxNFTBlockBatch          = 1_024
	maxNFTUnblockBatch        = 128
	maxNFTScriptBytes         = 2 << 20
	maxLoggedDiagnosticValues = 32
	maxLogValueBytes          = 256
)

func validateArrayLength(field string, length, maximum int) error {
	if length > maximum {
		return fmt.Errorf("%s contains %d items; maximum is %d", field, length, maximum)
	}
	return nil
}

func validateStringLength(field, value string) error {
	if len(value) > maxPluginStringBytes {
		return fmt.Errorf("%s is %d bytes; maximum is %d", field, len(value), maxPluginStringBytes)
	}
	return nil
}

func quotedForError(value string) string {
	quoted := strconv.QuoteToASCII(value)
	if len(quoted) <= maxLogValueBytes {
		return quoted
	}
	suffix := fmt.Sprintf("... (%d bytes)", len(value))
	return quoted[:maxLogValueBytes-len(suffix)] + suffix
}
