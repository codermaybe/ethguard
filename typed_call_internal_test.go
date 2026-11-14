package ethguard

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/typed"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/ethereum/go-ethereum/common/hexutil"
	gethrpc "github.com/ethereum/go-ethereum/rpc"
)

func TestToCallArgCarriesBlobAndAuthorizationFields(t *testing.T) {
	msg, err := ethapi.NewCallMsg(json.RawMessage(`{"maxFeePerBlobGas":"0x7","blobVersionedHashes":["0x0000000000000000000000000000000000000000000000000000000000000001"],"authorizationList":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	arg, err := toCallArg(msg)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(arg)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	want := map[string]json.RawMessage{
		"maxFeePerBlobGas":    json.RawMessage(`"0x7"`),
		"blobVersionedHashes": json.RawMessage(`["0x0000000000000000000000000000000000000000000000000000000000000001"]`),
		"authorizationList":   json.RawMessage(`[]`),
	}
	if !reflect.DeepEqual(wire, want) {
		t.Fatalf("call argument = %s, want exactly %+v", encoded, want)
	}
}

func TestToCallArgPreservesExplicitZeroNullAndAbsence(t *testing.T) {
	for _, source := range []string{`{}`, `{"to":null}`, `{"input":"0x0001","gas":"0x0","value":"0x0","accessList":[]}`} {
		msg, err := ethapi.NewCallMsg(json.RawMessage(source))
		if err != nil {
			t.Fatal(err)
		}
		arg, err := toCallArg(msg)
		if err != nil {
			t.Fatal(err)
		}
		var want map[string]any
		if err := json.Unmarshal([]byte(source), &want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(arg, want) {
			t.Fatalf("%s became %+v", source, arg)
		}
	}
}

func TestToCallArgRejectsInvalidDirectlyConstructedInputs(t *testing.T) {
	badAddress := ethapi.Address("0x1")
	badData := ethapi.Data("0xnothex")
	badQuantity := ethapi.Quantity("-1")
	for _, msg := range []ethapi.CallMsg{{From: &badAddress}, {Input: &badData}, {Gas: &badQuantity}} {
		if arg, err := toCallArg(msg); arg != nil || !errors.Is(err, rpc.ErrInvalidRequest) {
			t.Fatalf("invalid call = %+v, %v", arg, err)
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
			// The adapter's error semantics: RevertError carries the revert
			// context as a geth DataError without exposing a provider
			// rpc.Error.
			var target *RevertError
			if !errors.As(&RevertError{Data: hexutil.MustDecode(data), DataRaw: json.RawMessage(`"` + data + `"`)}, &target) || target == nil {
				t.Fatal("expected RevertError to satisfy errors.As")
			}
			var rpcErr gethrpc.Error
			if errors.As(target, &rpcErr) {
				t.Fatalf("RevertError must no longer expose provider rpc.Error context: %v", target)
			}
			var dataErr gethrpc.DataError
			if !errors.As(target, &dataErr) {
				t.Fatal("expected geth rpc.DataError")
			}
			if got, ok := dataErr.ErrorData().(string); target.Data != nil && (!ok || got == "") {
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
			_, revert, err := typed.DecodeCallResult(json.RawMessage(tc.payload))
			if err != nil || revert == nil {
				t.Fatalf("got %v %v, want a revert carrier", revert, err)
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
			// The adapter's ErrorData mapping rides the typed carrier through
			// the decision decoder.
			decision := rpc.Decision{
				Value:  json.RawMessage(tc.payload),
				Revert: &rpc.RevertContext{Code: 3, Message: "execution reverted"},
			}
			_, revertErr := decodeCallDecision(decision)
			var adapter *RevertError
			if !errors.As(revertErr, &adapter) {
				t.Fatalf("got %T %v, want RevertError", revertErr, revertErr)
			}
			got := adapter.ErrorData()
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
