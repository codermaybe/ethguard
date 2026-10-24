package ethguard_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

// T09.2 protected-field mutations are grouped by class. Each row names the
// method, the class, the mutated field, and the fault it must produce. A
// mutation never votes, never shrinks the fixed basis, and never causes a
// second vote from another source's fields.

type t09Mutation struct {
	class   string
	field   string
	payload json.RawMessage
	hint    string
	// sourceFault is true only for admission-stage faults; normalize and
	// encode faults are collected without per-source attribution.
	sourceFault bool
}

type t09MutationCase struct {
	method     string
	params     []any
	healthy    json.RawMessage
	other      json.RawMessage
	resolution t09Resolution
	mutations  []t09Mutation
}

func TestT092ProtectedMutationClasses(t *testing.T) {
	chain := t09NewChain(t)
	block, hash := chain.block(t09AnchorHeight, 1_700_000_000, 21_000)
	fork, _ := chain.block(t09AnchorHeight, 1_700_000_100, 42_000)
	nextBlock, _ := chain.block(t09AnchorHeight+1, 1_700_000_200, 21_000)
	receipt := chain.receipt(hash, t09AnchorHeight, "0x5208")
	otherReceipt := chain.receipt(hash, t09AnchorHeight, "0x1")
	receipts := t09JSON([]json.RawMessage{receipt, chain.receipt2(hash, t09AnchorHeight, "0x5208")})
	otherReceipts := t09JSON([]json.RawMessage{otherReceipt, chain.receipt2(hash, t09AnchorHeight, "0x1")})
	tx := chain.txObject(true, hash, t09AnchorHeight, 0)
	logs := t09Logs(hash, chain.txHash)
	otherLogs := t09JSON([]json.RawMessage{t09Log(hash, chain.txHash, t09AnchorHeight, 0, "0x00fe")})
	fee := t09FeeHistory()
	otherFee := json.RawMessage(`{"oldestBlock":"0xf","baseFeePerGas":["0x1","0x2","0x3"],"gasUsedRatio":[0.25,0.5],"reward":[["0x1"],["0x2"]],"vendor":"t09"}`)
	access := t09AccessList()
	otherAccess := json.RawMessage(`{"accessList":[{"address":"0x00000000000000000000000000000000000000cc","storageKeys":["0x0000000000000000000000000000000000000000000000000000000000000001","0x0000000000000000000000000000000000000000000000000000000000000002"]}],"gasUsed":"0x5209","vendor":"t09"}`)
	nextHash := "0x" + strings.Repeat("77", 32)
	blockParams := []any{hash, true}
	blockNumberParams := []any{fmt.Sprintf("0x%x", t09AnchorHeight), true}
	callParams := []any{map[string]any{"to": t09Address}, fmt.Sprintf("0x%x", t09AnchorHeight)}
	feeParams := []any{uint64(2), fmt.Sprintf("0x%x", t09AnchorHeight), []float64{50}}
	accessParams := []any{map[string]any{"to": t09Address}, fmt.Sprintf("0x%x", t09AnchorHeight)}
	logsParams := []any{map[string]any{"blockHash": hash}}
	receiptLogs := fieldOf(t, receipt, "logs")
	receiptsSecondLogs := fieldOf(t, chain.receipt2(hash, t09AnchorHeight, "0x5208"), "logs")
	storageWord := func(suffix string) json.RawMessage {
		return json.RawMessage(`"0x` + strings.Repeat("00", 31) + suffix + `"`)
	}

	cases := []t09MutationCase{
		{
			method: methods.MethodChainID, resolution: t09Strict,
			healthy: json.RawMessage(`"0x1"`), other: json.RawMessage(`"0x2"`),
			mutations: []t09Mutation{
				{class: "illegal-type", field: "quantity", payload: json.RawMessage(`true`), hint: "hex quantity"},
				{class: "empty-value", field: "quantity", payload: json.RawMessage(`"0x"`), hint: "invalid quantity"},
			},
		},
		{
			method: methods.MethodGetBlockByHash, params: blockParams, resolution: t09Strict,
			healthy: block, other: json.RawMessage(`null`),
			mutations: []t09Mutation{
				{class: "delete", field: "hash", payload: t09DeleteField(t, block, "hash"), hint: "missing hash", sourceFault: true},
				{class: "tamper", field: "stateRoot", payload: t09SetField(t, block, "stateRoot", nextHash), hint: "wire hash disagrees", sourceFault: true},
				{class: "wrong-binding", field: "blockHash", payload: nextBlock, hint: "block hash does not match", sourceFault: true},
				{class: "illegal-type", field: "transactions", payload: t09SetField(t, block, "transactions", "0x0"), hint: "block body", sourceFault: true},
				{class: "out-of-range", field: "gasUsed", payload: t09SetField(t, block, "gasUsed", "0x10000000000000000"), hint: "gasUsed", sourceFault: true},
				{class: "nested-delete", field: "transactions[1].gas", payload: t09SetField(t, block, "transactions", t09DeleteNestedField(t, fieldOf(t, block, "transactions"), 1, "gas")), hint: "gas", sourceFault: true},
				{class: "optional-type", field: "baseFeePerGas", payload: t09SetField(t, block, "baseFeePerGas", "bad"), hint: "baseFeePerGas", sourceFault: true},
			},
		},
		{
			method: methods.MethodGetBlockByNumber, params: blockNumberParams, resolution: t09Strict,
			healthy: block, other: fork,
			mutations: []t09Mutation{
				{class: "delete", field: "hash", payload: t09DeleteField(t, block, "hash"), hint: "missing hash", sourceFault: true},
				{class: "wrong-binding", field: "blockNumber", payload: nextBlock, hint: "block number does not match", sourceFault: true},
				{class: "illegal-type", field: "transactions", payload: t09SetField(t, block, "transactions", "0x0"), hint: "block body", sourceFault: true},
				{class: "out-of-range", field: "gasUsed", payload: t09SetField(t, block, "gasUsed", "0x10000000000000000"), hint: "gasUsed", sourceFault: true},
			},
		},
		{
			method: methods.MethodGetBalance, params: []any{t09Address, fmt.Sprintf("0x%x", t09AnchorHeight)}, resolution: t09Strict,
			healthy: json.RawMessage(`"0x64"`), other: json.RawMessage(`"0x3e8"`),
			mutations: []t09Mutation{
				{class: "illegal-type", field: "quantity", payload: json.RawMessage(`true`), hint: "hex quantity"},
				{class: "empty-value", field: "quantity", payload: json.RawMessage(`"0x"`), hint: "invalid quantity"},
				{class: "non-hex", field: "quantity", payload: json.RawMessage(`"zz"`), hint: "hex quantity"},
			},
		},
		{
			method: methods.MethodGetTransactionReceipt, params: []any{chain.txHash}, resolution: t09Strict,
			healthy: receipt, other: otherReceipt,
			mutations: []t09Mutation{
				{class: "delete", field: "logs", payload: t09DeleteField(t, receipt, "logs"), hint: "missing logs", sourceFault: true},
				{class: "wrong-binding", field: "transactionHash", payload: t09SetField(t, receipt, "transactionHash", nextHash), hint: "transaction hash mismatch", sourceFault: true},
				{class: "illegal-type", field: "status", payload: t09SetField(t, receipt, "status", "yes"), hint: "status", sourceFault: true},
				{class: "out-of-range", field: "transactionIndex", payload: t09SetField(t, receipt, "transactionIndex", "0x10000000000000000"), hint: "receipt", sourceFault: true},
				{class: "nested-delete", field: "logs[0].topics", payload: t09SetField(t, receipt, "logs", t09DeleteNestedField(t, receiptLogs, 0, "topics")), hint: "receipt", sourceFault: true},
				{class: "optional-type", field: "blobGasPrice", payload: t09SetField(t, receipt, "blobGasPrice", "bad"), hint: "receipt", sourceFault: true},
			},
		},
		{
			method: methods.MethodGetTransactionByHash, params: []any{chain.txHash}, resolution: t09Strict,
			healthy: tx, other: chain.txObject(true, hash, t09AnchorHeight+1, 1),
			mutations: []t09Mutation{
				{class: "delete", field: "gas", payload: t09DeleteField(t, tx, "gas"), hint: "malformed response", sourceFault: true},
				{class: "wrong-binding", field: "hash", payload: t09SetField(t, tx, "hash", nextHash), hint: "malformed response", sourceFault: true},
				{class: "illegal-type", field: "nonce", payload: t09SetField(t, tx, "nonce", "1"), hint: "malformed response", sourceFault: true},
				{class: "out-of-range", field: "gas", payload: t09SetField(t, tx, "gas", "0x10000000000000000"), hint: "malformed response", sourceFault: true},
				{class: "optional-type", field: "maxFeePerGas", payload: t09SetField(t, tx, "maxFeePerGas", "0xzz"), hint: "malformed response", sourceFault: true},
			},
		},
		{
			method: methods.MethodGetBlockReceipts, params: []any{hash}, resolution: t09Strict,
			healthy: receipts, other: otherReceipts,
			mutations: []t09Mutation{
				{class: "delete", field: "receipts[1].logs", payload: t09DeleteNestedField(t, receipts, 1, "logs"), hint: "missing logs", sourceFault: true},
				{class: "wrong-binding", field: "receipts[1].logs[0].blockHash", payload: t09SetNestedField(t, receipts, 1, "blockHash", nextHash), hint: "block hash mismatch", sourceFault: true},
				{class: "illegal-type", field: "receipts[1].logs", payload: t09SetNestedField(t, receipts, 1, "logs", "bad"), hint: "logs must be an array", sourceFault: true},
				{class: "out-of-range", field: "receipts[1].transactionIndex", payload: t09SetNestedField(t, receipts, 1, "transactionIndex", "0x10000000000000000"), hint: "receipt 1", sourceFault: true},
				{class: "nested-delete", field: "receipts[1].logs[0].topics", payload: t09SetNestedField(t, receipts, 1, "logs", t09DeleteNestedField(t, receiptsSecondLogs, 0, "topics")), hint: "receipt 1", sourceFault: true},
			},
		},
		{
			method: methods.MethodGetTransactionCount, params: []any{t09Address, fmt.Sprintf("0x%x", t09AnchorHeight)}, resolution: t09Strict,
			healthy: json.RawMessage(`"0xa"`), other: json.RawMessage(`"0xb"`),
			mutations: []t09Mutation{
				{class: "illegal-type", field: "quantity", payload: json.RawMessage(`true`), hint: "hex quantity"},
				{class: "empty-value", field: "quantity", payload: json.RawMessage(`"0x"`), hint: "invalid quantity"},
			},
		},
		{
			method: methods.MethodGetStorageAt, params: []any{t09Address, t09StorageKey, fmt.Sprintf("0x%x", t09AnchorHeight)}, resolution: t09Strict,
			healthy: storageWord("aa"), other: storageWord("bb"),
			mutations: []t09Mutation{
				{class: "illegal-type", field: "word", payload: json.RawMessage(`true`), hint: "storage word"},
				{class: "malformed", field: "word", payload: json.RawMessage(`"0x01"`), hint: "storage word"},
			},
		},
		{
			method: methods.MethodGetCode, params: []any{t09Address, fmt.Sprintf("0x%x", t09AnchorHeight)}, resolution: t09Strict,
			healthy: json.RawMessage(`"0x6080"`), other: json.RawMessage(`"0x6081"`),
			mutations: []t09Mutation{
				{class: "illegal-type", field: "code", payload: json.RawMessage(`true`), hint: "hex data"},
				{class: "malformed", field: "code", payload: json.RawMessage(`"0xabc"`), hint: "hex data"},
			},
		},
		{
			method: methods.MethodCall, params: callParams, resolution: t09Strict,
			healthy: json.RawMessage(`"0xdeadbeef"`), other: json.RawMessage(`"0xdeadbeee"`),
			mutations: []t09Mutation{
				{class: "illegal-type", field: "data", payload: json.RawMessage(`true`), hint: "call outcome"},
				{class: "malformed", field: "data", payload: json.RawMessage(`"0xabc"`), hint: "hex data"},
				{class: "non-hex", field: "data", payload: json.RawMessage(`"zz"`), hint: "hex data"},
			},
		},
		{
			method: methods.MethodEstimateGas, params: callParams, resolution: t09Median,
			healthy: json.RawMessage(`"0x5208"`), other: json.RawMessage(`"0x5209"`),
			mutations: []t09Mutation{
				{class: "illegal-type", field: "quantity", payload: json.RawMessage(`true`), hint: "hex quantity"},
				{class: "empty-value", field: "quantity", payload: json.RawMessage(`"0x"`), hint: "invalid quantity"},
				{class: "non-hex", field: "quantity", payload: json.RawMessage(`"zz"`), hint: "hex quantity"},
			},
		},
		{
			method: methods.MethodGetLogs, params: logsParams, resolution: t09Strict,
			healthy: logs, other: otherLogs,
			mutations: []t09Mutation{
				{class: "nested-delete", field: "logs[1].topics", payload: t09DeleteNestedField(t, logs, 1, "topics"), hint: "logs[1]", sourceFault: true},
				{class: "wrong-binding", field: "logs[0].blockHash", payload: t09SetNestedField(t, logs, 0, "blockHash", nextHash), hint: "blockHash", sourceFault: true},
				{class: "illegal-type", field: "logs[0].removed", payload: t09SetNestedField(t, logs, 0, "removed", "no"), hint: "removed", sourceFault: true},
			},
		},
		{
			method: methods.MethodFeeHistory, params: feeParams, resolution: t09Strict,
			healthy: fee, other: otherFee,
			mutations: []t09Mutation{
				{class: "delete", field: "oldestBlock", payload: t09DeleteField(t, fee, "oldestBlock"), hint: "feeHistory", sourceFault: true},
				{class: "illegal-type", field: "gasUsedRatio", payload: t09SetField(t, fee, "gasUsedRatio", "bad"), hint: "feeHistory", sourceFault: true},
				{class: "out-of-range", field: "gasUsedRatio", payload: t09SetField(t, fee, "gasUsedRatio", []float64{2}), hint: "gasUsedRatio", sourceFault: true},
				{class: "nested-type", field: "reward[0][0]", payload: t09SetField(t, fee, "reward", []any{[]any{"bad"}}), hint: "reward", sourceFault: true},
				{class: "optional-type", field: "baseFeePerBlobGas", payload: t09SetField(t, fee, "baseFeePerBlobGas", "bad"), hint: "feeHistory", sourceFault: true},
			},
		},
		{
			method: methods.MethodGasPrice, resolution: t09Median,
			healthy: json.RawMessage(`"0x3b9aca00"`), other: json.RawMessage(`"0x3b9aca01"`),
			mutations: []t09Mutation{
				{class: "illegal-type", field: "quantity", payload: json.RawMessage(`true`), hint: "hex quantity"},
				{class: "empty-value", field: "quantity", payload: json.RawMessage(`"0x"`), hint: "invalid quantity"},
			},
		},
		{
			method: methods.MethodMaxPriorityFeePerGas, resolution: t09Median,
			healthy: json.RawMessage(`"0x5f5e100"`), other: json.RawMessage(`"0x5f5e101"`),
			mutations: []t09Mutation{
				{class: "illegal-type", field: "quantity", payload: json.RawMessage(`true`), hint: "hex quantity"},
				{class: "empty-value", field: "quantity", payload: json.RawMessage(`"0x"`), hint: "invalid quantity"},
			},
		},
		{
			method: methods.MethodBlobBaseFee, resolution: t09Median,
			healthy: json.RawMessage(`"0x1"`), other: json.RawMessage(`"0x2"`),
			mutations: []t09Mutation{
				{class: "illegal-type", field: "quantity", payload: json.RawMessage(`true`), hint: "hex quantity"},
				{class: "empty-value", field: "quantity", payload: json.RawMessage(`"0x"`), hint: "invalid quantity"},
			},
		},
		{
			method: methods.MethodBlockNumber, resolution: t09Median,
			healthy: json.RawMessage(`"0x10"`), other: json.RawMessage(`"0x11"`),
			mutations: []t09Mutation{
				{class: "illegal-type", field: "quantity", payload: json.RawMessage(`true`), hint: "hex quantity"},
				{class: "empty-value", field: "quantity", payload: json.RawMessage(`"0x"`), hint: "invalid quantity"},
			},
		},
		{
			method: methods.MethodCreateAccessList, params: accessParams, resolution: t09Strict,
			healthy: access, other: otherAccess,
			mutations: []t09Mutation{
				{class: "delete", field: "accessList", payload: t09DeleteField(t, access, "accessList"), hint: "accessList"},
				{class: "illegal-type", field: "accessList", payload: t09SetField(t, access, "accessList", "bad"), hint: "expected an array"},
				{class: "nested-type", field: "accessList[0].storageKeys[0]", payload: t09SetField(t, access, "accessList", []any{map[string]any{"address": "0x00000000000000000000000000000000000000cc", "storageKeys": []string{"0x01"}}}), hint: "storageKeys"},
				{class: "optional-type", field: "error", payload: t09SetField(t, access, "error", 5), hint: "error must be a string"},
				{class: "out-of-range", field: "gasUsed", payload: t09SetField(t, access, "gasUsed", "-0x1"), hint: "gasUsed"},
			},
		},
		{
			method: methods.MethodPeerCount, resolution: t09Local,
			healthy: json.RawMessage(`"0x5"`), other: json.RawMessage(`"0x6"`),
			mutations: []t09Mutation{
				{class: "illegal-type", field: "quantity", payload: json.RawMessage(`null`), hint: "hex quantity"},
				{class: "empty-value", field: "quantity", payload: json.RawMessage(`"0x"`), hint: "invalid quantity"},
				{class: "non-hex", field: "quantity", payload: json.RawMessage(`"0xzz"`), hint: "hex quantity"},
			},
		},
		{
			method: methods.MethodSyncing, resolution: t09Local,
			healthy: json.RawMessage(`false`), other: json.RawMessage(`false`),
			mutations: []t09Mutation{
				{class: "illegal-type", field: "answer", payload: json.RawMessage(`null`), hint: "expected false or an object"},
				{class: "illegal-type", field: "answer", payload: json.RawMessage(`true`), hint: "expected false or an object"},
				{class: "illegal-type", field: "answer", payload: json.RawMessage(`[]`), hint: "expected false or an object"},
			},
		},
		{
			method: methods.MethodNetVersion, resolution: t09Strict,
			healthy: json.RawMessage(`"1"`), other: json.RawMessage(`"2"`),
			mutations: []t09Mutation{
				{class: "illegal-type", field: "version", payload: json.RawMessage(`true`), hint: "decimal string"},
				{class: "wrong-spelling", field: "version", payload: json.RawMessage(`"0x1"`), hint: "decimal string"},
			},
		},
		{
			method: methods.MethodListening, resolution: t09Local,
			healthy: json.RawMessage(`true`), other: json.RawMessage(`false`),
			mutations: []t09Mutation{
				{class: "illegal-type", field: "answer", payload: json.RawMessage(`null`), hint: "expected a boolean"},
				{class: "illegal-type", field: "answer", payload: json.RawMessage(`"yes"`), hint: "expected a boolean"},
				{class: "illegal-type", field: "answer", payload: json.RawMessage(`1`), hint: "expected a boolean"},
			},
		},
	}

	seen := map[string]bool{}
	for _, mc := range cases {
		mc := mc
		if seen[mc.method] {
			t.Fatalf("%s: duplicate mutation case", mc.method)
		}
		seen[mc.method] = true
		t.Run(mc.method, func(t *testing.T) {
			if len(mc.mutations) == 0 {
				t.Fatal("no mutation classes")
			}
			for _, mutation := range mc.mutations {
				mutation := mutation
				t.Run(mutation.class+"/"+mutation.field, func(t *testing.T) {
					t09RunMutation(t, mc, mutation)
				})
			}
		})
	}
	for _, name := range methods.RegisteredNames() {
		if !seen[name] {
			t.Fatalf("registered method %q has no mutation case", name)
		}
	}
}

