package runtime

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/codermaybe/ethguard/rpctest"
)

// The remaining read methods' plan-and-chain equivalence. After the four
// admission slices landed, the rest of the catalog migrates cluster by
// cluster; these fixtures prove one representative per execution branch the
// earlier slices had not yet driven end to end: median sampling with and
// without an anchor, local resolution, the hash-anchored pending reject, the
// height-anchored validated read, and the hash-pinned admitted read.

func TestPlannedMedianMatchesChainOverTheFleet(t *testing.T) {
	scenarios := []struct {
		name    string
		method  string
		answers []string
		want    rpc.Status
		value   string
	}{
		{
			name:    "gas prices resolve to the lower median",
			method:  "eth_gasPrice",
			answers: []string{"0x66", "0x64", "0x65"},
			want:    rpc.Selected,
			value:   `"0x65"`,
		},
		{
			name:    "tip suggestions resolve to the lower median",
			method:  "eth_maxPriorityFeePerGas",
			answers: []string{"0x64", "0x64", "0x65"},
			want:    rpc.Selected,
			value:   `"0x64"`,
		},
	}
	for _, scenario := range scenarios {
		scenario := scenario
		t.Run(scenario.name, func(t *testing.T) {
			script := func(t *testing.T, nodes []*rpctest.Node) {
				for i, node := range nodes {
					node.Return(scenario.method, scenario.answers[i])
				}
			}
			planDecision, planErr, chainDecision, chainErr := runPlanAndChain(t, script,
				func(stage *ReferenceStage) rpc.Policy { return medianChainFor(t, scenario.method) },
				rpc.Request{Method: scenario.method})
			comparePlanAndChain(t, planDecision, planErr, chainDecision, chainErr)
			if planDecision.Status != scenario.want || string(planDecision.Value) != scenario.value {
				t.Fatalf("median decision: %+v %v, want %v %s", planDecision.Status, planErr, scenario.want, scenario.value)
			}
		})
	}
}

func medianChainFor(t *testing.T, method string) rpc.Policy {
	t.Helper()
	switch method {
	case "eth_gasPrice":
		return methods.GasPricePolicy()
	case "eth_maxPriorityFeePerGas":
		return methods.MaxPriorityFeePerGasPolicy()
	}
	t.Fatalf("no median chain for %s", method)
	return nil
}

// The local class: one provider's own state, the first healthy source stands.
func TestPlannedLocalMatchesChainOverTheFleet(t *testing.T) {
	script := func(t *testing.T, nodes []*rpctest.Node) {
		for _, node := range nodes {
			node.Return("net_peerCount", "0x10")
		}
	}
	planDecision, planErr, chainDecision, chainErr := runPlanAndChain(t, script,
		func(stage *ReferenceStage) rpc.Policy { return methods.PeerCountPolicy() },
		rpc.Request{Method: "net_peerCount"})
	comparePlanAndChain(t, planDecision, planErr, chainDecision, chainErr)
	if planDecision.Status != rpc.Selected || string(planDecision.Value) != `"0x10"` {
		t.Fatalf("local decision: %+v %v", planDecision, planErr)
	}
}

