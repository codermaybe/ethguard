package methods

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
)

// logFixture is one log the receipt encoder will accept: it carries the hashes
// of the receipt that owns it, so the per-log binding holds.
func logFixture() map[string]any {
	return map[string]any{
		"address":          "0x0000000aa232009084bd71a5797d089aa4edfad4",
		"topics":           []string{"0x" + strings.Repeat("11", 32), "0x" + strings.Repeat("22", 32)},
		"data":             "0xdeadbeef",
		"blockNumber":      "0x18d592f",
		"transactionIndex": "0x0",
		"transactionHash":  "0xaa1c6c878d686b6c14b77c6b78ff23ae4b069cf941736678e9c1f8102b219b15",
		"blockHash":        "0x9bc1666a9b54a70b8fc7f4caf052c25d2bef94897c2815dc168163d6bc188b1e",
		"logIndex":         "0x1",
		"removed":          false,
		"blockTimestamp":   "0x64",
		"mark":             1,
		"vendor":           "0xFF",
	}
}

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
