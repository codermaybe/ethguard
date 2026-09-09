package ethadapter

import (
	"encoding/json"
	"errors"
	"math/big"
	"testing"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	gethrpc "github.com/ethereum/go-ethereum/rpc"
)

func TestToCallArgCarriesBlobAndAuthorizationFields(t *testing.T) {
	msg := ethereum.CallMsg{
		BlobGasFeeCap:     big.NewInt(7),
		BlobHashes:        []common.Hash{common.HexToHash("0x01")},
		AuthorizationList: []types.SetCodeAuthorization{},
	}
	arg := toCallArg(msg)
	encoded, err := json.Marshal(arg)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"maxFeePerBlobGas", "blobVersionedHashes", "authorizationList"} {
		if _, ok := wire[key]; !ok {
			t.Fatalf("missing %s from call argument: %s", key, encoded)
		}
	}
}

func TestDecodeCallResultPreservesGethRevertData(t *testing.T) {
	for _, data := range []string{
		"0x08c379a0", // Error(string)
		"0x4e487b71", // Panic(uint256)
		"0xdeadbeef", // custom error selector
		"0x",         // empty revert
	} {
		t.Run(data, func(t *testing.T) {
			_, err := decodeCallResult(json.RawMessage(`{"code":3,"message":"execution reverted","data":"` + data + `"}`))
			if err == nil {
				t.Fatal("expected revert error")
			}
			var revert *RevertError
			if !errors.As(err, &revert) {
				t.Fatalf("expected RevertError, got %T: %v", err, err)
			}
			var rpcErr gethrpc.Error
			if !errors.As(err, &rpcErr) || rpcErr.ErrorCode() != 3 {
				t.Fatalf("expected geth rpc.Error code 3, got %v", err)
			}
			var dataErr gethrpc.DataError
			if !errors.As(err, &dataErr) {
				t.Fatal("expected geth rpc.DataError")
			}
			if got, ok := ethclient.RevertErrorData(err); !ok || len(got) != len(revert.Data) {
				t.Fatalf("expected geth RevertErrorData compatibility, got %x (ok=%v)", got, ok)
			}
		})
	}
}
