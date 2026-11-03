package runtime

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/codermaybe/ethguard/rpctest"
)

// logsChainFor is the hand-written getLogs chain the equivalence comparison
// drives, bound to the same stage the production bundle owns.
func logsChainFor(t *testing.T, stage *ReferenceStage) rpc.Policy {
	t.Helper()
	return methods.LogsPolicy(stage.AnchorHeightReader())
}

// The eth_getLogs plan-and-chain equivalence matrix. getLogs carries the
// field-anchored reference shape: the filter's fromBlock/toBlock ride moving
// tags or provider defaults, a blockHash pin excludes the range, and pending
// bounds are rejected by the method's own validate hook before any
// establishment. Every scenario runs the same scripted fleet through the
// plan route and the chain route, and the two outcomes must agree.

const logsHeadHeight = 100

var logsHeadHash = "0x" + fmt.Sprintf("%064x", 100)

// validLog builds one log row the schema admission accepts, bound to the
// scripted head.
func validLog() map[string]any {
	return map[string]any{
		"address":          "0x" + fmt.Sprintf("%040x", 1),
		"topics":           []any{"0x" + fmt.Sprintf("%064x", 7)},
		"data":             "0xdeadbeef",
		"blockNumber":      fmt.Sprintf("0x%x", logsHeadHeight),
		"transactionHash":  "0x" + fmt.Sprintf("%064x", 2),
		"transactionIndex": "0x1",
		"blockHash":        logsHeadHash,
		"logIndex":         "0x1",
		"removed":          false,
	}
}

func logsHeadHandler(tag string) (any, error) {
	if tag != "latest" && tag != "safe" && tag != "finalized" {
		return nil, fmt.Errorf("unexpected head tag %q", tag)
	}
	return map[string]any{
		"number": fmt.Sprintf("0x%x", logsHeadHeight),
		"hash":   logsHeadHash,
	}, nil
}

func logArray(rows ...map[string]any) []map[string]any {
	return rows
}

