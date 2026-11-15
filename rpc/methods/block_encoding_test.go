package methods

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/rpc"
	blockobject "github.com/codermaybe/ethguard/rpc/internal/objects/block"
)

func TestBlockPolicyCanonicalizesCompleteOfficialValue(t *testing.T) {
	header := &types.Header{
		ParentHash:  common.HexToHash("0x1"),
		UncleHash:   types.EmptyUncleHash,
		Root:        common.HexToHash("0x2"),
		TxHash:      types.EmptyTxsHash,
		ReceiptHash: common.HexToHash("0x3"),
		Difficulty:  new(big.Int),
		Number:      big.NewInt(1),
		GasLimit:    1,
		Time:        1,
	}
	first := blockRaw(t, header, nil)
	second := injectField(t, first, "totalDifficulty", `"0x300"`)
	second = injectField(t, second, "vendor", `true`)
	policy := planResolver(t, MethodGetBlockByHash, rpc.WithQuorum(2)).(rpc.CorroboratingPolicy)
	decision, err := policy.ResolveCorroborated(rpc.Corroboration{Floor: 2}, []rpc.Observation{
		{Source: rpc.SourceRef{ID: "a"}, Value: first},
		{Source: rpc.SourceRef{ID: "b"}, Value: second},
	})
	if err != nil || decision.Status != rpc.Corroborated {
		t.Fatalf("decision = %+v err %v", decision, err)
	}
	want, err := blockobject.Encode(first)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decision.Value, want) {
		t.Fatalf("value = %s, want %s", decision.Value, want)
	}
	for _, field := range []string{"totalDifficulty", "vendor"} {
		if bytes.Contains(decision.Value, []byte(`"`+field+`"`)) {
			t.Fatalf("uncommitted field %s survived: %s", field, decision.Value)
		}
	}
}

func TestBlockByNumberHeaderOnlyUsesCanonicalValue(t *testing.T) {
	first := json.RawMessage(`{"number":"0x010","hash":"0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","vendor":true}`)
	second := json.RawMessage(`{"hash":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","number":"0x10","extra":"ignored"}`)
	policy := planResolver(t, MethodGetBlockByNumber, rpc.WithQuorum(2)).(rpc.CorroboratingPolicy)
	decision, err := policy.ResolveCorroborated(rpc.Corroboration{Floor: 2}, []rpc.Observation{
		{Source: rpc.SourceRef{ID: "a"}, Value: first},
		{Source: rpc.SourceRef{ID: "b"}, Value: second},
	})
	if err != nil || decision.Status != rpc.Corroborated {
		t.Fatalf("decision = %+v err %v", decision, err)
	}
	want := json.RawMessage(`{"hash":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","number":"0x10"}`)
	if !bytes.Equal(decision.Value, want) {
		t.Fatalf("value = %s, want canonical %s", decision.Value, want)
	}
	for _, field := range []string{"vendor", "extra"} {
		if bytes.Contains(decision.Value, []byte(`"`+field+`"`)) {
			t.Fatalf("source-only field %s survived: %s", field, decision.Value)
		}
	}
}

func TestBlockPolicyFaultsFalseOriginalWireHashBeforeClustering(t *testing.T) {
	firstHeader := &types.Header{
		ParentHash:  common.HexToHash("0x1"),
		UncleHash:   types.EmptyUncleHash,
		Root:        common.HexToHash("0x2"),
		TxHash:      types.EmptyTxsHash,
		ReceiptHash: common.HexToHash("0x3"),
		Difficulty:  new(big.Int),
		Number:      big.NewInt(1),
		GasLimit:    1,
		Time:        1,
	}
	secondHeader := *firstHeader
	secondHeader.ParentHash = common.HexToHash("0x4")
	first := blockRaw(t, firstHeader, nil)
	second := injectField(t, blockRaw(t, &secondHeader, nil), "hash", string(mustMarshal(t, firstHeader.Hash())))
	policy := planResolver(t, MethodGetBlockByHash, rpc.WithQuorum(2)).(rpc.CorroboratingPolicy)
	decision, err := policy.ResolveCorroborated(rpc.Corroboration{Floor: 2}, []rpc.Observation{
		{Source: rpc.SourceRef{ID: "a"}, Value: first},
		{Source: rpc.SourceRef{ID: "b"}, Value: second},
	})
	if !errors.Is(err, rpc.ErrConflict) || decision.Status != rpc.Unknown || len(decision.Support) != 1 || decision.Support[0].ID != "a" || len(decision.Faults) != 1 || !strings.Contains(decision.Faults[0].Error(), `source "b"`) || !strings.Contains(decision.Faults[0].Error(), "wire hash disagrees") {
		t.Fatalf("decision = %+v err %v, want source b fault before clustering", decision, err)
	}
	// A third valid source reaches quorum; the faulty declaration never votes.
	decision, err = policy.ResolveCorroborated(rpc.Corroboration{Floor: 2}, []rpc.Observation{
		{Source: rpc.SourceRef{ID: "a"}, Value: first},
		{Source: rpc.SourceRef{ID: "b"}, Value: second},
		{Source: rpc.SourceRef{ID: "c"}, Value: first},
	})
	if err != nil || decision.Status != rpc.Corroborated || len(decision.Support) != 2 || len(decision.Faults) != 1 {
		t.Fatalf("valid quorum blocked by faulty source: %+v %v", decision, err)
	}
	if _, err := ethapi.NewBlock(decision.Value); err != nil {
		t.Fatalf("committed Value is not official Block: %v", err)
	}
}

