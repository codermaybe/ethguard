package typed

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/rpc"
)

func TestDecodeBlockHeaderOnlyPreservesNotFound(t *testing.T) {
	_, err := DecodeBlockHeaderOnly([]byte("null"))
	if !errors.Is(err, ethereum.NotFound) {
		t.Fatalf("error = %v, want ethereum.NotFound", err)
	}
}

func TestDecodeReceiptRejectsIncompleteEnvelope(t *testing.T) {
	_, err := DecodeReceipt([]byte(`{"transactionHash":"0x0"}`))
	if err == nil {
		t.Fatal("incomplete receipt unexpectedly decoded")
	}
}

func typedReceiptFixture(t *testing.T) json.RawMessage {
	t.Helper()
	txHash := common.HexToHash("0x" + strings.Repeat("11", 32))
	blockHash := common.HexToHash("0x" + strings.Repeat("22", 32))
	receipt := &types.Receipt{
		Status:            types.ReceiptStatusSuccessful,
		CumulativeGasUsed: 21_000,
		GasUsed:           21_000,
		EffectiveGasPrice: big.NewInt(1),
		TxHash:            txHash,
		BlockHash:         blockHash,
		BlockNumber:       big.NewInt(7),
		TransactionIndex:  0,
		Logs: []*types.Log{{
			Address:     common.HexToAddress("0x" + strings.Repeat("33", 20)),
			Topics:      []common.Hash{common.HexToHash("0x" + strings.Repeat("44", 32))},
			Data:        []byte{1, 2},
			TxHash:      txHash,
			BlockHash:   blockHash,
			BlockNumber: 7,
		}},
	}
	receipt.Bloom = types.CreateBloom(receipt)
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	obj["from"] = json.RawMessage(`"0x0000000000000000000000000000000000000001"`)
	delete(obj, "root")
	raw, err = json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestDecodeReceiptRejectsForeignLogBindings(t *testing.T) {
	for _, field := range []string{"transactionHash", "blockHash"} {
		t.Run(field, func(t *testing.T) {
			var obj map[string]json.RawMessage
			if err := json.Unmarshal(typedReceiptFixture(t), &obj); err != nil {
				t.Fatal(err)
			}
			var logs []map[string]json.RawMessage
			if err := json.Unmarshal(obj["logs"], &logs); err != nil {
				t.Fatal(err)
			}
			logs[0][field] = json.RawMessage(`"0x` + strings.Repeat("aa", 32) + `"`)
			encodedLogs, err := json.Marshal(logs)
			if err != nil {
				t.Fatal(err)
			}
			obj["logs"] = encodedLogs
			raw, err := json.Marshal(obj)
			if err != nil {
				t.Fatal(err)
			}
			_, err = DecodeReceipt(raw)
			if !errors.Is(err, rpc.ErrMalformedResponse) || !strings.Contains(err.Error(), "does not match receipt") {
				t.Fatalf("error = %v, want malformed log binding", err)
			}
		})
	}
}

func TestDecodeReceiptRejectsBloomThatDoesNotMatchLogs(t *testing.T) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(typedReceiptFixture(t), &obj); err != nil {
		t.Fatal(err)
	}
	obj["logsBloom"] = json.RawMessage(`"0x` + strings.Repeat("00", 256) + `"`)
	raw, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	_, err = DecodeReceipt(raw)
	if !errors.Is(err, rpc.ErrMalformedResponse) || !strings.Contains(err.Error(), "logsBloom disagrees") {
		t.Fatalf("error = %v, want malformed bloom", err)
	}
}

func TestDecodeReceiptEnvelopeBoundaries(t *testing.T) {
	for _, field := range []string{"from", "effectiveGasPrice", "transactionIndex", "blockNumber"} {
		for _, replacement := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`"bad"`)} {
			t.Run(field+string(replacement), func(t *testing.T) {
				var obj map[string]json.RawMessage
				if err := json.Unmarshal(typedReceiptFixture(t), &obj); err != nil {
					t.Fatal(err)
				}
				if replacement == nil {
					delete(obj, field)
				} else {
					obj[field] = replacement
				}
				raw, err := json.Marshal(obj)
				if err != nil {
					t.Fatal(err)
				}
				assertTypedReceiptRejected(t, raw)
			})
		}
	}
	for _, field := range []string{"type", "status", "root", "blobGasUsed", "blobGasPrice"} {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(typedReceiptFixture(t), &obj); err != nil {
			t.Fatal(err)
		}
		obj[field] = json.RawMessage(`null`)
		raw, err := json.Marshal(obj)
		if err != nil {
			t.Fatal(err)
		}
		assertTypedReceiptRejected(t, raw)
	}
	for _, field := range []string{"status", "root"} {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(typedReceiptFixture(t), &obj); err != nil {
			t.Fatal(err)
		}
		obj[field] = json.RawMessage(`"0x2"`)
		raw, err := json.Marshal(obj)
		if err != nil {
			t.Fatal(err)
		}
		assertTypedReceiptRejected(t, raw)
	}
	for _, kind := range []string{"0x0", "0xff", "0x100"} {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(typedReceiptFixture(t), &obj); err != nil {
			t.Fatal(err)
		}
		obj["type"], _ = json.Marshal(kind)
		raw, err := json.Marshal(obj)
		if err != nil {
			t.Fatal(err)
		}
		if kind == "0x100" {
			assertTypedReceiptRejected(t, raw)
			continue
		}
		if _, err := DecodeReceipt(raw); err != nil {
			t.Fatalf("type %s: %v", kind, err)
		}
		if _, err := DecodeReceipts(append(append([]byte{'['}, raw...), ']')); err != nil {
			t.Fatalf("array type %s: %v", kind, err)
		}
	}
}

