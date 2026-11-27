package ethguard_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/methods"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpctest"
)

func TestTypedHeaderByHashUsesHeaderOnlyRequest(t *testing.T) {
	client, nodes := fleet(t, 3)
	raw := fixtureBlock(t, 16)
	hash := ethapi.Hash(blockHashOf(t, raw))
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockByHash, raw)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	header, result, err := client.HeaderByHash(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	height, heightErr := rpc.ParseHexQuantity(string(header.Number))
	if header == nil || heightErr != nil || height != 16 || !result.IsCorroborated() {
		t.Fatalf("header=%v result=%+v", header, result.Decision)
	}
	for i, node := range nodes {
		for _, call := range node.Calls() {
			if call.Method != methods.MethodGetBlockByHash || len(call.Params) != 2 {
				continue
			}
			var full bool
			if err := json.Unmarshal(call.Params[1], &full); err != nil || full {
				t.Fatalf("node %d header request fullTx=%s", i, call.Params[1])
			}
		}
	}
}

func TestTypedHeaderByNumberMatchesViewAndRejectsPending(t *testing.T) {
	client, nodes := fleet(t, 3)
	raw := fixtureBlock(t, 17)
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockByNumber, raw)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	header, _, err := client.HeaderByNumber(ctx, big.NewInt(17))
	height, heightErr := rpc.ParseHexQuantity(string(header.Number))
	if err != nil || header == nil || heightErr != nil || height != 17 {
		t.Fatalf("root header=%v err=%v", header, err)
	}
	viewHeader, err := client.EthClient().HeaderByNumber(ctx, big.NewInt(17))
	if err != nil || viewHeader == nil || viewHeader.Hash != header.Hash {
		t.Fatalf("view header=%v err=%v", viewHeader, err)
	}

	before := make([]int, len(nodes))
	for i, node := range nodes {
		before[i] = len(node.Calls())
	}
	if got, _, err := client.HeaderByNumber(ctx, big.NewInt(-1)); got != nil || !errors.Is(err, ethguard.ErrInvalidRequest) {
		t.Fatalf("pending header=%v err=%v", got, err)
	}
	for i, node := range nodes {
		if len(node.Calls()) != before[i] {
			t.Fatalf("pending header contacted node %d", i)
		}
	}
}

func TestTypedHeaderMovingTagsPinHashAndCaptureReference(t *testing.T) {
	for _, tc := range []struct {
		name   string
		number *big.Int
		tag    string
		height int64
	}{
		{"default", nil, "latest", 16},
		{"latest", big.NewInt(-2), "latest", 16},
		{"safe", big.NewInt(-4), "safe", 17},
		{"finalized", big.NewInt(-3), "finalized", 18},
	} {
		for _, surface := range []string{"root", "view"} {
			t.Run(tc.name+"/"+surface, func(t *testing.T) {
				client, nodes := fleet(t, 2)
				raw := fixtureBlock(t, tc.height)
				hash := blockHashOf(t, raw)
				for _, node := range nodes {
					node.Handle(methods.MethodGetBlockByNumber, func(call rpctest.Call) (any, error) {
						if len(call.Params) != 2 || wireString(t, call.Params[0]) != tc.tag || string(call.Params[1]) != "false" {
							t.Errorf("anchor request = %s, want [%q,false]", call.Params, tc.tag)
							return nil, rpctest.RPCError(-32602, "unexpected anchor request")
						}
						return map[string]any{"number": fmt.Sprintf("0x%x", tc.height), "hash": hash}, nil
					})
					node.Handle(methods.MethodGetBlockByHash, func(call rpctest.Call) (any, error) {
						if len(call.Params) != 2 || wireString(t, call.Params[0]) != hash || string(call.Params[1]) != "false" {
							t.Errorf("header request = %s, want [%q,false]", call.Params, hash)
							return nil, rpctest.RPCError(-32602, "unexpected header request")
						}
						return raw, nil
					})
				}
				base, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				ctx, capture := ethguard.WithResult(base)
				var header *ethapi.Header
				var direct ethguard.CallResult
				var err error
				if surface == "root" {
					header, direct, err = client.HeaderByNumber(ctx, tc.number)
				} else {
					header, err = client.EthClient().HeaderByNumber(ctx, tc.number)
				}
				if err != nil || header == nil || string(header.Hash) != hash || string(header.Number) != fmt.Sprintf("0x%x", tc.height) {
					t.Fatalf("header = %+v err:%v, want pinned %s at %d", header, err, hash, tc.height)
				}
				result, published, captureErr := capture.Result()
				if !published || captureErr != nil || result.Err != nil || !result.IsCorroborated() || result.Decision.Reference == nil {
					t.Fatalf("capture = %+v published:%v err:%v", result, published, captureErr)
				}
				if surface == "root" && (direct.Err != nil || !reflect.DeepEqual(direct.Decision, result.Decision)) {
					t.Fatalf("root and capture differ: direct:%+v captured:%+v", direct, result)
				}
				ref := result.Decision.Reference
				wantTag := tc.tag
				if wantTag == "latest" {
					wantTag = ""
				}
				if ref.Tag != wantTag || ref.Hash != hash || ref.Height != uint64(tc.height) || ref.Status != rpc.Corroborated || ref.ObservedAt.IsZero() || len(ref.Support) != 2 {
					t.Fatalf("reference = %+v, want the %s establishment", ref, tc.tag)
				}
				for i, node := range nodes {
					var calls []string
					for _, call := range node.Calls() {
						if call.Method == methods.MethodGetBlockByNumber || call.Method == methods.MethodGetBlockByHash {
							calls = append(calls, call.Method)
						}
					}
					if !reflect.DeepEqual(calls, []string{methods.MethodGetBlockByNumber, methods.MethodGetBlockByHash}) {
						t.Fatalf("node %d calls = %v, want one establishment followed by one pinned read", i, calls)
					}
				}
			})
		}
	}
}

