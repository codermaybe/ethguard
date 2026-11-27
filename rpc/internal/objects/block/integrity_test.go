package block

import (
	"bytes"
	"context"
	"encoding/json"
	"math/big"
	"os"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/trie"
)

func completeBlock(t *testing.T) (json.RawMessage, rpc.Request) {
	t.Helper()
	txRaw, err := os.ReadFile("../transaction/testdata/tx-one-mainnet-1559.json")
	if err != nil {
		t.Fatal(err)
	}
	var tx types.Transaction
	if err := json.Unmarshal(txRaw, &tx); err != nil {
		t.Fatal(err)
	}
	head := &types.Header{UncleHash: types.EmptyUncleHash, TxHash: types.DeriveSha(types.Transactions{&tx}, trie.NewStackTrie(nil)), Difficulty: big.NewInt(1), Number: big.NewInt(8), GasLimit: 1000000, Time: 12}
	raw, err := marshalTestHeader(t, head)
	if err != nil {
		t.Fatal(err)
	}
	var fields, txFields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	_ = json.Unmarshal(txRaw, &txFields)
	txFields["blockHash"], _ = json.Marshal(head.Hash())
	txFields["blockNumber"] = json.RawMessage(`"0x8"`)
	txFields["blockTimestamp"] = json.RawMessage(`"0xc"`)
	txFields["transactionIndex"] = json.RawMessage(`"0x0"`)
	item, _ := json.Marshal(txFields)
	fields["transactions"], _ = json.Marshal([]json.RawMessage{item})
	fields["uncles"] = json.RawMessage(`[]`)
	fields["size"] = json.RawMessage(`"0x321"`)
	raw, _ = json.Marshal(fields)
	return raw, rpc.Request{Params: []any{head.Hash().Hex(), true}}
}

func mutateBlock(t *testing.T, raw json.RawMessage, fn func(map[string]json.RawMessage, map[string]json.RawMessage)) json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	var txs []map[string]json.RawMessage
	_ = json.Unmarshal(fields["transactions"], &txs)
	fn(fields, txs[0])
	fields["transactions"], _ = json.Marshal(txs)
	out, _ := json.Marshal(fields)
	return out
}

func TestFullBlockCanonicalOfficialShapeAndTransactionContext(t *testing.T) {
	raw, req := completeBlock(t)
	if _, err := AdmitBlock(context.Background(), req, raw, true, nil); err != nil {
		t.Fatal(err)
	}
	canonical, err := Encode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ethapi.NewBlock(canonical); err != nil {
		t.Fatalf("canonical value is not an official Block: %v", err)
	}
	for _, pair := range []string{`"size":"0x321"`, `"from":"0x2252f216f4a494a87025123425181ca1bb754fb8"`, `"blockNumber":"0x8"`, `"blockTimestamp":"0xc"`, `"transactionIndex":"0x0"`} {
		if !bytes.Contains(canonical, []byte(pair)) {
			t.Fatalf("official declaration lost %s", pair)
		}
	}
	// from is checked as a source declaration. This boundary deliberately does
	// not claim sender recovery or compare it to a recovered account.
	changed := mutateBlock(t, raw, func(_ map[string]json.RawMessage, tx map[string]json.RawMessage) {
		tx["from"], _ = json.Marshal(common.Address{}.Hex())
	})
	if _, err := AdmitBlock(context.Background(), req, changed, true, nil); err != nil {
		t.Fatalf("valid declared sender rejected: %v", err)
	}
	changedCanonical, err := Encode(changed)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(canonical, changedCanonical) {
		t.Fatal("distinct declared senders produced equal comparison values")
	}
}

