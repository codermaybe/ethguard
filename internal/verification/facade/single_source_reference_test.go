package ethguard_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpctest"
)

const singleSourceAccount = ethapi.Address("0x0000000000000000000000000000000000000001")

type singleSourceSelector struct {
	name   string
	tag    string
	number *big.Int
	height int64
}

func singleSourceSelectors() []singleSourceSelector {
	return []singleSourceSelector{
		{name: "nil", tag: "latest", height: 20},
		{name: "latest", tag: "latest", number: big.NewInt(-2), height: 20},
		{name: "safe", tag: "safe", number: big.NewInt(-4), height: 18},
		{name: "finalized", tag: "finalized", number: big.NewInt(-3), height: 16},
	}
}

// singleSourceReferencePeer uses the existing independent HTTP/WebSocket peer
// and records requests at the actual wire boundary, not an internal plan seam.
func singleSourceReferencePeer(t *testing.T, ws bool, answer func(sourceWireRequest) string) (*sourceWirePeer, func() []sourceWireRequest) {
	t.Helper()
	var mu sync.Mutex
	var calls []sourceWireRequest
	peer := newSourceWirePeer(t, ws, func(req sourceWireRequest) string {
		mu.Lock()
		calls = append(calls, req)
		mu.Unlock()
		return answer(req)
	})
	return peer, func() []sourceWireRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]sourceWireRequest(nil), calls...)
	}
}