func TestPlannedLogsMatchChainOverTheFleet(t *testing.T) {
	scenarios := []struct {
		name   string
		params []any
		// answer builds the business answer; it observes the params the fan
		// out actually carried, so the fixture can assert the rewrite.
		answer func(t *testing.T, params []json.RawMessage) (any, error)
		// dissentNode, when in range, answers a different log instead.
		dissentNode int
		// failHeads fails the establishment round on every node.
		failHeads bool
	}{
		{
			name:   "moving range is pinned to the established height",
			params: []any{map[string]any{"fromBlock": "latest", "toBlock": "latest"}},
			answer: func(t *testing.T, params []json.RawMessage) (any, error) {
				var filter map[string]any
				if err := json.Unmarshal(params[0], &filter); err != nil {
					t.Fatal(err)
				}
				if filter["fromBlock"] != fmt.Sprintf("0x%x", logsHeadHeight) || filter["toBlock"] != fmt.Sprintf("0x%x", logsHeadHeight) {
					t.Fatalf("range was not pinned: %v", filter)
				}
				return logArray(validLog()), nil
			},
		},
		{
			name:   "missing bounds default to the established height",
			params: []any{map[string]any{"address": "0x" + fmt.Sprintf("%040x", 1)}},
			answer: func(t *testing.T, params []json.RawMessage) (any, error) {
				var filter map[string]any
				if err := json.Unmarshal(params[0], &filter); err != nil {
					t.Fatal(err)
				}
				if filter["fromBlock"] != fmt.Sprintf("0x%x", logsHeadHeight) || filter["toBlock"] != fmt.Sprintf("0x%x", logsHeadHeight) {
					t.Fatalf("bounds were not defaulted: %v", filter)
				}
				return logArray(validLog()), nil
			},
		},
		{
			name:   "explicit heights pass through without establishment",
			params: []any{map[string]any{"fromBlock": "0x63", "toBlock": "0x64"}},
			answer: func(t *testing.T, params []json.RawMessage) (any, error) {
				var filter map[string]any
				if err := json.Unmarshal(params[0], &filter); err != nil {
					t.Fatal(err)
				}
				if filter["fromBlock"] != "0x63" || filter["toBlock"] != "0x64" {
					t.Fatalf("explicit heights were rewritten: %v", filter)
				}
				return logArray(validLog()), nil
			},
		},
		{
			name:   "a blockHash pin passes through untouched",
			params: []any{map[string]any{"blockHash": logsHeadHash}},
			answer: func(t *testing.T, params []json.RawMessage) (any, error) {
				var filter map[string]any
				if err := json.Unmarshal(params[0], &filter); err != nil {
					t.Fatal(err)
				}
				if _, pinned := filter["blockHash"]; !pinned {
					t.Fatalf("hash pin was lost: %v", filter)
				}
				return logArray(validLog()), nil
			},
		},
		{
			name:   "dissent is a conflict",
			params: []any{map[string]any{"fromBlock": "latest", "toBlock": "latest"}},
			answer: func(t *testing.T, params []json.RawMessage) (any, error) {
				return logArray(validLog()), nil
			},
			dissentNode: 2,
		},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			script := func(t *testing.T, nodes []*rpctest.Node) {
				for i, node := range nodes {
					if scenario.failHeads {
						node.Fail("eth_getBlockByNumber", -32000, "head unavailable")
					} else {
						node.Handle("eth_getBlockByNumber", func(call rpctest.Call) (any, error) {
							tag := "latest"
							if len(call.Params) > 0 {
								_ = json.Unmarshal(call.Params[0], &tag)
							}
							return logsHeadHandler(tag)
						})
					}
					answer, dissent := scenario.answer, i == scenario.dissentNode
					node.Handle("eth_getLogs", func(call rpctest.Call) (any, error) {
						if dissent {
							broken := validLog()
							broken["data"] = "0xcafebabe"
							return logArray(broken), nil
						}
						return answer(t, call.Params)
					})
				}
			}
			req := rpc.Request{Method: "eth_getLogs", Params: scenario.params}
			planDecision, planErr, chainDecision, chainErr := runPlanAndChain(t, script,
				func(stage *ReferenceStage) rpc.Policy { return logsChainFor(t, stage) }, req)
			comparePlanAndChain(t, planDecision, planErr, chainDecision, chainErr)
		})
	}

	t.Run("pending bound is rejected before establishment", func(t *testing.T) {
		script := func(t *testing.T, nodes []*rpctest.Node) {
			for _, node := range nodes {
				node.Handle("eth_getBlockByNumber", func(call rpctest.Call) (any, error) {
					return logsHeadHandler("latest")
				})
			}
		}
		req := rpc.Request{Method: "eth_getLogs", Params: []any{map[string]any{"fromBlock": "pending"}}}
		planDecision, planErr, _, chainErr := runPlanAndChain(t, script,
			func(stage *ReferenceStage) rpc.Policy { return logsChainFor(t, stage) }, req)
		if planErr == nil || planDecision.Status != rpc.Unknown {
			t.Fatalf("pending bound did not fail the planned read: %+v %v", planDecision, planErr)
		}
		if planErr.Error() != chainErr.Error() {
			t.Fatalf("rejection mismatch: plan %q chain %q", planErr, chainErr)
		}
	})

	t.Run("blockHash with a range bound is rejected locally", func(t *testing.T) {
		script := func(t *testing.T, nodes []*rpctest.Node) {
			for _, node := range nodes {
				node.Handle("eth_getBlockByNumber", func(call rpctest.Call) (any, error) {
					return logsHeadHandler("latest")
				})
			}
		}
		req := rpc.Request{Method: "eth_getLogs", Params: []any{map[string]any{"blockHash": logsHeadHash, "fromBlock": "0x1"}}}
		planDecision, planErr, _, chainErr := runPlanAndChain(t, script,
			func(stage *ReferenceStage) rpc.Policy { return logsChainFor(t, stage) }, req)
		if planErr == nil || planDecision.Status != rpc.Unknown {
			t.Fatalf("contradictory filter did not fail the planned read: %+v %v", planDecision, planErr)
		}
		if planErr.Error() != chainErr.Error() {
			t.Fatalf("rejection mismatch: plan %q chain %q", planErr, chainErr)
		}
	})

	t.Run("establishment failure keeps the fan-out at zero", func(t *testing.T) {
		served := map[string]bool{}
		script := func(t *testing.T, nodes []*rpctest.Node) {
			for i, node := range nodes {
				node.Fail("eth_getBlockByNumber", -32000, "head unavailable")
				node.Handle("eth_getLogs", func(call rpctest.Call) (any, error) {
					served[fmt.Sprintf("%d", i)] = true
					return logArray(validLog()), nil
				})
			}
		}
		req := rpc.Request{Method: "eth_getLogs", Params: []any{map[string]any{"fromBlock": "latest", "toBlock": "latest"}}}
		planDecision, planErr, _, chainErr := runPlanAndChain(t, script,
			func(stage *ReferenceStage) rpc.Policy { return logsChainFor(t, stage) }, req)
		if planErr == nil || planDecision.Status != rpc.Unknown {
			t.Fatalf("failed establishment did not fail the planned read: %+v %v", planDecision, planErr)
		}
		if planErr.Error() != chainErr.Error() {
			t.Fatalf("failure mismatch: plan %q chain %q", planErr, chainErr)
		}
		// The runner scripts a fresh fleet per route, so served business
		// calls on either route would mean the fan-out started. The scenario
		// comparison below already pins the decision shape; this asserts the
		// handler was never reached on the plan route by construction of the
		// scripted fleet: both routes' business reads never ran.
		if len(served) != 0 {
			t.Fatalf("the failed establishment still fanned out: %v", served)
		}
	})
}

