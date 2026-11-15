package ethguard

import (
	"bytes"
	"context"
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
	internalsubscription "github.com/codermaybe/ethguard/internal/subscription"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func prerequisiteReceipt(t *testing.T, hash common.Hash, index uint) json.RawMessage {
	t.Helper()
	value := &types.Receipt{Status: 1, CumulativeGasUsed: 21000, GasUsed: 21000, EffectiveGasPrice: big.NewInt(1), TxHash: hash, BlockHash: common.HexToHash("0x22"), BlockNumber: big.NewInt(7), TransactionIndex: index, Logs: []*types.Log{}}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	fields["from"] = json.RawMessage(`"0x0000000000000000000000000000000000000001"`)
	delete(fields, "root") // Status receipt has no pre-Byzantium state root.
	raw, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestReceiptPullPreservesOfficialFieldsAndFrozenFilter(t *testing.T) {
	wanted := common.HexToHash("0x11")
	seedRaw, _ := json.Marshal([]json.RawMessage{prerequisiteReceipt(t, wanted, 0)})
	seed, _, err := decodeReceiptNotification(seedRaw, "a")
	if err != nil {
		t.Fatal(err)
	}
	seed.Generation = 3
	allRaw, _ := json.Marshal([]json.RawMessage{prerequisiteReceipt(t, wanted, 0), prerequisiteReceipt(t, common.HexToHash("0x33"), 2)})
	wantedHashes := []ethapi.Hash{ethapi.Hash(wanted.Hex())}
	query := &ethapi.TransactionReceiptsQuery{TransactionHashes: &wantedHashes}
	frozen, wire, err := snapshotReceiptSubscriptionQuery(query)
	if err != nil {
		t.Fatal(err)
	}
	(*query.TransactionHashes)[0] = ethapi.Hash(common.HexToHash("0x99").Hex())
	encoded, _ := json.Marshal(wire)
	if !bytes.Contains(encoded, []byte(`"transactionHashes"`)) || bytes.Contains(encoded, []byte("TransactionHashes")) {
		t.Fatalf("nonofficial query: %s", encoded)
	}
	if _, err := ethapi.NewTransactionReceiptsQuery(encoded); err != nil {
		t.Fatal(err)
	}
	source := &pullTestSource{id: "b", pull: func(method string, args ...any) (json.RawMessage, error) {
		if method != "eth_getBlockReceipts" || len(args) != 1 || args[0] != common.HexToHash("0x22").Hex() {
			t.Fatalf("Pull %s %v", method, args)
		}
		return allRaw, nil
	}}
	got, err := pullReceiptNotification(context.Background(), source, internalsubscription.KindTransactionReceipts, seed.ContextKey, seed, frozen)
	if err != nil {
		t.Fatal(err)
	}
	if source.pullCount() != 1 || got.SourceID != "b" || got.Generation != seed.Generation || got.Key != seed.Key {
		t.Fatalf("Pull: %+v", got)
	}
	parsed, err := ethapi.NewGetBlockReceiptsResult(got.Payload)
	if err != nil || parsed == nil || len(*parsed) != 1 || (*parsed)[0].TransactionHash != ethapi.Hash(wanted.Hex()) {
		t.Fatalf("official Pull payload %s: %v", got.Payload, err)
	}
	if !bytes.Contains(got.Payload, []byte(`"from"`)) || bytes.Contains(got.Payload, []byte(`"root":"0x"`)) {
		t.Fatalf("geth projection was reused as wire: %s", got.Payload)
	}
	var malformed []map[string]json.RawMessage
	_ = json.Unmarshal(allRaw, &malformed)
	malformed[0]["logsBloom"] = json.RawMessage(`"0x` + strings.Repeat("11", 256) + `"`)
	bad, _ := json.Marshal(malformed)
	source.pull = func(string, ...any) (json.RawMessage, error) { return bad, nil }
	if _, err := pullReceiptNotification(context.Background(), source, internalsubscription.KindTransactionReceipts, seed.ContextKey, seed, frozen); err == nil {
		t.Fatal("malformed source bloom accepted by Pull")
	}
}

func TestReceiptNotificationKeysCoverOfficialFieldsAndOneBlock(t *testing.T) {
	raw := prerequisiteReceipt(t, common.HexToHash("0x11"), 0)
	first, _ := json.Marshal([]json.RawMessage{raw})
	a, _, err := decodeReceiptNotification(first, "a")
	if err != nil {
		t.Fatal(err)
	}
	var changed map[string]json.RawMessage
	_ = json.Unmarshal(raw, &changed)
	changed["from"] = json.RawMessage(`"0x0000000000000000000000000000000000000002"`)
	modified, _ := json.Marshal(changed)
	second, _ := json.Marshal([]json.RawMessage{modified})
	b, _, err := decodeReceiptNotification(second, "b")
	if err != nil || a.ContextKey != b.ContextKey || a.Key == b.Key {
		t.Fatalf("sender declaration lost from comparison: %v", err)
	}
	changed["blockNumber"] = json.RawMessage(`"0x8"`)
	modified, _ = json.Marshal(changed)
	mixed, _ := json.Marshal([]json.RawMessage{raw, modified})
	if _, _, err := decodeReceiptNotification(mixed, "b"); err == nil {
		t.Fatal("mixed-block event accepted")
	}
	if _, values, err := decodeReceiptNotification([]byte(`[]`), "a"); err != nil || values == nil || len(values) != 0 {
		t.Fatalf("empty event changed: %v %v", values, err)
	}
}

func TestReceiptSubscriptionEmptyFiltersUseOfficialEmptyObject(t *testing.T) {
	for _, q := range []*ethapi.TransactionReceiptsQuery{nil, {}, {TransactionHashes: &[]ethapi.Hash{}}} {
		_, wire, err := snapshotReceiptSubscriptionQuery(q)
		if err != nil {
			t.Fatalf("empty query %v: %v", q, err)
		}
		raw, err := json.Marshal(wire)
		if err != nil || string(raw) != "{}" {
			t.Fatalf("empty query %s: %v", raw, err)
		}
	}
}