func newSingleSourceReferenceClient(t *testing.T, endpoint string) *ethguard.Client {
	t.Helper()
	cfg, err := config.FromEndpoints(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	client, err := ethguard.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	if client.Mode() != rpc.CorroborationForwarded || client.VotingMembers() != 1 {
		t.Fatalf("single-source client mode=%s members=%d", client.Mode(), client.VotingMembers())
	}
	return client
}

func assertSingleSourceReference(t *testing.T, result ethguard.CallResult, selector singleSourceSelector, hash string) {
	t.Helper()
	if result.Decision == nil || result.Decision.Status != rpc.Forwarded || result.IsCorroborated() {
		t.Fatalf("single-source result claims incorrect confidence: %+v", result)
	}
	ref := result.Decision.Reference
	if ref == nil {
		t.Fatal("business result omitted its reference")
	}
	wantTag := selector.tag
	if wantTag == "latest" {
		wantTag = ""
	}
	if ref.Status != rpc.Forwarded || ref.Hash != hash || ref.Height != uint64(selector.height) || ref.Tag != wantTag || ref.ObservedAt.IsZero() {
		t.Fatalf("business reference = %+v, want Forwarded %s/%d for %s", ref, hash, selector.height, selector.tag)
	}
	if ref.Adjudication.Basis != 1 || ref.Adjudication.Required != 1 || len(ref.Support) != 1 || len(ref.Reject) != 0 {
		t.Fatalf("single-source reference invented or lost membership: %+v", ref)
	}
}

// A single provider can establish a request snapshot without proving peer
// agreement. Each public read must use its tag's snapshot and report Forwarded.
func TestSingleSourceMovingReadsPinForwardedReference(t *testing.T) {
	vector := readSimulateVector(t, "ethSimulate-empty.io")
	msg, err := ethapi.NewCallMsg(json.RawMessage(`{"to":"0x0000000000000000000000000000000000000001"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, ws := range []bool{false, true} {
		transport := "HTTP"
		if ws {
			transport = "WSS"
		}
		for _, selector := range singleSourceSelectors() {
			for _, method := range []string{"BalanceAt", "HeaderByNumber", "BlockByNumber", "BlockNumber", "EstimateGas", "FilterLogs", "SimulateV1"} {
				if method == "BlockNumber" && selector.name != "nil" {
					continue // BlockNumber has no selector argument.
				}
				t.Run(transport+"/"+selector.name+"/"+method, func(t *testing.T) {
					raw := fixtureBlock(t, selector.height)
					hash := blockHashOf(t, raw)
					peer, recorded := singleSourceReferencePeer(t, ws, func(req sourceWireRequest) string {
						switch req.Method {
						case "eth_getBlockByNumber", "eth_getBlockByHash":
							return `"result":` + string(raw)
						case "eth_getBalance":
							return `"result":"0x2a"`
						case "eth_estimateGas":
							return `"result":"0x5208"`
						case "eth_getLogs":
							return `"result":[]`
						case "eth_simulateV1":
							return `"result":` + string(vector.result)
						default:
							t.Errorf("unexpected method %s", req.Method)
							return `"error":{"code":-32601,"message":"unexpected method"}`
						}
					})
					client := newSingleSourceReferenceClient(t, peer.endpoint)
					base, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					ctx, capture := ethguard.WithResult(base)
					var result ethguard.CallResult
					var callErr error
					switch method {
					case "BalanceAt":
						var value *big.Int
						value, result, callErr = client.BalanceAt(ctx, singleSourceAccount, selector.number)
						if callErr == nil && (value == nil || value.Cmp(big.NewInt(42)) != 0) {
							t.Fatalf("balance=%v, want 42", value)
						}
					case "HeaderByNumber":
						var header *ethapi.Header
						header, result, callErr = client.HeaderByNumber(ctx, selector.number)
						if callErr == nil && (header == nil || string(header.Hash) != hash) {
							t.Fatalf("header=%+v, want %s", header, hash)
						}
					case "BlockByNumber":
						var block *ethapi.Block
						block, result, callErr = client.BlockByNumber(ctx, selector.number)
						if callErr == nil && (block == nil || string(block.Hash) != hash) {
							t.Fatalf("block=%+v, want %s", block, hash)
						}
					case "BlockNumber":
						var height uint64
						height, result, callErr = client.BlockNumber(ctx)
						if callErr == nil && height != uint64(selector.height) {
							t.Fatalf("height=%d, want %d", height, selector.height)
						}
					case "EstimateGas":
						var gas uint64
						if selector.name == "nil" {
							gas, result, callErr = client.EstimateGas(ctx, msg)
						} else {
							gas, result, callErr = client.EstimateGasAtBlock(ctx, msg, selector.number)
						}
						if callErr == nil && gas != 21000 {
							t.Fatalf("gas=%d, want 21000", gas)
						}
					case "FilterLogs":
						queryJSON := `{}`
						if selector.name != "nil" {
							queryJSON = fmt.Sprintf(`{"fromBlock":%q,"toBlock":%q}`, selector.tag, selector.tag)
						}
						query := mustFilterQuery(t, queryJSON)
						var logs []ethapi.Log
						logs, result, callErr = client.FilterLogs(ctx, query)
						if callErr == nil && len(logs) != 0 {
							t.Fatalf("logs=%+v, want empty result", logs)
						}
					case "SimulateV1":
						var ref *ethapi.SimulateBlockReference
						if selector.name != "nil" {
							ref = simulateReference(t, fmt.Sprintf("%q", selector.tag))
						}
						var blocks []ethapi.SimulateBlock
						blocks, result, callErr = client.SimulateV1(ctx, vector.request, ref)
						if callErr == nil && !reflect.DeepEqual(blocks, vector.blocks) {
							t.Fatal("simulation result differs from the official fixture")
						}
					}
					if callErr != nil {
						t.Fatalf("single-source %s(%s): %v", method, selector.name, callErr)
					}
					assertSingleSourceReference(t, result, selector, hash)
					captured, done, captureErr := capture.Result()
					if !done || captureErr != nil || !reflect.DeepEqual(captured, result) {
						t.Fatalf("capture did not publish the one core operation: done=%v err=%v captured=%+v direct=%+v", done, captureErr, captured, result)
					}
					calls := recorded()
					if len(calls) != 2 || calls[0].Method != "eth_getBlockByNumber" || len(calls[0].Params) != 2 || string(calls[0].Params[0]) != fmt.Sprintf("%q", selector.tag) || string(calls[0].Params[1]) != "false" {
						t.Fatalf("read must establish its own tag then issue one pinned business request: %+v", calls)
					}
					business := calls[1]
					switch method {
					case "HeaderByNumber", "BlockByNumber", "BlockNumber":
						full := "true"
						if method == "HeaderByNumber" {
							full = "false"
						}
						if business.Method != "eth_getBlockByHash" || len(business.Params) != 2 || string(business.Params[0]) != fmt.Sprintf("%q", hash) || string(business.Params[1]) != full {
							t.Fatalf("block/header request lost the established hash: %+v", business)
						}
					case "FilterLogs":
						var filter map[string]string
						from := fmt.Sprintf("0x%x", selector.height)
						if selector.name == "nil" {
							from = "0x0" // The public typed query defaults to genesis..latest.
						}
						if business.Method != "eth_getLogs" || len(business.Params) != 1 || json.Unmarshal(business.Params[0], &filter) != nil || filter["fromBlock"] != from || filter["toBlock"] != fmt.Sprintf("0x%x", selector.height) {
							t.Fatalf("filter lost its height snapshot: %+v", business)
						}
					default:
						if len(business.Params) != 2 || string(business.Params[1]) != fmt.Sprintf("%q", hash) {
							t.Fatalf("business request did not use the established hash: %+v", business)
						}
					}
					if ws && peer.connections.Load() == 0 || !ws && peer.httpConnections.Load() == 0 {
						t.Fatal("test did not exercise the selected real transport")
					}
					if method == "BalanceAt" && selector.name == "nil" {
						preserved, done, err := capture.Result()
						if !done || err != nil || !reflect.DeepEqual(preserved, captured) {
							t.Fatal("reading capture did not preserve the original completion")
						}
					}
				})
			}
		}
	}
}

func TestSingleSourceStandaloneAnchorsStillRequireCorroboration(t *testing.T) {
	node := rpctest.NewNode(1)
	t.Cleanup(node.Close)
	node.ReturnRaw("eth_getBlockByNumber", fixtureBlock(t, 16))
	client := newSingleSourceReferenceClient(t, node.URL())
	calls := []struct {
		name string
		call func(context.Context) (ethguard.CallResult, error)
	}{
		{"hash", func(ctx context.Context) (ethguard.CallResult, error) {
			hash, result, err := client.AnchorBlockHash(ctx)
			if hash != "" {
				t.Errorf("failed standalone anchor returned %s", hash)
			}
			return result, err
		}},
		{"height", func(ctx context.Context) (ethguard.CallResult, error) {
			height, result, err := client.AnchorBlockHeight(ctx)
			if height != 0 {
				t.Errorf("failed standalone anchor returned %d", height)
			}
			return result, err
		}},
	}
	for _, tag := range []string{"latest", "safe", "finalized"} {
		calls = append(calls, struct {
			name string
			call func(context.Context) (ethguard.CallResult, error)
		}{tag, func(ctx context.Context) (ethguard.CallResult, error) {
			hash, result, err := client.AnchorBlockHashForTag(ctx, tag)
			if hash != "" {
				t.Errorf("failed %s standalone anchor returned %s", tag, hash)
			}
			return result, err
		}})
	}
	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result, err := tc.call(ctx)
			if !errors.Is(err, ethguard.ErrGuaranteeNotMet) || result.IsCorroborated() {
				t.Fatalf("standalone anchor result=%+v err=%v", result, err)
			}
		})
	}
}

func TestSingleSourcePinnedBusinessFailureKeepsReferenceAndCapture(t *testing.T) {
	for _, ws := range []bool{false, true} {
		t.Run(fmt.Sprintf("WSS=%v", ws), func(t *testing.T) {
			raw := fixtureBlock(t, 20)
			foreign := fixtureBlock(t, 21)
			hash := blockHashOf(t, raw)
			peer, _ := singleSourceReferencePeer(t, ws, func(req sourceWireRequest) string {
				if req.Method == "eth_getBlockByNumber" {
					return `"result":` + string(raw)
				}
				return `"result":` + string(foreign)
			})
			client := newSingleSourceReferenceClient(t, peer.endpoint)
			base, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ctx, capture := ethguard.WithResult(base)
			block, result, err := client.BlockByNumber(ctx, nil)
			if block != nil || !errors.Is(err, ethguard.ErrMalformedResponse) {
				t.Fatalf("foreign block=%+v err=%v, want malformed response", block, err)
			}
			ref := result.Decision.Reference
			if ref == nil || ref.Status != rpc.Forwarded || ref.Hash != hash || ref.Height != 20 {
				t.Fatalf("business failure lost its established reference: %+v", result)
			}
			captured, done, captureErr := capture.Result()
			if !done || captureErr != nil || !errors.Is(captured.Err, ethguard.ErrMalformedResponse) || !reflect.DeepEqual(captured.Decision, result.Decision) {
				t.Fatalf("failure capture=%+v done=%v err=%v", captured, done, captureErr)
			}
		})
	}
}

func TestSingleSourceMalformedReferenceNeverDispatchesBusiness(t *testing.T) {
	for _, raw := range []string{
		`{"number":"0x10","hash":"0xabc"}`,
		`{"hash":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
		`{"number":"0x10000000000000000","hash":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
	} {
		t.Run(raw, func(t *testing.T) {
			node := rpctest.NewNode(1)
			t.Cleanup(node.Close)
			node.ReturnRaw("eth_getBlockByNumber", json.RawMessage(raw))
			node.Return("eth_getBalance", "0x2a")
			client := newSingleSourceReferenceClient(t, node.URL())
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			value, result, err := client.BalanceAt(ctx, singleSourceAccount, nil)
			if err == nil || value != nil || result.IsCorroborated() {
				t.Fatalf("malformed reference passed: value=%v result=%+v err=%v", value, result, err)
			}
			if len(businessCalls(node.Calls(), "eth_getBalance")) != 0 {
				t.Fatal("malformed reference reached the business method")
			}
		})
	}
}

func TestMajorityReferenceDoesNotBecomeForwardedWhenSourcesFail(t *testing.T) {
	var endpoints []string
	var nodes []*rpctest.Node
	for i := range 3 {
		node := rpctest.NewNode(1)
		t.Cleanup(node.Close)
		endpoints = append(endpoints, node.URL())
		nodes = append(nodes, node)
		if i == 0 {
			node.ReturnRaw("eth_getBlockByNumber", fixtureBlock(t, 20))
		} else {
			node.Fail("eth_getBlockByNumber", -32000, "source unavailable")
		}
		node.Return("eth_getBalance", "0x2a")
	}
	client, err := ethguard.Dial(endpoints...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	value, result, err := client.BalanceAt(ctx, singleSourceAccount, nil)
	if err == nil || value != nil || result.IsCorroborated() || client.Mode() != rpc.CorroborationMajority || client.VotingMembers() != 3 {
		t.Fatalf("failed majority downgraded: value=%v result=%+v err=%v", value, result, err)
	}
	if result.Decision == nil || result.Decision.Status == rpc.Forwarded || result.Decision.Reference == nil || result.Decision.Reference.Status == rpc.Forwarded || result.Decision.Reference.Adjudication.Basis != 3 || result.Decision.Reference.Adjudication.Required != 2 {
		t.Fatalf("failure lost the frozen majority basis: %+v", result)
	}
	for _, node := range nodes {
		if len(businessCalls(node.Calls(), "eth_getBalance")) != 0 {
			t.Fatal("failed majority reference dispatched business read")
		}
	}
}