// comparePlanAndChain is the shared decision comparison for the equivalence
// matrix: same error text, status, value, pin, support, faults, prepared
// request, reference facts, and projection presence.
func comparePlanAndChain(t *testing.T, planDecision rpc.Decision, planErr error, chainDecision rpc.Decision, chainErr error) {
	t.Helper()
	if (planErr == nil) != (chainErr == nil) {
		t.Fatalf("error mismatch: plan %v chain %v", planErr, chainErr)
	}
	if planErr != nil && planErr.Error() != chainErr.Error() {
		t.Fatalf("error text mismatch: plan %q chain %q", planErr, chainErr)
	}
	if planDecision.Status != chainDecision.Status {
		t.Fatalf("status mismatch: plan %v chain %v", planDecision.Status, chainDecision.Status)
	}
	if string(planDecision.Value) != string(chainDecision.Value) {
		t.Fatalf("value mismatch: plan %s chain %s", planDecision.Value, chainDecision.Value)
	}
	if planDecision.BlockRef != chainDecision.BlockRef {
		t.Fatalf("block pin mismatch: plan %q chain %q", planDecision.BlockRef, chainDecision.BlockRef)
	}
	if len(planDecision.Support) != len(chainDecision.Support) {
		t.Fatalf("support mismatch: plan %v chain %v", planDecision.Support, chainDecision.Support)
	}
	if len(planDecision.Faults) != len(chainDecision.Faults) {
		t.Fatalf("faults mismatch: plan %v chain %v", planDecision.Faults, chainDecision.Faults)
	}
	if !samePreparedRequest(planDecision.Prepared, chainDecision.Prepared) {
		t.Fatalf("prepared request mismatch: plan %v chain %v", planDecision.Prepared, chainDecision.Prepared)
	}
	if !sameReferenceFacts(planDecision.Reference, chainDecision.Reference) {
		t.Fatalf("reference mismatch:\nplan  %+v\nchain %+v", planDecision.Reference, chainDecision.Reference)
	}
	if (planDecision.Extensions == nil) != (chainDecision.Extensions == nil) {
		t.Fatalf("projection presence mismatch: plan %v chain %v", planDecision.Extensions, chainDecision.Extensions)
	}
}
