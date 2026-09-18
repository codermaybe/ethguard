package methods

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"ethguard/rpc"
)

func TestNormalizeFeeHistoryChecksArrayRelationships(t *testing.T) {
	valid := []byte(`{"oldestBlock":"0x1","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[0.5],"reward":[["0x3"]]}`)
	if _, err := normalizeFeeHistory(valid); err != nil {
		t.Fatal(err)
	}
	bad := []byte(`{"oldestBlock":"0x1","baseFeePerGas":["0x1"],"gasUsedRatio":[0.5],"reward":[["0x3"]]}`)
	if _, err := normalizeFeeHistory(bad); err == nil {
		t.Fatal("expected base fee length mismatch")
	}
	bad = []byte(`{"oldestBlock":"0x1","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[1.2],"reward":[["0x3"]]}`)
	if _, err := normalizeFeeHistory(bad); err == nil {
		t.Fatal("expected gas ratio range rejection")
	}
}

func TestNormalizeFeeHistoryRejectsInconsistentRewards(t *testing.T) {
	bad := []byte(`{"oldestBlock":"0x1","baseFeePerGas":["0x1","0x2","0x3"],"gasUsedRatio":[0.1,0.2],"reward":[["0x1"],["0x1","0x2"]]}`)
	if _, err := normalizeFeeHistory(bad); err == nil {
		t.Fatal("expected inconsistent reward row rejection")
	}
}

func TestFeeHistoryRequestValidatesTypedAndRawInputs(t *testing.T) {
	if err := validateFeeHistoryRequest(rpc.Request{Params: []any{uint64(2), "latest", []float64{0, 50}}}); err != nil {
		t.Fatal(err)
	}
	if err := validateFeeHistoryRequest(rpc.Request{Params: []any{"0x2", "latest", []any{float64(0), float64(50)}}}); err != nil {
		t.Fatal(err)
	}
	if err := validateFeeHistoryRequest(rpc.Request{Params: []any{uint64(0), "latest", nil}}); !errors.Is(err, rpc.ErrInvalidRequest) {
		t.Fatalf("expected invalid zero count, got %v", err)
	}
}

func TestFeeHistoryAdmissionRejectsMalformedResponse(t *testing.T) {
	p := FeeHistoryPolicy(func(context.Context) (uint64, error) { return 1, nil })
	bad := json.RawMessage(`{"oldestBlock":"0x1","baseFeePerGas":["0x1"],"gasUsedRatio":[0.5]}`)
	admit := p.(rpc.AdmissionPolicy)
	_, err := admit.Admit(context.Background(), rpc.Request{Method: MethodFeeHistory, Params: []any{uint64(1), "latest", []float64{}}}, bad, nil)
	if !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("expected malformed response, got %v", err)
	}
}

func TestFeeHistoryAdmissionRequiresRequestedBlockCount(t *testing.T) {
	p := FeeHistoryPolicy(func(context.Context) (uint64, error) { return 1, nil })
	admit := p.(rpc.AdmissionPolicy)
	value := json.RawMessage(`{"oldestBlock":"0x1","baseFeePerGas":["0x1","0x2","0x3"],"gasUsedRatio":[0.5],"reward":[[]]}`)
	_, err := admit.Admit(context.Background(), rpc.Request{Method: MethodFeeHistory, Params: []any{uint64(2), "0x2", []float64{}}}, value, nil)
	if !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("expected block-count mismatch, got %v", err)
	}
}
