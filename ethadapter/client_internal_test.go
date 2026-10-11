package ethadapter

import (
	"encoding/json"
	"errors"
	"math/big"
	"testing"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
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
			if errors.As(err, &rpcErr) {
				t.Fatalf("RevertError must no longer expose provider rpc.Error context: %v", err)
			}
			var dataErr gethrpc.DataError
			if !errors.As(err, &dataErr) {
				t.Fatal("expected geth rpc.DataError")
			}
			if got, ok := dataErr.ErrorData().(string); revert.Data != nil && (!ok || got == "") {
				t.Fatalf("expected DataError data, got %#v (ok=%v)", dataErr.ErrorData(), ok)
			}
		})
	}
}

func TestDecodeCallResultKeepsRevertDataStates(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		wantNil bool
		wantHex string
		wantRaw string
	}{
		{"missing", `{"code":3,"message":"execution reverted"}`, true, "", ""},
		{"null", `{"code":3,"message":"execution reverted","data":null}`, true, "", "null"},
		{"empty", `{"code":3,"message":"execution reverted","data":"0x"}`, false, "0x", `"0x"`},
		{"hex", `{"code":3,"message":"execution reverted: nope","data":"0xdead"}`, false, "0xdead", `"0xdead"`},
		{"object", `{"code":3,"message":"execution reverted","data":{"reason":"nope"}}`, false, "", `{"reason":"nope"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeCallResult(json.RawMessage(tc.payload))
			var revert *RevertError
			if !errors.As(err, &revert) {
				t.Fatalf("got %T %v", err, err)
			}
			if tc.wantRaw == "" {
				if len(revert.DataRaw) != 0 {
					t.Fatalf("DataRaw = %s, want empty", revert.DataRaw)
				}
			} else if string(revert.DataRaw) != tc.wantRaw {
				t.Fatalf("DataRaw = %s, want %s", revert.DataRaw, tc.wantRaw)
			}
			if tc.wantHex == "" {
				if revert.Data != nil {
					t.Fatalf("Data = %x, want nil", revert.Data)
				}
			} else if hexutil.Encode(revert.Data) != tc.wantHex {
				t.Fatalf("Data = %s, want %s", hexutil.Encode(revert.Data), tc.wantHex)
			}
			got := revert.ErrorData()
			if tc.wantNil {
				if got != nil {
					t.Fatalf("ErrorData = %#v, want nil", got)
				}
			}
			if tc.name == "object" {
				asMap, ok := got.(map[string]any)
				if !ok || asMap["reason"] != "nope" {
					t.Fatalf("structured ErrorData = %#v", got)
				}
			}
		})
	}
}