func TestFullBlockRejectsMissingRequiredAndFalseOriginalDeclarations(t *testing.T) {
	raw, req := completeBlock(t)
	cases := []struct {
		name   string
		mutate func(map[string]json.RawMessage, map[string]json.RawMessage)
	}{
		{"size", func(b, _ map[string]json.RawMessage) { delete(b, "size") }},
		{"from", func(_, tx map[string]json.RawMessage) { delete(tx, "from") }},
		{"inclusion", func(_, tx map[string]json.RawMessage) { delete(tx, "blockTimestamp") }},
		{"wire hash", func(b, _ map[string]json.RawMessage) { b["hash"], _ = json.Marshal(common.HexToHash("0x123").Hex()) }},
		{"transaction hash", func(_, tx map[string]json.RawMessage) { tx["hash"], _ = json.Marshal(common.HexToHash("0x123").Hex()) }},
		{"transaction blockHash", func(_, tx map[string]json.RawMessage) {
			tx["blockHash"], _ = json.Marshal(common.HexToHash("0x123").Hex())
		}},
		{"transaction blockNumber", func(_, tx map[string]json.RawMessage) { tx["blockNumber"] = json.RawMessage(`"0x9"`) }},
		{"transaction blockTimestamp", func(_, tx map[string]json.RawMessage) { tx["blockTimestamp"] = json.RawMessage(`"0xd"`) }},
		{"transaction index", func(_, tx map[string]json.RawMessage) { tx["transactionIndex"] = json.RawMessage(`"0x1"`) }},
		{"signature", func(_, tx map[string]json.RawMessage) { tx["r"] = json.RawMessage(`"0x0"`) }},
		{"uncles", func(b, _ map[string]json.RawMessage) {
			b["uncles"], _ = json.Marshal([]common.Hash{common.HexToHash("0x1")})
		}},
		{"unexpected withdrawals", func(b, _ map[string]json.RawMessage) { b["withdrawals"] = json.RawMessage(`[]`) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bad := mutateBlock(t, raw, tc.mutate)
			if _, err := AdmitBlock(context.Background(), req, bad, true, nil); err == nil {
				t.Fatal("invalid source accepted")
			}
		})
	}
}

func TestFullBlockRetainsTrieAndWithdrawalCommitmentChecks(t *testing.T) {
	raw, _ := completeBlock(t)
	for _, tc := range []string{"transactions", "withdrawals"} {
		t.Run(tc, func(t *testing.T) {
			var head types.Header
			_ = json.Unmarshal(raw, &head)
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(raw, &fields)
			if tc == "transactions" {
				fields["transactions"] = json.RawMessage(`[]`)
			} else {
				root := common.HexToHash("0x456")
				head.WithdrawalsHash = &root
				fields["withdrawalsRoot"], _ = json.Marshal(root)
				fields["withdrawals"] = json.RawMessage(`[]`)
				// Keep transaction metadata bound to the new header so this negative
				// reaches the withdrawal root check rather than an earlier relation.
				var txs []map[string]json.RawMessage
				_ = json.Unmarshal(fields["transactions"], &txs)
				txs[0]["blockHash"], _ = json.Marshal(head.Hash())
				fields["transactions"], _ = json.Marshal(txs)
			}
			fields["hash"], _ = json.Marshal(head.Hash())
			bad, _ := json.Marshal(fields)
			req := rpc.Request{Params: []any{head.Hash().Hex(), true}}
			if _, err := AdmitBlock(context.Background(), req, bad, true, nil); err == nil {
				t.Fatal("false body commitment accepted")
			}
		})
	}
}