func TestBlockPolicyKeepsCommittedUncleHashes(t *testing.T) {
	uncle := &types.Header{
		ParentHash: common.HexToHash("0x9"),
		Number:     big.NewInt(1),
		GasLimit:   1,
		Time:       1,
		Difficulty: new(big.Int),
	}
	header := &types.Header{
		ParentHash:  common.HexToHash("0x1"),
		UncleHash:   types.CalcUncleHash([]*types.Header{uncle}),
		Root:        common.HexToHash("0x2"),
		TxHash:      types.EmptyTxsHash,
		ReceiptHash: common.HexToHash("0x3"),
		Difficulty:  new(big.Int),
		Number:      big.NewInt(1),
		GasLimit:    1,
		Time:        1,
	}
	raw := blockRaw(t, header, []common.Hash{uncle.Hash()})
	policy := planResolver(t, MethodGetBlockByHash, rpc.WithQuorum(2)).(rpc.CorroboratingPolicy)
	decision, err := policy.ResolveCorroborated(rpc.Corroboration{Floor: 2}, []rpc.Observation{
		{Source: rpc.SourceRef{ID: "a"}, Value: raw},
		{Source: rpc.SourceRef{ID: "b"}, Value: raw},
	})
	if err != nil || decision.Status != rpc.Corroborated {
		t.Fatalf("decision = %+v err %v", decision, err)
	}
	if !bytes.Contains(decision.Value, []byte(`"uncles":["`+uncle.Hash().Hex()+`"]`)) {
		t.Fatalf("committed uncle hash missing: %s", decision.Value)
	}
}

func TestBlockPolicyKeepsCommittedWithdrawals(t *testing.T) {
	withdrawalsHash := types.EmptyWithdrawalsHash
	header := &types.Header{
		ParentHash:      common.HexToHash("0x1"),
		UncleHash:       types.EmptyUncleHash,
		Root:            common.HexToHash("0x2"),
		TxHash:          types.EmptyTxsHash,
		ReceiptHash:     common.HexToHash("0x3"),
		Difficulty:      new(big.Int),
		Number:          big.NewInt(1),
		GasLimit:        1,
		Time:            1,
		WithdrawalsHash: &withdrawalsHash,
	}
	raw := injectField(t, blockRaw(t, header, nil), "withdrawals", `[]`)
	policy := planResolver(t, MethodGetBlockByHash, rpc.WithQuorum(2)).(rpc.CorroboratingPolicy)
	decision, err := policy.ResolveCorroborated(rpc.Corroboration{Floor: 2}, []rpc.Observation{
		{Source: rpc.SourceRef{ID: "a"}, Value: raw},
		{Source: rpc.SourceRef{ID: "b"}, Value: raw},
	})
	if err != nil || decision.Status != rpc.Corroborated {
		t.Fatalf("decision = %+v err %v", decision, err)
	}
	if !bytes.Contains(decision.Value, []byte(`"withdrawals":[]`)) {
		t.Fatalf("committed withdrawals missing: %s", decision.Value)
	}
}