func TestTypedHeaderFailuresKeepReferenceAndCapture(t *testing.T) {
	for _, phase := range []string{"reference", "business"} {
		for _, surface := range []string{"root", "view"} {
			t.Run(phase+"/"+surface, func(t *testing.T) {
				client, nodes := fleet(t, 2)
				raw := fixtureBlock(t, 17)
				hash := blockHashOf(t, raw)
				for _, node := range nodes {
					if phase == "reference" {
						node.Fail(methods.MethodGetBlockByNumber, -32603, "reference unavailable")
					} else {
						node.Return(methods.MethodGetBlockByNumber, map[string]any{"number": "0x11", "hash": hash})
					}
					node.Fail(methods.MethodGetBlockByHash, -32603, "header unavailable")
				}
				base, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				ctx, capture := ethguard.WithResult(base)
				var header *ethapi.Header
				var direct ethguard.CallResult
				var err error
				if surface == "root" {
					header, direct, err = client.HeaderByNumber(ctx, big.NewInt(-4))
				} else {
					header, err = client.EthClient().HeaderByNumber(ctx, big.NewInt(-4))
				}
				if err == nil || header != nil {
					t.Fatalf("%s failure = header:%+v err:%v", phase, header, err)
				}
				result, published, captureErr := capture.Result()
				if !published || captureErr != nil || result.Err != err || result.Decision == nil || result.Decision.Reference == nil { //nolint:errorlint // The result must retain the exact error returned to the caller.
					t.Fatalf("failure capture = %+v published:%v err:%v, want business error %v and reference", result, published, captureErr, err)
				}
				if surface == "root" && (direct.Err != err || !reflect.DeepEqual(direct.Decision, result.Decision)) { //nolint:errorlint // The result must retain the exact error returned to the caller.
					t.Fatalf("root and capture differ: direct:%+v captured:%+v", direct, result)
				}
				d, ref := result.Decision, result.Decision.Reference
				if d.Status != rpc.Unknown || d.Value != nil || len(d.Support) != 0 || ref.Tag != "safe" || ref.ObservedAt.IsZero() || len(ref.Sources) != 2 {
					t.Fatalf("failure facts mixed reference and business: %+v reference:%+v", d, ref)
				}
				wantBusiness := 0
				if phase == "reference" {
					if ref.Status != rpc.Unknown || len(ref.Support) != 0 || len(ref.Faults) != 2 {
						t.Fatalf("failed reference lost its source facts: %+v", ref)
					}
				} else {
					wantBusiness = 1
					if ref.Status != rpc.Corroborated || ref.Hash != hash || ref.Height != 17 || len(ref.Support) != 2 {
						t.Fatalf("business failure lost successful reference: %+v", ref)
					}
				}
				for i, node := range nodes {
					anchorCalls, businessCalls := 0, 0
					for _, call := range node.Calls() {
						switch call.Method {
						case methods.MethodGetBlockByNumber:
							anchorCalls++
							if len(call.Params) != 2 || wireString(t, call.Params[0]) != "safe" || string(call.Params[1]) != "false" {
								t.Fatalf("failure anchor params = %s", call.Params)
							}
						case methods.MethodGetBlockByHash:
							businessCalls++
							if len(call.Params) != 2 || wireString(t, call.Params[0]) != hash || string(call.Params[1]) != "false" {
								t.Fatalf("failure business params = %s", call.Params)
							}
						}
					}
					if anchorCalls != 1 || businessCalls != wantBusiness {
						t.Fatalf("node %d calls = reference:%d business:%d, want 1/%d", i, anchorCalls, businessCalls, wantBusiness)
					}
				}
			})
		}
	}
}
