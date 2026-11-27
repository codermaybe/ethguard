package receipt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/holiman/uint256"
)

func commitmentFixture(t *testing.T, postState bool, kinds ...uint8) (*types.Header, types.Transactions, []map[string]any) {
	t.Helper()
	key, err := crypto.HexToECDSA(strings.Repeat("01", 32))
	if err != nil {
		t.Fatal(err)
	}
	to := common.HexToAddress(decodeAddress)
	txs := make(types.Transactions, len(kinds))
	receipts := make(types.Receipts, len(kinds))
	for i, kind := range kinds {
		var inner types.TxData
		switch kind {
		case types.LegacyTxType:
			inner = &types.LegacyTx{Nonce: uint64(i), To: &to, Gas: 21000, GasPrice: big.NewInt(2), Value: big.NewInt(0)}
		case types.AccessListTxType:
			inner = &types.AccessListTx{ChainID: big.NewInt(1), Nonce: uint64(i), To: &to, Gas: 21000, GasPrice: big.NewInt(2), Value: big.NewInt(0)}
		case types.DynamicFeeTxType:
			inner = &types.DynamicFeeTx{ChainID: big.NewInt(1), Nonce: uint64(i), To: &to, Gas: 21000, GasFeeCap: big.NewInt(2), GasTipCap: big.NewInt(1), Value: big.NewInt(0)}
		case types.BlobTxType:
			inner = &types.BlobTx{ChainID: uint256.NewInt(1), Nonce: uint64(i), To: to, Gas: 21000, GasFeeCap: uint256.NewInt(2), GasTipCap: uint256.NewInt(1), Value: uint256.NewInt(0), BlobFeeCap: uint256.NewInt(1), BlobHashes: []common.Hash{common.HexToHash("0x01" + strings.Repeat("00", 31))}}
		case types.SetCodeTxType:
			inner = &types.SetCodeTx{ChainID: uint256.NewInt(1), Nonce: uint64(i), To: to, Gas: 21000, GasFeeCap: uint256.NewInt(2), GasTipCap: uint256.NewInt(1), Value: uint256.NewInt(0)}
		default:
			t.Fatalf("unsupported fixture transaction type %d", kind)
		}
		txs[i], err = types.SignNewTx(key, types.LatestSignerForChainID(big.NewInt(1)), inner)
		if err != nil {
			t.Fatal(err)
		}
		r := &types.Receipt{Type: kind, Status: 1, CumulativeGasUsed: uint64(i+1) * 21000, Logs: []*types.Log{{Address: to, Topics: []common.Hash{common.BigToHash(big.NewInt(int64(i + 1)))}, Data: []byte{byte(i + 1)}}}}
		if postState {
			r.PostState = common.HexToHash("0x1234").Bytes()
		}
		r.Bloom = types.CreateBloom(r)
		receipts[i] = r
	}
	header := &types.Header{Number: big.NewInt(42), Difficulty: big.NewInt(1), GasLimit: 30000000, GasUsed: uint64(len(txs)) * 21000, Time: 123,
		TxHash: types.DeriveSha(txs, trie.NewStackTrie(nil)), ReceiptHash: types.DeriveSha(receipts, trie.NewStackTrie(nil))}
	rows := make([]map[string]any, len(txs))
	for i, r := range receipts {
		rows[i] = map[string]any{
			"type": hexutil.EncodeUint64(uint64(r.Type)), "status": "0x1", "cumulativeGasUsed": hexutil.EncodeUint64(r.CumulativeGasUsed),
			"gasUsed": "0x5208", "effectiveGasPrice": "0x2", "logsBloom": hexutil.Encode(r.Bloom.Bytes()),
			"transactionHash": txs[i].Hash().Hex(), "transactionIndex": hexutil.EncodeUint64(uint64(i)), "blockHash": header.Hash().Hex(), "blockNumber": "0x2a",
			"from": crypto.PubkeyToAddress(key.PublicKey).Hex(), "to": to.Hex(), "contractAddress": nil,
			"logs": []map[string]any{{"address": to.Hex(), "topics": []string{r.Logs[0].Topics[0].Hex()}, "data": hexutil.Encode(r.Logs[0].Data),
				"transactionHash": txs[i].Hash().Hex(), "transactionIndex": hexutil.EncodeUint64(uint64(i)), "blockHash": header.Hash().Hex(), "blockNumber": "0x2a", "logIndex": hexutil.EncodeUint64(uint64(i)), "removed": false}},
		}
		if postState {
			delete(rows[i], "status")
			rows[i]["root"] = hexutil.Encode(r.PostState)
		}
	}
	return header, txs, rows
}

func commitmentJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestVerifyBlockCommitmentAcceptsEmptyAndTypedReceipts(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		header, txs, rows := commitmentFixture(t, false)
		canonical, err := VerifyBlockCommitment(header, txs, commitmentJSON(t, rows))
		if err != nil || string(canonical) != "[]" {
			t.Fatalf("empty block = %s, %v", canonical, err)
		}
	})
	for _, kind := range []uint8{types.LegacyTxType, types.AccessListTxType, types.DynamicFeeTxType, types.BlobTxType, types.SetCodeTxType} {
		for _, omit := range []bool{false, true} {
			t.Run(fmt.Sprintf("type-%d/omitted-%t", kind, omit), func(t *testing.T) {
				header, txs, rows := commitmentFixture(t, false, kind)
				if omit {
					delete(rows[0], "type")
				}
				raw := commitmentJSON(t, rows)
				canonical, err := VerifyBlockCommitment(header, txs, raw)
				if err != nil {
					t.Fatal(err)
				}
				want, err := EncodeBlock(raw)
				if err != nil || !bytes.Equal(canonical, want) {
					t.Fatalf("official presence changed: %s, want %s (%v)", canonical, want, err)
				}
			})
		}
	}
}

func TestVerifyBlockCommitmentRejectsIncompleteOrMisboundBatch(t *testing.T) {
	cases := []struct {
		name string
		edit func([]map[string]any) []map[string]any
		want string
	}{
		{"missing", func(rows []map[string]any) []map[string]any { return rows[:1] }, "receipt count"},
		{"extra", func(rows []map[string]any) []map[string]any { return append(rows, rows[0]) }, "receipt count"},
		{"empty-nonempty-block", func([]map[string]any) []map[string]any { return []map[string]any{} }, "receipt count"},
		{"reordered", func(rows []map[string]any) []map[string]any { rows[0], rows[1] = rows[1], rows[0]; return rows }, "transaction identity or order"},
		{"duplicate", func(rows []map[string]any) []map[string]any { rows[1] = rows[0]; return rows }, "transaction identity or order"},
		{"wrong-block", func(rows []map[string]any) []map[string]any {
			rows[0]["blockHash"] = common.HexToHash("0xffff").Hex()
			rows[0]["logs"].([]map[string]any)[0]["blockHash"] = rows[0]["blockHash"]
			return rows
		}, "block identity"},
		{"wrong-height", func(rows []map[string]any) []map[string]any { rows[0]["blockNumber"] = "0x2b"; return rows }, "block identity"},
		{"wrong-transaction", func(rows []map[string]any) []map[string]any {
			rows[0]["transactionHash"] = common.HexToHash("0xffff").Hex()
			rows[0]["logs"].([]map[string]any)[0]["transactionHash"] = rows[0]["transactionHash"]
			return rows
		}, "transaction identity or order"},
		{"wrong-index", func(rows []map[string]any) []map[string]any { rows[0]["transactionIndex"] = "0x1"; return rows }, "transaction identity or order"},
		{"wrong-type", func(rows []map[string]any) []map[string]any { rows[1]["type"] = "0x0"; return rows }, "transaction type"},
		{"null-row", func(rows []map[string]any) []map[string]any { rows[0] = nil; return rows }, "invalid payload"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			header, txs, rows := commitmentFixture(t, false, types.LegacyTxType, types.DynamicFeeTxType)
			canonical, err := VerifyBlockCommitment(header, txs, commitmentJSON(t, tc.edit(rows)))
			if canonical != nil || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %s, %v; want %s", canonical, err, tc.want)
			}
		})
	}
}