func assertTypedReceiptRejected(t *testing.T, raw json.RawMessage) {
	t.Helper()
	if _, err := DecodeReceipt(raw); !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("DecodeReceipt = %v for %s", err, raw)
	}
	if _, err := DecodeReceipts(append(append([]byte{'['}, raw...), ']')); !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("DecodeReceipts = %v for %s", err, raw)
	}
}

// entryUncleFixture builds a complete official block JSON carrying one uncle
// header plus the same-source evidence array, using geth as the independent
// hash oracle (the block's declared hash and the uncle root are computed by
// geth, never by the decoder under test).
func entryUncleFixture(t *testing.T, number int64) (block json.RawMessage, evidence json.RawMessage, uncleHash common.Hash) {
	t.Helper()
	uncle := &types.Header{
		ParentHash:  common.HexToHash("0x" + strings.Repeat("77", 32)),
		UncleHash:   types.EmptyUncleHash,
		Root:        common.HexToHash("0x" + strings.Repeat("78", 32)),
		TxHash:      types.EmptyTxsHash,
		ReceiptHash: common.HexToHash("0x" + strings.Repeat("79", 32)),
		Difficulty:  big.NewInt(1),
		Number:      big.NewInt(number - 1),
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
		Number:      big.NewInt(number),
		GasLimit:    30_000_000,
		Time:        5,
	}
	blockHash := types.NewBlockWithHeader(head).Hash()
	raw, err := head.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	obj["hash"] = json.RawMessage(fmt.Sprintf("%q", blockHash.Hex()))
	obj["size"] = json.RawMessage(`"0x500"`)
	obj["transactions"] = json.RawMessage(`[]`)
	uncleHashes, err := json.Marshal([]string{uncle.Hash().Hex()})
	if err != nil {
		t.Fatal(err)
	}
	obj["uncles"] = uncleHashes
	// Mirror the shared optional-null folding so the fixture spells the
	// admitted canonical shape (geth writes unset optional members as null;
	// the committed canonical form omits them).
	for _, name := range []string{"baseFeePerGas", "withdrawalsRoot", "blobGasUsed", "excessBlobGas", "parentBeaconBlockRoot", "requestsHash", "blockAccessListHash", "slotNumber"} {
		if value, ok := obj[name]; ok && string(value) == "null" {
			delete(obj, name)
		}
	}
	block, err = json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	uncleRaw, err := uncle.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	evidence, err = json.Marshal([]json.RawMessage{json.RawMessage(uncleRaw)})
	if err != nil {
		t.Fatal(err)
	}
	return block, evidence, uncle.Hash()
}

// TestDecodeBlockWithEvidenceAdmitsSameSourceUncles pins the evidence path of
// the direct entry: non-empty uncles without evidence fail, evidence with a
// foreign uncle hash fails, and matching same-source evidence admits the
// historical block.
func TestDecodeBlockWithEvidenceAdmitsSameSourceUncles(t *testing.T) {
	block, evidence, uncleHash := entryUncleFixture(t, 16)

	if _, err := DecodeBlockWithEvidence(block, nil); err == nil || !strings.Contains(err.Error(), "uncle headers were not admitted") {
		t.Fatalf("uncles without evidence = %v", err)
	}
	foreign, err := json.Marshal([]json.RawMessage{json.RawMessage(`{"parentHash":"0x` + strings.Repeat("99", 32) + `","sha3Uncles":"` + types.EmptyUncleHash.Hex() + `","stateRoot":"0x` + strings.Repeat("9a", 32) + `","transactionsRoot":"` + types.EmptyTxsHash.Hex() + `","receiptsRoot":"0x` + strings.Repeat("9b", 32) + `","difficulty":"0x1","number":"0xf","gasLimit":"0x1c9c380","gasUsed":"0x0","timestamp":"0x4","extraData":"0x","mixHash":"0x` + strings.Repeat("9c", 32) + `","nonce":"0x0000000000000000","logsBloom":"0x` + strings.Repeat("00", 256) + `","miner":"0x` + strings.Repeat("00", 20) + `","hash":"0x` + strings.Repeat("9d", 32) + `"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeBlockWithEvidence(block, foreign); err == nil || !strings.Contains(err.Error(), "uncle 0 hash mismatch") {
		t.Fatalf("foreign uncle evidence = %v", err)
	}
	value, err := DecodeBlockWithEvidence(block, evidence)
	if err != nil {
		t.Fatalf("matching same-source evidence rejected: %v", err)
	}
	if len(value.Uncles) != 1 || value.Uncles[0] != ethapi.Hash(uncleHash.Hex()) {
		t.Fatalf("admitted block uncles = %v, want [%s]", value.Uncles, uncleHash.Hex())
	}
}

// TestDecodeBlockAdmittedAcceptsUncleBearingCanonicalValue pins the
// projection split: the admitted path consumes the canonical value the
// object owner committed — uncle hash list present, uncle header bodies
// intentionally absent — while the direct raw entry still demands evidence
// for the same shape.
func TestDecodeBlockAdmittedAcceptsUncleBearingCanonicalValue(t *testing.T) {
	block, _, _ := entryUncleFixture(t, 16)
	admitted, err := DecodeBlockAdmitted(block)
	if err != nil {
		t.Fatalf("admitted projection rejected an uncle-bearing canonical value: %v", err)
	}
	if len(admitted.Uncles) != 1 {
		t.Fatalf("admitted uncles = %v", admitted.Uncles)
	}
	if _, err := DecodeBlock(block); err == nil {
		t.Fatal("direct entry accepted an uncle-bearing value without evidence")
	}
}
