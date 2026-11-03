package runtime

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/codermaybe/ethguard/rpctest"
)

// The eth_call plan-and-chain equivalence matrix. call is the call-revert
// escape hatch: execution and revert data are separate comparison
// dimensions, a corroborated revert may delete an uncorroborated data field
// from one admitted envelope (rule 4e), and pending rides the node-local
// route with error bodies in the value domain. Every scenario runs the same
// scripted fleet through the plan route and the chain route.

var callHeadHash = "0x" + fmt.Sprintf("%064x", 100)

func callHeadHandler() func(call rpctest.Call) (any, error) {
	return func(call rpctest.Call) (any, error) {
		tag := "latest"
		if len(call.Params) > 0 {
			_ = json.Unmarshal(call.Params[0], &tag)
		}
		if tag != "latest" && tag != "safe" && tag != "finalized" {
			return nil, fmt.Errorf("unexpected head tag %q", tag)
		}
		return map[string]any{"number": "0x64", "hash": callHeadHash}, nil
	}
}

// sameDimensions compares method-specific dimension evidence without source
// identity: the fleets are separate, so only the shape must agree — the
// dimension names, their returned flag, and the size of every class.
func sameDimensions(a, b []rpc.DimensionEvidence) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Returned != b[i].Returned ||
			len(a[i].Support) != len(b[i].Support) ||
			len(a[i].Reject) != len(b[i].Reject) ||
			len(a[i].Abstain) != len(b[i].Abstain) {
			return false
		}
	}
	return true
}

// sameProvenance compares withheld evidence without source identity: the
// fleets are separate, so the withheld entries' data values are the facts.
func sameProvenance(a, b json.RawMessage) bool {
	type withheld struct {
		Source string          `json:"source"`
		Data   json.RawMessage `json:"data"`
	}
	unwrap := func(raw json.RawMessage) []withheld {
		var entries []withheld
		_ = json.Unmarshal(raw, &entries)
		if entries == nil {
			var envelope struct {
				Withheld []withheld `json:"withheld_revert_data"`
			}
			_ = json.Unmarshal(raw, &envelope)
			return envelope.Withheld
		}
		return entries
	}
	left, right := unwrap(a), unwrap(b)
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if string(left[i].Data) != string(right[i].Data) {
			return false
		}
	}
	return true
}