func TestVerifyBlockCommitmentRejectsInvalidInputsAndRoot(t *testing.T) {
	for _, raw := range []string{"null", "{}", `"[]"`} {
		t.Run(raw, func(t *testing.T) {
			header, txs, _ := commitmentFixture(t, false)
			if canonical, err := VerifyBlockCommitment(header, txs, []byte(raw)); canonical != nil || err == nil {
				t.Fatalf("invalid list accepted: %s, %v", canonical, err)
			}
		})
	}
	for _, name := range []string{"nil-header", "nil-height", "negative-height", "nil-transaction", "wrong-empty-root", "wrong-root"} {
		t.Run(name, func(t *testing.T) {
			header, txs, rows := commitmentFixture(t, false, types.LegacyTxType)
			switch name {
			case "nil-header":
				header = nil
			case "nil-height":
				header.Number = nil
			case "negative-height":
				header.Number = big.NewInt(-1)
			case "nil-transaction":
				txs[0] = nil
			case "wrong-empty-root":
				txs = nil
				rows = []map[string]any{}
			case "wrong-root":
				header.ReceiptHash = common.HexToHash("0xffff")
				rows[0]["blockHash"] = header.Hash().Hex()
				rows[0]["logs"].([]map[string]any)[0]["blockHash"] = rows[0]["blockHash"]
			}
			if canonical, err := VerifyBlockCommitment(header, txs, commitmentJSON(t, rows)); canonical != nil || err == nil {
				t.Fatalf("invalid input accepted: %s, %v", canonical, err)
			}
		})
	}
}

func TestVerifyBlockCommitmentRejectsAlteredConsensusContent(t *testing.T) {
	for _, field := range []string{"status", "cumulativeGasUsed", "log-data", "log-address", "log-topics"} {
		t.Run(field, func(t *testing.T) {
			header, txs, rows := commitmentFixture(t, false, types.DynamicFeeTxType)
			log := rows[0]["logs"].([]map[string]any)[0]
			switch field {
			case "status":
				rows[0][field] = "0x0"
			case "cumulativeGasUsed":
				rows[0][field] = "0x5209"
			case "log-data":
				log["data"] = "0xffff"
			case "log-address":
				log["address"] = common.HexToAddress("0xffff").Hex()
			case "log-topics":
				log["topics"] = []string{common.HexToHash("0xffff").Hex()}
			}
			// Keep the altered log and bloom self-consistent, so the rejection
			// exercises the block commitment rather than the shared bloom gate.
			var logs []*types.Log
			if err := json.Unmarshal(commitmentJSON(t, rows[0]["logs"]), &logs); err != nil {
				t.Fatal(err)
			}
			bloom := types.CreateBloom(&types.Receipt{Logs: logs})
			rows[0]["logsBloom"] = hexutil.Encode(bloom.Bytes())
			canonical, err := VerifyBlockCommitment(header, txs, commitmentJSON(t, rows))
			if canonical != nil || err == nil || !strings.Contains(err.Error(), "receipts root mismatch") {
				t.Fatalf("consensus tampering accepted: %s, %v", canonical, err)
			}
		})
	}
}

