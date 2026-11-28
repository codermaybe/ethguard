package ethguard_test

import (
	"encoding/json"
	"errors"
	"math/big"
	"reflect"
	"testing"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/methods"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpctest"
)

func TestCallRevertForwardedPublicDelivery(t *testing.T) {
	for _, tc := range []struct {
		name     string
		data     string
		wantData []byte
	}{
		{name: "missing"},
		{name: "null", data: `null`},
		{name: "empty", data: `"0x"`, wantData: []byte{}},
		{name: "hex", data: `"0xDEAD"`, wantData: []byte{0xde, 0xad}},
		{name: "structured", data: `{"reason":"nope"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := rpctest.NewNode(1)
			defer node.Close()
			node.FailWithData(methods.MethodCall, 3, "execution reverted", json.RawMessage(tc.data), tc.data != "")
			client, err := ethguard.Dial(node.URL())
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			wantValue := `{"code":3,"message":"execution reverted"`
			if tc.data != "" {
				wantValue += `,"data":` + tc.data
			}
			wantValue += `}`
			checkDecision := func(t *testing.T, decision *rpc.Decision) {
				t.Helper()
				if decision == nil || decision.Status != rpc.Forwarded || decision.Adjudication.Reason != rpc.ReasonAccepted {
					t.Fatalf("decision = %+v, want accepted Forwarded", decision)
				}
				if decision.Adjudication.Basis != 1 || decision.Adjudication.Required != 1 || len(decision.Support) != 1 || len(decision.Reject) != 0 {
					t.Fatalf("forwarded membership changed: %+v", decision)
				}
				if string(decision.Value) != wantValue || len(decision.DeletedFields) != 0 {
					t.Fatalf("value = %s, want %s; deleted = %v", decision.Value, wantValue, decision.DeletedFields)
				}
				if decision.Revert == nil || decision.Revert.Code != 3 || decision.Revert.Message != "execution reverted" {
					t.Fatalf("revert context = %+v", decision.Revert)
				}
				if len(decision.Sources) != 1 || !decision.Sources[0].Attempted || decision.Sources[0].Relation != rpc.VerdictSupported || decision.Sources[0].Answer.Kind != rpc.AnswerProtocolError || decision.Sources[0].Answer.Interpretation.State != rpc.InterpretationValidated {
					t.Fatalf("source facts = %+v", decision.Sources)
				}
			}
			t.Run("raw", func(t *testing.T) {
				result, err := client.Call(t.Context(), methods.MethodCall, map[string]any{}, "0x1")
				if err != nil || result.Err != nil {
					t.Fatalf("raw revert returned a pipeline failure: %v / %v", err, result.Err)
				}
				checkDecision(t, result.Decision)
			})
			t.Run("typed", func(t *testing.T) {
				data, result, err := client.CallContract(t.Context(), ethapi.CallMsg{}, big.NewInt(1))
				var revert *ethguard.RevertError
				if !errors.As(err, &revert) || !errors.Is(result.Err, err) || data != nil {
					t.Fatalf("typed revert = %x / %v / %v, want RevertError", data, err, result.Err)
				}
				checkDecision(t, result.Decision)
				if !reflect.DeepEqual(revert.Data, tc.wantData) || string(revert.DataRaw) != tc.data {
					t.Fatalf("revert data = %#v / %s, want %#v / %s", revert.Data, revert.DataRaw, tc.wantData, tc.data)
				}
			})
			calls := 0
			for _, call := range node.Calls() {
				switch call.Method {
				case methods.MethodCall:
					calls++
				case methods.MethodChainID:
				default:
					t.Fatalf("explicit-height forwarded call made unexpected request: %s", call.Method)
				}
			}
			if calls != 2 {
				t.Fatalf("eth_call requests = %d, want one per entry", calls)
			}
		})
	}
}