func TestPlannedCallMatchesChainOverTheFleet(t *testing.T) {
	revertEnvelope := func(t *testing.T, node *rpctest.Node, data json.RawMessage, present bool) {
		t.Helper()
		if data == nil && !present {
			node.Fail("eth_call", 3, "execution reverted")
			return
		}
		node.FailWithData("eth_call", 3, "execution reverted", data, present)
	}
	scenarios := []struct {
		name string
		// scriptCall wires one node's eth_call answer.
		scriptCall func(t *testing.T, node *rpctest.Node)
		params     []any
	}{
		{
			name: "successful return data agrees",
			scriptCall: func(t *testing.T, node *rpctest.Node) {
				node.Return("eth_call", "0xdeadbeef")
			},
			params: []any{map[string]any{"to": "0x" + fmt.Sprintf("%040x", 1)}, "latest"},
		},
		{
			name: "a corroborated revert keeps its data and context",
			scriptCall: func(t *testing.T, node *rpctest.Node) {
				revertEnvelope(t, node, json.RawMessage(`"0x08c379a0"`), true)
			},
			params: []any{map[string]any{"to": "0x" + fmt.Sprintf("%040x", 1)}, "latest"},
		},
		{
			name: "a revert without data stays an abstention",
			scriptCall: func(t *testing.T, node *rpctest.Node) {
				revertEnvelope(t, node, nil, false)
			},
			params: []any{map[string]any{"to": "0x" + fmt.Sprintf("%040x", 1)}, "latest"},
		},
		{
			// Every node asserts the revert; the data dimension splits empty,
			// concrete, and missing, so no data cluster reaches the
			// invocation quorum and rule 4e deletes the field. The per-node
			// wiring lives in the script below.
			name: "a short data cluster is deleted from one envelope",
			scriptCall: func(t *testing.T, node *rpctest.Node) {
				revertEnvelope(t, node, nil, false)
			},
			params: []any{map[string]any{"to": "0x" + fmt.Sprintf("%040x", 1)}, "latest"},
		},
		{
			name: "the object form pins the established hash",
			scriptCall: func(t *testing.T, node *rpctest.Node) {
				node.Return("eth_call", "0xdeadbeef")
			},
			params: []any{map[string]any{"to": "0x" + fmt.Sprintf("%040x", 1)}, map[string]any{"blockNumber": "safe"}},
		},
		{
			name: "pending resolves as a node-local observation",
			scriptCall: func(t *testing.T, node *rpctest.Node) {
				node.Return("eth_call", "0xbeef")
			},
			params: []any{map[string]any{"to": "0x" + fmt.Sprintf("%040x", 1)}, "pending"},
		},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			script := func(t *testing.T, nodes []*rpctest.Node) {
				for i, node := range nodes {
					node.Handle("eth_getBlockByNumber", callHeadHandler())
					if scenario.name == "a short data cluster is deleted from one envelope" {
						switch i {
						case 0:
							revertEnvelope(t, node, json.RawMessage(`"0x"`), true)
						case 1:
							revertEnvelope(t, node, json.RawMessage(`"0xdeadbeef"`), true)
						default:
							revertEnvelope(t, node, nil, false)
						}
						continue
					}
					scenario.scriptCall(t, node)
				}
			}
			req := rpc.Request{Method: "eth_call", Params: scenario.params}
			planDecision, planErr, chainDecision, chainErr := runPlanAndChain(t, script,
				func(stage *ReferenceStage) rpc.Policy { return methods.CallPolicy(stage.AnchorHashReader()) }, req)
			comparePlanAndChain(t, planDecision, planErr, chainDecision, chainErr)
			// The method-specific evidence: revert context, dimensions, and
			// rule-4e deletions are part of the decision both routes return.
			if fmt.Sprint(planDecision.Revert) != fmt.Sprint(chainDecision.Revert) {
				t.Fatalf("revert context mismatch: plan %v chain %v", planDecision.Revert, chainDecision.Revert)
			}
			if !sameDimensions(planDecision.Dimensions, chainDecision.Dimensions) {
				t.Fatalf("dimensions mismatch:\nplan  %v\nchain %v", planDecision.Dimensions, chainDecision.Dimensions)
			}
			if fmt.Sprint(planDecision.DeletedFields) != fmt.Sprint(chainDecision.DeletedFields) {
				t.Fatalf("deleted fields mismatch: plan %v chain %v", planDecision.DeletedFields, chainDecision.DeletedFields)
			}
			if !sameProvenance(planDecision.Provenance, chainDecision.Provenance) {
				t.Fatalf("provenance mismatch: plan %s chain %s", planDecision.Provenance, chainDecision.Provenance)
			}
			switch scenario.name {
			case "successful return data agrees":
				if planDecision.Status != rpc.Corroborated || string(planDecision.Value) != `"0xdeadbeef"` {
					t.Fatalf("unexpected success decision: %+v %v", planDecision, planErr)
				}
			case "a corroborated revert keeps its data and context":
				if planDecision.Status != rpc.Corroborated || planDecision.Revert == nil || planDecision.Revert.Code != 3 {
					t.Fatalf("unexpected revert decision: %+v %v", planDecision, planErr)
				}
			case "a revert without data stays an abstention":
				if planDecision.Status != rpc.Corroborated || planDecision.Revert == nil {
					t.Fatalf("unexpected abstaining revert: %+v %v", planDecision, planErr)
				}
			case "a short data cluster is deleted from one envelope":
				if planDecision.Status != rpc.Corroborated || len(planDecision.DeletedFields) == 0 {
					t.Fatalf("rule 4e did not delete the uncorroborated data: %+v %v", planDecision, planErr)
				}
			case "the object form pins the established hash":
				if planDecision.BlockRef != callHeadHash {
					t.Fatalf("object form did not pin the hash: %q", planDecision.BlockRef)
				}
			case "pending resolves as a node-local observation":
				if planDecision.Status != rpc.Selected || string(planDecision.Value) != `"0xbeef"` {
					t.Fatalf("pending observation claimed %v with value %s", planDecision.Status, planDecision.Value)
				}
			}
		})
	}

	t.Run("a minority success against a corroborated revert is recorded as dissent", func(t *testing.T) {
		script := func(t *testing.T, nodes []*rpctest.Node) {
			for i, node := range nodes {
				node.Handle("eth_getBlockByNumber", callHeadHandler())
				if i == 0 {
					node.Return("eth_call", "0xdeadbeef")
				} else {
					node.Fail("eth_call", 3, "execution reverted")
				}
			}
		}
		req := rpc.Request{Method: "eth_call", Params: []any{map[string]any{"to": "0x" + fmt.Sprintf("%040x", 1)}, "latest"}}
		planDecision, planErr, chainDecision, chainErr := runPlanAndChain(t, script,
			func(stage *ReferenceStage) rpc.Policy { return methods.CallPolicy(stage.AnchorHashReader()) }, req)
		comparePlanAndChain(t, planDecision, planErr, chainDecision, chainErr)
		if planErr != nil || planDecision.Status != rpc.Corroborated || planDecision.Revert == nil {
			t.Fatalf("majority-mode dissent did not corroborate the revert: %+v %v", planDecision, planErr)
		}
		if len(planDecision.Reject) != 1 {
			t.Fatalf("the dissenting source is not recorded as reject: %v", planDecision.Reject)
		}
	})

	t.Run("a split execution with an unusable provider failure is a conflict", func(t *testing.T) {
		script := func(t *testing.T, nodes []*rpctest.Node) {
			for i, node := range nodes {
				node.Handle("eth_getBlockByNumber", callHeadHandler())
				switch i {
				case 0:
					node.Return("eth_call", "0xdeadbeef")
				case 1:
					node.Fail("eth_call", 3, "execution reverted")
				default:
					// A provider failure that is not an execution observation:
					// it never enters a revert key and shrinks the winner.
					node.Fail("eth_call", -32603, "internal error")
				}
			}
		}
		req := rpc.Request{Method: "eth_call", Params: []any{map[string]any{"to": "0x" + fmt.Sprintf("%040x", 1)}, "latest"}}
		planDecision, planErr, chainDecision, chainErr := runPlanAndChain(t, script,
			func(stage *ReferenceStage) rpc.Policy { return methods.CallPolicy(stage.AnchorHashReader()) }, req)
		comparePlanAndChain(t, planDecision, planErr, chainDecision, chainErr)
		if planErr == nil || planDecision.Status != rpc.Unknown {
			t.Fatalf("a split execution did not fail the read: %+v %v", planDecision, planErr)
		}
	})

	t.Run("establishment failure keeps the fan-out at zero", func(t *testing.T) {
		script := func(t *testing.T, nodes []*rpctest.Node) {
			for _, node := range nodes {
				node.Fail("eth_getBlockByNumber", -32000, "head unavailable")
				node.Return("eth_call", "0xdeadbeef")
			}
		}
		req := rpc.Request{Method: "eth_call", Params: []any{map[string]any{"to": "0x" + fmt.Sprintf("%040x", 1)}, "latest"}}
		planDecision, planErr, _, chainErr := runPlanAndChain(t, script,
			func(stage *ReferenceStage) rpc.Policy { return methods.CallPolicy(stage.AnchorHashReader()) }, req)
		if planErr == nil || planDecision.Status != rpc.Unknown {
			t.Fatalf("failed establishment did not fail the planned read: %+v %v", planDecision, planErr)
		}
		if planErr.Error() != chainErr.Error() {
			t.Fatalf("failure mismatch: plan %q chain %q", planErr, chainErr)
		}
	})
}
