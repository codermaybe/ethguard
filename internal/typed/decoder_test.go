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