func TestFullBlockUncleEvidenceStaysSourceBoundAndChecksOriginalHash(t *testing.T) {
	uncle := &types.Header{UncleHash: types.EmptyUncleHash, Difficulty: big.NewInt(1), Number: big.NewInt(6), GasLimit: 1000000, Time: 10}
	head := &types.Header{UncleHash: types.CalcUncleHash([]*types.Header{uncle}), TxHash: types.EmptyTxsHash, Difficulty: big.NewInt(1), Number: big.NewInt(8), GasLimit: 1000000, Time: 12}
	raw, _ := marshalTestHeader(t, head)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	fields["size"] = json.RawMessage(`"0x200"`)
	fields["transactions"] = json.RawMessage(`[]`)
	fields["uncles"], _ = json.Marshal([]common.Hash{uncle.Hash()})
	raw, _ = json.Marshal(fields)
	uncleRaw, _ := json.Marshal(uncle)
	req := rpc.Request{Params: []any{head.Hash().Hex(), true}}
	caller := func(_ context.Context, method string, params []any) (json.RawMessage, error) {
		if method != "eth_getUncleByBlockHashAndIndex" || len(params) != 2 || params[0] != head.Hash().Hex() || params[1] != "0x0" {
			t.Fatalf("uncle load escaped containing source context: %s %v", method, params)
		}
		return uncleRaw, nil
	}
	evidence, err := AdmitBlock(context.Background(), req, raw, true, caller)
	if err != nil {
		t.Fatal(err)
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(evidence, &rows); err != nil || len(rows) != 1 || !bytes.Equal(rows[0], uncleRaw) {
		t.Fatal("source uncle evidence lost")
	}
	if _, err := AdmitBlock(context.Background(), req, raw, true, nil); err == nil {
		t.Fatal("unproved nonempty uncle root accepted")
	}
	var badUncle map[string]json.RawMessage
	_ = json.Unmarshal(uncleRaw, &badUncle)
	badUncle["hash"], _ = json.Marshal(common.HexToHash("0xdead").Hex())
	uncleRaw, _ = json.Marshal(badUncle)
	if _, err := AdmitBlock(context.Background(), req, raw, true, caller); err == nil {
		t.Fatal("uncle with false original wire hash accepted")
	}
}

func TestFullBlockEmbeddedTransactionJSONEscapesUseCanonicalPayload(t *testing.T) {
	raw, req := completeBlock(t)
	// Both fields belong to the real signed payload. Only JSON wire spelling
	// changes; the original transaction hash and trie commitment stay fixed.
	escaped := mutateBlock(t, raw, func(_ map[string]json.RawMessage, tx map[string]json.RawMessage) {
		for _, name := range []string{"input", "gas"} {
			if !bytes.HasPrefix(tx[name], []byte(`"0x`)) {
				t.Fatalf("fixture %s has no hex prefix", name)
			}
			tx[name] = append([]byte(`"\u0030x`), tx[name][3:]...)
		}
	})
	if bytes.Equal(escaped, raw) {
		t.Fatal("escape did not change source representation")
	}
	if _, err := AdmitBlock(context.Background(), req, escaped, true, nil); err != nil {
		t.Fatalf("signed payload with legal JSON escape rejected: %v", err)
	}
	canonical, err := Encode(escaped)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := Encode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canonical, baseline) {
		t.Fatal("equivalent embedded wire spellings changed committed value")
	}
	forged := mutateBlock(t, escaped, func(_ map[string]json.RawMessage, tx map[string]json.RawMessage) {
		tx["hash"], _ = json.Marshal(common.HexToHash("0xdead").Hex())
	})
	if _, err := AdmitBlock(context.Background(), req, forged, true, nil); err == nil || !strings.Contains(err.Error(), "hash does not match signed payload") {
		t.Fatalf("canonicalization hid false original transaction hash: %v", err)
	}
}

func TestFullBlockJSONEscapesDoNotReenterRawGethBodyDecoder(t *testing.T) {
	raw, req := completeBlock(t)
	escaped := json.RawMessage(strings.ReplaceAll(string(raw), `"0x`, `"\u0030x`))
	if bytes.Equal(raw, escaped) {
		t.Fatal("escape did not change source representation")
	}
	if _, err := AdmitBlock(context.Background(), req, escaped, true, nil); err != nil {
		t.Fatalf("equivalent original block JSON rejected by private bridge: %v", err)
	}
	canonical, err := Encode(escaped)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := Encode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canonical, baseline) {
		t.Fatal("original JSON representation changed complete protected value")
	}
}