func t09RunMutation(t *testing.T, mc t09MutationCase, mutation t09Mutation) {
	t.Helper()
	fleet := t09NewFleet(t, 4)
	mutantIndex := 3
	if mc.resolution == t09Local {
		mutantIndex = 0
	}
	for index := 0; index < 4; index++ {
		switch {
		case index == mutantIndex:
			fleet.nodes[index].ReturnRaw(mc.method, mutation.payload)
		case mc.resolution == t09Local:
			if index == 1 {
				fleet.nodes[index].ReturnRaw(mc.method, mc.healthy)
			} else {
				fleet.nodes[index].ReturnRaw(mc.method, mc.other)
			}
		case index < 2:
			fleet.nodes[index].ReturnRaw(mc.method, mc.healthy)
		case index == 2:
			fleet.nodes[index].ReturnRaw(mc.method, mc.other)
		}
	}
	decision, err := fleet.client.Call(fleet.context(t), mc.method, mc.params...)
	switch mc.resolution {
	case t09Strict:
		if !errors.Is(err, rpc.ErrConflict) || decision.Status != rpc.Unknown {
			t.Fatalf("%s/%s: status %v err %v, want conflict", mc.method, mutation.field, decision.Status, err)
		}
		if len(decision.Support) != 2 || len(decision.Reject) != 1 {
			t.Fatalf("%s/%s: support/reject = %v/%v, want 2/1", mc.method, mutation.field, t09SourceNames(decision.Support), t09SourceNames(decision.Reject))
		}
		if decision.Support[0].Name != fleet.names[0] || decision.Support[1].Name != fleet.names[1] {
			t.Fatalf("%s/%s: support = %v, want s0,s1", mc.method, mutation.field, t09SourceNames(decision.Support))
		}
		if decision.Reject[0].Name != fleet.names[2] {
			t.Fatalf("%s/%s: reject = %v, want s2", mc.method, mutation.field, t09SourceNames(decision.Reject))
		}
		wantText := "required support 3, winning cluster 2 of 4"
		if mc.method == methods.MethodCall {
			wantText = "required support 3, winning execution 2 of 4"
		}
		if err == nil || !strings.Contains(err.Error(), wantText) {
			t.Fatalf("%s/%s: error = %v, want %q", mc.method, mutation.field, err, wantText)
		}
	case t09Median:
		if err != nil || decision.Status != rpc.Selected {
			t.Fatalf("%s/%s: status %v err %v, want selected", mc.method, mutation.field, decision.Status, err)
		}
		if len(decision.Support) != 2 || len(decision.Reject) != 1 {
			t.Fatalf("%s/%s: support/reject = %v/%v, want 2/1", mc.method, mutation.field, t09SourceNames(decision.Support), t09SourceNames(decision.Reject))
		}
	case t09Local:
		if err != nil || decision.Status != rpc.Selected {
			t.Fatalf("%s/%s: status %v err %v, want selected", mc.method, mutation.field, decision.Status, err)
		}
		if len(decision.Support) != 1 || decision.Support[0].Name != fleet.names[1] {
			t.Fatalf("%s/%s: support = %v, want s1", mc.method, mutation.field, t09SourceNames(decision.Support))
		}
		if len(decision.Reject) != 0 {
			t.Fatalf("%s/%s: reject = %v, want none", mc.method, mutation.field, t09SourceNames(decision.Reject))
		}
	}
	if len(decision.Participants) != 4 {
		t.Fatalf("%s/%s: participants = %d, want fixed basis 4", mc.method, mutation.field, len(decision.Participants))
	}
	if len(decision.Faults) != 1 {
		t.Fatalf("%s/%s: faults = %d, want 1: %v", mc.method, mutation.field, len(decision.Faults), decision.Faults)
	}
	if mutation.sourceFault {
		if len(decision.SourceFaults) != 1 || decision.SourceFaults[0].Source.Name != fleet.names[mutantIndex] {
			t.Fatalf("%s/%s: source faults = %+v, want %q", mc.method, mutation.field, decision.SourceFaults, fleet.names[mutantIndex])
		}
	} else if len(decision.SourceFaults) != 0 {
		t.Fatalf("%s/%s: source faults = %+v, want none for a normalize/encode fault", mc.method, mutation.field, decision.SourceFaults)
	}
	for _, source := range append(append([]rpc.SourceRef(nil), decision.Support...), decision.Reject...) {
		if source.Name == fleet.names[mutantIndex] {
			t.Fatalf("%s/%s: mutated source entered Support or Reject", mc.method, mutation.field)
		}
	}
	if mutation.hint != "" {
		joined := ""
		for _, fault := range decision.Faults {
			joined += fault.Error() + "\n"
		}
		if !strings.Contains(strings.ToLower(joined), strings.ToLower(mutation.hint)) {
			t.Fatalf("%s/%s: fault does not name %q:\n%s", mc.method, mutation.field, mutation.hint, joined)
		}
	}
	if len(decision.Value) == 0 {
		t.Fatalf("%s/%s: success Value is empty", mc.method, mutation.field)
	}
	want, err := t09FieldRowFor(t, mc.method).decode(t09SemanticJSON(t, mc.healthy))
	if err != nil {
		t.Fatal(err)
	}
	got, err := t09FieldRowFor(t, mc.method).decode(decision.Value)
	if err != nil || got != want {
		t.Fatalf("%s/%s: Value = %s (%v), want %q", mc.method, mutation.field, trimValue(decision.Value), err, want)
	}
}