// The hash-anchored pending reject: eth_getBlockReceipts anchors its block
// reference at position 0, and the pipeline owns the local pending
// rejection — no validate hook runs first.
func TestPlannedBlockReceiptsMatchChainOverTheFleet(t *testing.T) {
	validReceipt := func(blockHash string) map[string]any {
		return map[string]any{
			"transactionHash":   "0x" + fmt.Sprintf("%064x", 2),
			"transactionIndex":  "0x1",
			"blockHash":         blockHash,
			"blockNumber":       fmt.Sprintf("0x%x", logsHeadHeight),
			"cumulativeGasUsed": "0x5208",
			"gasUsed":           "0x5208",
			"logsBloom":         "0x" + strings.Repeat("0", 512),
			"status":            "0x1",
			"logs":              []any{},
		}
	}

	t.Run("moving tag pins and receipts bind to the block", func(t *testing.T) {
		script := func(t *testing.T, nodes []*rpctest.Node) {
			for _, node := range nodes {
				node.Handle("eth_getBlockByNumber", func(call rpctest.Call) (any, error) {
					return logsHeadHandler("latest")
				})
				node.Handle("eth_getBlockReceipts", func(call rpctest.Call) (any, error) {
					var ref string
					if len(call.Params) == 1 {
						_ = json.Unmarshal(call.Params[0], &ref)
					}
					if ref != logsHeadHash {
						t.Fatalf("block reference was not pinned: %q", ref)
					}
					return []any{validReceipt(logsHeadHash)}, nil
				})
			}
		}
		planDecision, planErr, chainDecision, chainErr := runPlanAndChain(t, script,
			func(stage *ReferenceStage) rpc.Policy { return methods.BlockReceiptsPolicy(stage.AnchorHashReader()) },
			rpc.Request{Method: "eth_getBlockReceipts", Params: []any{"latest"}})
		comparePlanAndChain(t, planDecision, planErr, chainDecision, chainErr)
		if planErr != nil || planDecision.Status != rpc.Corroborated || planDecision.BlockRef != logsHeadHash {
			t.Fatalf("pinned receipts read failed: %+v %v", planDecision, planErr)
		}
	})

	t.Run("pending is rejected before establishment", func(t *testing.T) {
		script := func(t *testing.T, nodes []*rpctest.Node) {
			for _, node := range nodes {
				node.Handle("eth_getBlockByNumber", func(call rpctest.Call) (any, error) {
					return logsHeadHandler("latest")
				})
				node.Handle("eth_getBlockReceipts", func(call rpctest.Call) (any, error) {
					return []any{validReceipt(logsHeadHash)}, nil
				})
			}
		}
		req := rpc.Request{Method: "eth_getBlockReceipts", Params: []any{"pending"}}
		planDecision, planErr, _, chainErr := runPlanAndChain(t, script,
			func(stage *ReferenceStage) rpc.Policy { return methods.BlockReceiptsPolicy(stage.AnchorHashReader()) }, req)
		if planErr == nil || planDecision.Status != rpc.Unknown {
			t.Fatalf("pending was not rejected: %+v %v", planDecision, planErr)
		}
		if planErr.Error() != chainErr.Error() {
			t.Fatalf("rejection mismatch: plan %q chain %q", planErr, chainErr)
		}
	})
}

// The height-anchored validated read: feeHistory's newestBlock cannot carry
// a hash, so the moving tag is rewritten to the established height, and the
// method's own validate and admission hooks gate the read.
func TestPlannedFeeHistoryMatchesChainOverTheFleet(t *testing.T) {
	feeHistory := map[string]any{
		"oldestBlock":   fmt.Sprintf("0x%x", logsHeadHeight-1),
		"baseFeePerGas": []any{"0x1", "0x2"},
		"gasUsedRatio":  []any{0.5},
	}
	script := func(t *testing.T, nodes []*rpctest.Node) {
		for _, node := range nodes {
			node.Handle("eth_getBlockByNumber", func(call rpctest.Call) (any, error) {
				return logsHeadHandler("latest")
			})
			node.Handle("eth_feeHistory", func(call rpctest.Call) (any, error) {
				var newest string
				if len(call.Params) > 1 {
					_ = json.Unmarshal(call.Params[1], &newest)
				}
				if newest != fmt.Sprintf("0x%x", logsHeadHeight) {
					t.Fatalf("newest block was not pinned to the height: %q", newest)
				}
				return feeHistory, nil
			})
		}
	}
	planDecision, planErr, chainDecision, chainErr := runPlanAndChain(t, script,
		func(stage *ReferenceStage) rpc.Policy { return methods.FeeHistoryPolicy(stage.AnchorHeightReader()) },
		rpc.Request{Method: "eth_feeHistory", Params: []any{"0x1", "latest", []any{}}})
	comparePlanAndChain(t, planDecision, planErr, chainDecision, chainErr)
	if planErr != nil || planDecision.Status != rpc.Corroborated || planDecision.BlockRef != fmt.Sprintf("0x%x", logsHeadHeight) {
		t.Fatalf("fee history read failed: %+v %v", planDecision, planErr)
	}
}

