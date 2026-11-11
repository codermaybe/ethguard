package typed

import (
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

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