func TestBlockPolicyPreservesMissingHeadDiagnostic(t *testing.T) {
	policy := planResolver(t, MethodGetBlockByNumber, rpc.WithQuorum(2)).(rpc.CorroboratingPolicy)
	decision, err := policy.ResolveCorroborated(rpc.Corroboration{Floor: 2}, []rpc.Observation{
		{Source: rpc.SourceRef{ID: "a"}, Value: json.RawMessage(`{"unexpected":true}`)},
		{Source: rpc.SourceRef{ID: "b"}, Value: json.RawMessage(`{"unexpected":true}`)},
	})
	var faults string
	for _, fault := range decision.Faults {
		faults += fault.Error() + "\n"
	}
	if err == nil || !strings.Contains(faults, "missing number or hash") {
		t.Fatalf("decision = %+v err = %v, want missing-head diagnostic", decision, err)
	}
}

func TestBlockPolicySameHashDifferentSizeFormsDistinctCanonicalClusters(t *testing.T) {
	raw, _ := admissionBlock(t, []json.RawMessage{})
	first := injectField(t, raw, "size", `"0x200"`)
	second := injectField(t, raw, "size", `"0x201"`)
	for _, method := range []string{MethodGetBlockByHash, MethodGetBlockByNumber} {
		t.Run(method, func(t *testing.T) {
			policy := planResolver(t, method, rpc.WithQuorum(2)).(rpc.CorroboratingPolicy)
			obs := []rpc.Observation{{Source: rpc.SourceRef{ID: "a"}, Value: first}, {Source: rpc.SourceRef{ID: "b"}, Value: second}}
			decision, err := policy.ResolveCorroborated(rpc.Corroboration{Floor: 2}, obs)
			if !errors.Is(err, rpc.ErrConflict) || decision.Status != rpc.Unknown || len(decision.Support) != 1 || len(decision.Faults) != 0 {
				t.Fatalf("same hash with differing valid size did not split clusters: %+v %v", decision, err)
			}
			obs = append(obs, rpc.Observation{Source: rpc.SourceRef{ID: "c"}, Value: second})
			decision, err = policy.ResolveCorroborated(rpc.Corroboration{Floor: 2}, obs)
			if err != nil || decision.Status != rpc.Corroborated || len(decision.Support) != 2 || len(decision.Reject) != 1 || len(decision.Faults) != 0 {
				t.Fatalf("2 of 3 complete values did not win: %+v %v", decision, err)
			}
			value, err := ethapi.NewBlock(decision.Value)
			if err != nil || value.Size != "0x201" {
				t.Fatalf("winning official Value = %s %v", decision.Value, err)
			}
			for _, source := range decision.Support {
				if source.ID == "a" {
					t.Fatal("different-size source counted as support")
				}
			}
			want, err := blockobject.Encode(second)
			if err != nil || !bytes.Equal(decision.Value, want) {
				t.Fatalf("committed Value differs from actual winning key: %s %v", decision.Value, err)
			}
		})
	}
}

func TestBlockReferenceFallbackCannotBypassHeaderOrFullBodyChecks(t *testing.T) {
	reference := json.RawMessage(`{"number":"0x1","hash":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","vendor":true}`)
	if _, err := blockobject.Encode(reference); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"parentHash", "difficulty", "baseFeePerGas", "slotNumber", "transactions", "uncles", "size"} {
		raw := injectField(t, reference, field, `null`)
		if _, err := blockobject.Encode(raw); err == nil {
			t.Fatalf("reference fallback hid declared %s", field)
		}
	}
	plan := compiledPlan(t, MethodGetBlockByNumber)
	if _, err := plan.Admit(context.Background(), rpc.Request{Method: MethodGetBlockByNumber, Params: []any{"0x1", true}}, reference, nil); err == nil {
		t.Fatal("fullTx admission accepted reference-only object")
	}
}

func TestBlockReferenceRetainsInternalShortHashGrammar(t *testing.T) {
	policy := planResolver(t, MethodGetBlockByNumber, rpc.WithQuorum(2)).(rpc.CorroboratingPolicy)
	decision, err := policy.ResolveCorroborated(rpc.Corroboration{Floor: 2}, []rpc.Observation{
		{Source: rpc.SourceRef{ID: "a"}, Value: json.RawMessage(`{"hash":"0xABC","number":"0x01","vendor":true}`)},
		{Source: rpc.SourceRef{ID: "b"}, Value: json.RawMessage(`{"hash":"0xabc","number":"0x1","extra":true}`)},
	})
	if err != nil || decision.Status != rpc.Corroborated || string(decision.Value) != `{"hash":"0xabc","number":"0x1"}` {
		t.Fatalf("internal reference grammar changed: %+v %v", decision, err)
	}
}
