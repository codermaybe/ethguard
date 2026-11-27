package methods

import (
	"encoding/json"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
)

// bloomOf is geth's own bloom over the fixture's log, so the receipt encoder
// accepts the payload instead of rejecting a mismatched bloom.
func bloomOf(t *testing.T, log map[string]any) string {
	t.Helper()
	entry := &types.Log{Address: common.HexToAddress(log["address"].(string)), Data: common.FromHex(log["data"].(string))}
	for _, topic := range log["topics"].([]string) {
		entry.Topics = append(entry.Topics, common.HexToHash(topic))
	}
	return hexutil.Encode(types.CreateBloom(&types.Receipt{Logs: []*types.Log{entry}}).Bytes())
}

func mustMarshal(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