// The hash-pinned admitted read: the transaction hash addresses immutable
// content, so no establishment runs, and the receipt schema gates the
// response before it can vote.
func TestPlannedTransactionReceiptMatchesChainOverTheFleet(t *testing.T) {
	txHash := "0x" + fmt.Sprintf("%064x", 2)
	validReceipt := func() map[string]any {
		return map[string]any{
			"transactionHash":   txHash,
			"transactionIndex":  "0x1",
			"blockHash":         blockHeadHash,
			"blockNumber":       "0x64",
			"cumulativeGasUsed": "0x5208",
			"gasUsed":           "0x5208",
			"logsBloom":         "0x" + strings.Repeat("0", 512),
			"status":            "0x1",
			"logs":              []any{},
		}
	}

	t.Run("an agreed receipt is corroborated", func(t *testing.T) {
		script := func(t *testing.T, nodes []*rpctest.Node) {
			for _, node := range nodes {
				node.Handle("eth_getTransactionReceipt", func(call rpctest.Call) (any, error) {
					return validReceipt(), nil
				})
			}
		}
		planDecision, planErr, chainDecision, chainErr := runPlanAndChain(t, script,
			func(stage *ReferenceStage) rpc.Policy { return methods.TransactionReceiptPolicy() },
			rpc.Request{Method: "eth_getTransactionReceipt", Params: []any{txHash}})
		comparePlanAndChain(t, planDecision, planErr, chainDecision, chainErr)
		if planErr != nil || planDecision.Status != rpc.Corroborated {
			t.Fatalf("agreed receipt failed: %+v %v", planDecision, planErr)
		}
	})

	t.Run("a receipt bound to another transaction is rejected by admission", func(t *testing.T) {
		script := func(t *testing.T, nodes []*rpctest.Node) {
			for _, node := range nodes {
				node.Handle("eth_getTransactionReceipt", func(call rpctest.Call) (any, error) {
					return validReceipt(), nil
				})
			}
		}
		req := rpc.Request{Method: "eth_getTransactionReceipt", Params: []any{"0x" + fmt.Sprintf("%064x", 3)}}
		planDecision, planErr, _, chainErr := runPlanAndChain(t, script,
			func(stage *ReferenceStage) rpc.Policy { return methods.TransactionReceiptPolicy() }, req)
		if planErr == nil || planDecision.Status != rpc.Unknown {
			t.Fatalf("mismatched receipt did not fail the read: %+v %v", planDecision, planErr)
		}
		if planErr.Error() != chainErr.Error() {
			t.Fatalf("admission mismatch: plan %q chain %q", planErr, chainErr)
		}
	})
}

// The anchored median: estimateGas rides the same reference and pending
// shape as the state reads while sampling without clustering.
func TestPlannedEstimateGasMatchesChainOverTheFleet(t *testing.T) {
	script := func(t *testing.T, nodes []*rpctest.Node) {
		for _, node := range nodes {
			node.Handle("eth_getBlockByNumber", func(call rpctest.Call) (any, error) {
				return logsHeadHandler("latest")
			})
			node.Return("eth_estimateGas", "0x5208")
		}
	}
	planDecision, planErr, chainDecision, chainErr := runPlanAndChain(t, script,
		func(stage *ReferenceStage) rpc.Policy { return methods.EstimateGasPolicy(stage.AnchorHashReader()) },
		rpc.Request{Method: "eth_estimateGas", Params: []any{map[string]any{"to": "0x" + fmt.Sprintf("%040x", 1)}, "latest"}})
	comparePlanAndChain(t, planDecision, planErr, chainDecision, chainErr)
	if planErr != nil || planDecision.Status != rpc.Selected || planDecision.BlockRef != logsHeadHash {
		t.Fatalf("anchored median read failed: %+v %v", planDecision, planErr)
	}
}
