package typed

import (
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// The typed-projection baseline: the projection work the delivery layer adds
// on top of an already resolved, already admitted value. These measurements
// start from canonical bytes and never touch the aggregation pipeline, so a
// projection cost can never be mistaken for a processor re-run. The same
// block value runs through both entries: the admitted projection, which only
// re-parses through the generated schema, and the direct raw entry, which
// additionally re-checks the header self-hash and the payload bridge — the
// gap between the rows is the price of lacking admission proof, not a
// projection inefficiency.

// projectionBlockFixture builds one complete official uncle-bearing block
// value shaped like the committed canonical form, with geth computing the
// declared hashes.
func projectionBlockFixture(b *testing.B) (block json.RawMessage, evidence json.RawMessage) {
	b.Helper()
	uncle := &types.Header{
		ParentHash:  common.HexToHash("0x" + strings.Repeat("77", 32)),
		UncleHash:   types.EmptyUncleHash,
		Root:        common.HexToHash("0x" + strings.Repeat("78", 32)),
		TxHash:      types.EmptyTxsHash,
		ReceiptHash: common.HexToHash("0x" + strings.Repeat("79", 32)),
		Difficulty:  big.NewInt(1),
		Number:      big.NewInt(15),
		GasLimit:    30_000_000,
		Time:        4,
	}
	uncles := []*types.Header{uncle}
	head := &types.Header{
		ParentHash:  common.HexToHash("0x" + strings.Repeat("01", 32)),
		UncleHash:   types.CalcUncleHash(uncles),
		Root:        common.HexToHash("0x" + strings.Repeat("02", 32)),
		TxHash:      types.EmptyTxsHash,
		ReceiptHash: common.HexToHash("0x" + strings.Repeat("03", 32)),
		Difficulty:  big.NewInt(2),
		Number:      big.NewInt(16),
		GasLimit:    30_000_000,
		Time:        5,
	}
	blockHash := types.NewBlockWithHeader(head).Hash()
	raw, err := head.MarshalJSON()
	if err != nil {
		b.Fatal(err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		b.Fatal(err)
	}
	obj["hash"] = json.RawMessage(`"` + blockHash.Hex() + `"`)
	obj["size"] = json.RawMessage(`"0x500"`)
	obj["transactions"] = json.RawMessage(`[]`)
	uncleHashes, err := json.Marshal([]string{uncle.Hash().Hex()})
	if err != nil {
		b.Fatal(err)
	}
	obj["uncles"] = uncleHashes
	// Mirror the shared optional-null folding so the fixture spells the
	// committed canonical shape.
	for _, name := range []string{"baseFeePerGas", "withdrawalsRoot", "blobGasUsed", "excessBlobGas", "parentBeaconBlockRoot", "requestsHash", "blockAccessListHash", "slotNumber"} {
		if value, ok := obj[name]; ok && string(value) == "null" {
			delete(obj, name)
		}
	}
	block, err = json.Marshal(obj)
	if err != nil {
		b.Fatal(err)
	}
	uncleRaw, err := uncle.MarshalJSON()
	if err != nil {
		b.Fatal(err)
	}
	evidence, err = json.Marshal([]json.RawMessage{json.RawMessage(uncleRaw)})
	if err != nil {
		b.Fatal(err)
	}
	return block, evidence
}

func BenchmarkTypedProjectionAdmittedBlock(b *testing.B) {
	block, _ := projectionBlockFixture(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := DecodeBlockAdmitted(block); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkTypedProjectionRawBlock runs the direct raw entry over the same
// value with its same-source uncle evidence supplied. This is the public
// raw-entry contract cost: every check the admission owner would have run
// rides here instead.
func BenchmarkTypedProjectionRawBlock(b *testing.B) {
	block, evidence := projectionBlockFixture(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := DecodeBlockWithEvidence(block, evidence); err != nil {
			b.Fatal(err)
		}
	}
}

// projectionReceiptFixture builds one complete official receipt value the
// way the aggregation pipeline commits it: canonical members, sender bound,
// logs carrying their own binding context.
func projectionReceiptFixture(b *testing.B, logs int) json.RawMessage {
	b.Helper()
	txHash := common.HexToHash("0x" + strings.Repeat("11", 32))
	blockHash := common.HexToHash("0x" + strings.Repeat("22", 32))
	receipt := &types.Receipt{
		Status:            types.ReceiptStatusSuccessful,
		CumulativeGasUsed: 21_000 * uint64(logs+1),
		GasUsed:           21_000,
		EffectiveGasPrice: big.NewInt(1),
		TxHash:            txHash,
		BlockHash:         blockHash,
		BlockNumber:       big.NewInt(7),
		TransactionIndex:  0,
		Logs: func() []*types.Log {
			out := make([]*types.Log, logs)
			for i := range out {
				out[i] = &types.Log{
					Address:     common.HexToAddress("0x" + strings.Repeat("33", 20)),
					Topics:      []common.Hash{common.HexToHash("0x" + strings.Repeat("44", 32))},
					Data:        []byte{1, 2, 3, 4},
					TxHash:      txHash,
					BlockHash:   blockHash,
					BlockNumber: 7,
					Index:       uint(i),
				}
			}
			return out
		}(),
	}
	receipt.Bloom = types.CreateBloom(receipt)
	raw, err := json.Marshal(receipt)
	if err != nil {
		b.Fatal(err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		b.Fatal(err)
	}
	obj["from"] = json.RawMessage(`"0x0000000000000000000000000000000000000001"`)
	delete(obj, "root")
	out, err := json.Marshal(obj)
	if err != nil {
		b.Fatal(err)
	}
	return out
}

func BenchmarkTypedProjectionReceipt(b *testing.B) {
	raw := projectionReceiptFixture(b, 1)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := DecodeReceipt(raw); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTypedProjectionLogArray(b *testing.B) {
	var rows []json.RawMessage
	for i := 0; i < 8; i++ {
		rows = append(rows, projectionReceiptFixture(b, 1))
	}
	// The log array carries only the log members; the receipt fixture wraps
	// one log, so extract that log from each row once, outside the timer.
	var logs []json.RawMessage
	for _, row := range rows {
		var receipt struct {
			Logs []json.RawMessage `json:"logs"`
		}
		if err := json.Unmarshal(row, &receipt); err != nil {
			b.Fatal(err)
		}
		logs = append(logs, receipt.Logs[0])
	}
	raw, err := json.Marshal(logs)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := DecodeLogs(raw); err != nil {
			b.Fatal(err)
		}
	}
}
