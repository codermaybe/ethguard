package methods

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"ethguard/rpc"
	blockobject "ethguard/rpc/internal/objects/block"
)

func TestBlockPolicyCanonicalizesNarrowKeyValue(t *testing.T) {
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
	second := injectField(t, first, "size", `"0x200"`)
	second = injectField(t, second, "totalDifficulty", `"0x300"`)
	second = injectField(t, second, "vendor", `true`)
	policy := BlockByHashPolicy(rpc.WithQuorum(2)).(rpc.CorroboratingPolicy)
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
	for _, field := range []string{"size", "totalDifficulty", "vendor"} {
		if bytes.Contains(decision.Value, []byte(`"`+field+`"`)) {
			t.Fatalf("uncommitted field %s survived: %s", field, decision.Value)
		}
	}
}

func TestBlockPolicyFinishRejectsSameKeyDifferentCommittedHeader(t *testing.T) {
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
	policy := BlockByHashPolicy(rpc.WithQuorum(2)).(rpc.CorroboratingPolicy)
	decision, err := policy.ResolveCorroborated(rpc.Corroboration{Floor: 2}, []rpc.Observation{
		{Source: rpc.SourceRef{ID: "a"}, Value: first},
		{Source: rpc.SourceRef{ID: "b"}, Value: second},
	})
	if !errors.Is(err, rpc.ErrMalformedResponse) || decision.Status != rpc.Unknown || len(decision.Value) != 0 {
		t.Fatalf("decision = %+v err %v, want finish guard failure", decision, err)
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
	policy := BlockByHashPolicy(rpc.WithQuorum(2)).(rpc.CorroboratingPolicy)
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
	policy := BlockByHashPolicy(rpc.WithQuorum(2)).(rpc.CorroboratingPolicy)
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
	policy := BlockByNumberPolicy(rpc.WithQuorum(2)).(rpc.CorroboratingPolicy)
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
