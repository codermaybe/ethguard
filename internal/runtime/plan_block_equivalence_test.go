package runtime

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/codermaybe/ethguard/rpctest"
)

// The eth_getBlockByNumber plan-and-chain equivalence matrix. The block
// method has no reference of its own: it is the header-only read the
// reference establishment itself rides, its pending form is a node-local
// observation, and its full form is admission-gated. The head codec's
// decoding must not depend on the compiled policy — the plan's resolver
// carries the same block contract.

var blockHeadHash = "0x" + fmt.Sprintf("%064x", 100)

func blockHeadOnly() map[string]any {
	return map[string]any{"number": "0x64", "hash": blockHeadHash}
}

func TestPlannedBlockByNumberMatchesChainOverTheFleet(t *testing.T) {
	scenarios := []struct {
		name   string
		params []any
		// answer builds one node's block answer.
		answer func(t *testing.T, fullTx bool) (any, error)
		// dissentNode answers a different head instead.
		dissentNode int
		// threeWaySplit gives every node its own head.
		threeWaySplit bool
	}{
		{
			name:   "header-only agreement keeps the source spelling",
			params: []any{"0x64", false},
			answer: func(t *testing.T, fullTx bool) (any, error) {
				return blockHeadOnly(), nil
			},
		},
		{
			name:   "pending is a node-local observation",
			params: []any{"pending", false},
			answer: func(t *testing.T, fullTx bool) (any, error) {
				return blockHeadOnly(), nil
			},
		},
		{
			name:   "a missing block agrees on null",
			params: []any{"0x999", false},
			answer: func(t *testing.T, fullTx bool) (any, error) {
				return nil, nil
			},
		},
		{
			name:   "a minority head dissent does not break corroboration",
			params: []any{"0x64", false},
			answer: func(t *testing.T, fullTx bool) (any, error) {
				return blockHeadOnly(), nil
			},
			dissentNode: 2,
		},
		{
			// Three different heads: no cluster reaches the floor, so the
			// read fails closed with every head recorded as a cluster.
			name:   "a three-way head split is a conflict",
			params: []any{"0x64", false},
			answer: func(t *testing.T, fullTx bool) (any, error) {
				return blockHeadOnly(), nil
			},
			threeWaySplit: true,
		},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			script := func(t *testing.T, nodes []*rpctest.Node) {
				for i, node := range nodes {
					answer, dissent := scenario.answer, i == scenario.dissentNode
					node.Handle("eth_getBlockByNumber", func(call rpctest.Call) (any, error) {
						fullTx := false
						if len(call.Params) > 1 {
							_ = json.Unmarshal(call.Params[1], &fullTx)
						}
						if scenario.threeWaySplit {
							other := blockHeadOnly()
							other["hash"] = "0x" + fmt.Sprintf("%064x", 900+i)
							return other, nil
						}
						if dissent {
							other := blockHeadOnly()
							other["hash"] = "0x" + fmt.Sprintf("%064x", 999)
							return other, nil
						}
						return answer(t, fullTx)
					})
				}
			}
			req := rpc.Request{Method: "eth_getBlockByNumber", Params: scenario.params}
			planDecision, planErr, chainDecision, chainErr := runPlanAndChain(t, script,
				func(stage *ReferenceStage) rpc.Policy { return methods.BlockByNumberPolicy() }, req)
			comparePlanAndChain(t, planDecision, planErr, chainDecision, chainErr)
			switch scenario.name {
			case "header-only agreement keeps the source spelling":
				if planDecision.Status != rpc.Corroborated || planDecision.Value == nil {
					t.Fatalf("unexpected header-only decision: %+v %v", planDecision, planErr)
				}
				// The header-only form keeps the representative source's own
				// spelling in Value while clustering on the canonical head.
				var value map[string]any
				if err := json.Unmarshal(planDecision.Value, &value); err != nil {
					t.Fatalf("header-only value is not the source spelling: %s", planDecision.Value)
				}
				if value["parentHash"] != nil {
					t.Fatalf("header-only value gained body fields: %s", planDecision.Value)
				}
			case "pending is a node-local observation":
				if planDecision.Status != rpc.Selected {
					t.Fatalf("pending observation claimed %v", planDecision.Status)
				}
			case "a missing block agrees on null":
				if planErr != nil || planDecision.Status != rpc.Corroborated {
					t.Fatalf("null agreement failed: %+v %v", planDecision, planErr)
				}
			case "a minority head dissent does not break corroboration":
				if planErr != nil || planDecision.Status != rpc.Corroborated || len(planDecision.Reject) != 1 {
					t.Fatalf("majority-mode dissent changed shape: %+v %v", planDecision, planErr)
				}
			case "a three-way head split is a conflict":
				if planErr == nil || planDecision.Status != rpc.Unknown {
					t.Fatalf("a three-way split did not fail the read: %+v %v", planDecision, planErr)
				}
			}
		})
	}

	t.Run("a malformed full block fails admission on both routes", func(t *testing.T) {
		script := func(t *testing.T, nodes []*rpctest.Node) {
			for _, node := range nodes {
				// A full-block request answered header-only: the body is
				// incomplete, so admission rejects it before clustering.
				node.Handle("eth_getBlockByNumber", func(call rpctest.Call) (any, error) {
					return blockHeadOnly(), nil
				})
			}
		}
		req := rpc.Request{Method: "eth_getBlockByNumber", Params: []any{"0x64", true}}
		planDecision, planErr, chainDecision, chainErr := runPlanAndChain(t, script,
			func(stage *ReferenceStage) rpc.Policy { return methods.BlockByNumberPolicy() }, req)
		comparePlanAndChain(t, planDecision, planErr, chainDecision, chainErr)
		if planErr == nil || planDecision.Status != rpc.Unknown {
			t.Fatalf("malformed full block did not fail the read: %+v %v", planDecision, planErr)
		}
	})
}

// The block plan's resolver carries the head codec's contract: the same
// comparison key and the same canonical head encoding, so the reference
// establishment decodes heads without consulting a compiled policy.
func TestBlockPlanResolverCarriesTheHeadCodecContract(t *testing.T) {
	var plan rpc.ReadPlan
	for _, spec := range methods.Specs() {
		if spec.Method == methods.MethodGetBlockByNumber {
			compiled, err := methods.CompilePlan(spec)
			if err != nil {
				t.Fatal(err)
			}
			plan = compiled
		}
	}
	if plan.Resolver == nil {
		t.Fatal("the inventory carries no eth_getBlockByNumber spec")
	}
	head := json.RawMessage(fmt.Sprintf(`{"number":"0x64","hash":%q}`, blockHeadHash))
	planKey, planErr := plan.Resolver.Normalize(head)
	policyKey, policyErr := methods.BlockByNumberPolicy().Normalize(head)
	if (planErr == nil) != (policyErr == nil) || planKey != policyKey {
		t.Fatalf("comparison key mismatch: plan %q/%v policy %q/%v", planKey, planErr, policyKey, policyErr)
	}
	planValue, planErr := rpc.CanonicalOf(plan.Resolver, head)
	policyValue, policyErr := rpc.CanonicalOf(methods.BlockByNumberPolicy(), head)
	if (planErr == nil) != (policyErr == nil) || string(planValue) != string(policyValue) {
		t.Fatalf("canonical value mismatch: plan %s/%v policy %s/%v", planValue, planErr, policyValue, policyErr)
	}
	if _, ok := plan.Resolver.(rpc.CorroboratingPolicy); !ok {
		t.Fatal("the block resolver lost its corroboration capability")
	}
}