func TestVerifyBlockCommitmentPreservesPresenceAndInputOwnership(t *testing.T) {
	header, txs, rows := commitmentFixture(t, false, types.BlobTxType)
	delete(rows[0], "type")
	delete(rows[0], "contractAddress")
	rows[0]["to"] = nil
	rows[0]["blobGasUsed"], rows[0]["blobGasPrice"] = "0x20000", "0x1"
	rows[0]["logs"].([]map[string]any)[0]["blockTimestamp"] = "0x7b"
	raw := commitmentJSON(t, rows)
	rawBefore := append([]byte(nil), raw...)
	headerBefore, txsBefore := commitmentJSON(t, header), commitmentJSON(t, txs)
	canonical, err := VerifyBlockCommitment(header, txs, raw)
	if err != nil {
		t.Fatal(err)
	}
	var checked []map[string]json.RawMessage
	if err := json.Unmarshal(canonical, &checked); err != nil {
		t.Fatal(err)
	}
	if _, present := checked[0]["type"]; present {
		t.Fatal("private transaction type leaked into official presence")
	}
	if _, present := checked[0]["contractAddress"]; present || string(checked[0]["to"]) != "null" || string(checked[0]["blobGasPrice"]) != `"0x1"` {
		t.Fatalf("official presence changed: %s", canonical)
	}
	if !bytes.Contains(canonical, []byte(`"blockTimestamp":"0x7b"`)) {
		t.Fatalf("optional log timestamp lost: %s", canonical)
	}
	canonical[0] = '!'
	if !bytes.Equal(raw, rawBefore) || !bytes.Equal(headerBefore, commitmentJSON(t, header)) || !bytes.Equal(txsBefore, commitmentJSON(t, txs)) {
		t.Fatal("verification or returned bytes mutated caller inputs")
	}
}

func TestVerifyBlockCommitmentDoesNotProveNonConsensusFields(t *testing.T) {
	for _, field := range []string{"from", "gasUsed", "effectiveGasPrice", "to", "contractAddress", "blobGasUsed", "blobGasPrice", "log-context"} {
		t.Run(field, func(t *testing.T) {
			header, txs, rows := commitmentFixture(t, false, types.BlobTxType)
			switch field {
			case "from", "to", "contractAddress":
				rows[0][field] = common.HexToAddress("0xffff").Hex()
			case "log-context":
				log := rows[0]["logs"].([]map[string]any)[0]
				log["blockNumber"], log["transactionIndex"], log["logIndex"], log["blockTimestamp"], log["removed"] = "0xffff", "0xff", "0xff", "0xff", true
			default:
				rows[0][field] = "0xffff"
			}
			raw := commitmentJSON(t, rows)
			canonical, err := VerifyBlockCommitment(header, txs, raw)
			if err != nil {
				t.Fatalf("root check unexpectedly claims a non-consensus policy: %v", err)
			}
			want, err := EncodeBlock(raw)
			if err != nil || !bytes.Equal(canonical, want) {
				t.Fatalf("unproved official declaration was overwritten: %s, %v", canonical, err)
			}
		})
	}
}

func TestVerifyBlockCommitmentKeepsExtraStatusOutsideRootProof(t *testing.T) {
	header, txs, rows := commitmentFixture(t, true, types.LegacyTxType)
	for _, status := range []string{"", "0x0", "0x1"} {
		t.Run("status-"+status, func(t *testing.T) {
			if status != "" {
				rows[0]["status"] = status
			}
			raw := commitmentJSON(t, rows)
			canonical, err := VerifyBlockCommitment(header, txs, raw)
			want, encodeErr := EncodeBlock(raw)
			if err != nil || encodeErr != nil || !bytes.Equal(canonical, want) {
				t.Fatalf("root branch/presence = %s, %v, %v", canonical, err, encodeErr)
			}
		})
	}
	rows[0]["root"] = common.HexToHash("0xffff").Hex()
	canonical, err := VerifyBlockCommitment(header, txs, commitmentJSON(t, rows))
	if canonical != nil || err == nil || !strings.Contains(err.Error(), "receipts root mismatch") {
		t.Fatalf("altered committed root accepted: %s, %v", canonical, err)
	}
}
