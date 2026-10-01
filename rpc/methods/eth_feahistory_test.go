package methods

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ethguard/rpc"
	"ethguard/rpc/internal/objects/feehistory"
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

func feeHistory(oldest, ratio, reward string) string {
	body := `{"oldestBlock":"` + oldest + `","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[` + ratio + `]`
	if reward != "" {
		body += `,"reward":[["` + reward + `"]]`
	}
	return body + `}`
}

// TestFeeHistoryRatioSpellingsShareOneCanonicalValue pins the decimal rule.
// 0.5 and 0.50 are one cluster. Swapping capture order changes the
// representative and leaves the canonical Value untouched. The comparison key
// is the canonical bytes (0047): a field outside the protected domain does
// not split the cluster and does not enter Value.
func TestFeeHistoryRatioSpellingsShareOneCanonicalValue(t *testing.T) {
	policy := FeeHistoryPolicy(func(context.Context) (uint64, error) { return 1, nil }, rpc.WithQuorum(1))
	a := rpc.SourceRef{Name: "a", ID: "a"}
	b := rpc.SourceRef{Name: "b", ID: "b"}
	first := observation(a, feeHistory("0xA", "0.50", "0x10"), `{"from":"a"}`)
	second := observation(b, `{"vendor":"kept-out","oldestBlock":"0xa","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[0.5],"reward":[["0x10"]]}`, `{"from":"b"}`)
	want := `{"oldestBlock":"0xa","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[0.5],"reward":[["0x10"]]}`

	resolve := func(order []rpc.Observation) rpc.Decision {
		t.Helper()
		decision, err := policy.(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 1}, order)
		if err != nil {
			t.Fatal(err)
		}
		if decision.Status != rpc.Corroborated || string(decision.Value) != want {
			t.Fatalf("decision = %s status %v, want %s", decision.Value, decision.Status, want)
		}
		return decision
	}
	forward := resolve([]rpc.Observation{first, second})
	backward := resolve([]rpc.Observation{second, first})
	if forward.Representative != a || backward.Representative != b {
		t.Fatalf("representatives = %+v %+v", forward.Representative, backward.Representative)
	}
	if !bytes.Equal(forward.Evidence, json.RawMessage(`{"from":"a"}`)) || !bytes.Equal(backward.Evidence, json.RawMessage(`{"from":"b"}`)) {
		t.Fatal("evidence did not follow the first captured supporter")
	}
	if bytes.Contains(forward.Value, []byte("vendor")) {
		t.Fatal("a field outside the key entered Value")
	}
}

// TestFeeHistoryUnencodableRatioStaysInTheDenominator pins the failure path.
// Four sources, floor 2. The required support is 3. The fourth ratio's
// shortest form changes the decimal original, so it is a Fault and does not
// vote. Dropping it would shrink the denominator to 3 and let the other three
// succeed. They must not: the denominator stays 4 and two votes are short.
func TestFeeHistoryUnencodableRatioStaysInTheDenominator(t *testing.T) {
	policy := FeeHistoryPolicy(func(context.Context) (uint64, error) { return 1, nil }, rpc.WithQuorum(2))
	a := observation(rpc.SourceRef{Name: "a", ID: "a"}, feeHistory("0x1", "0.5", ""), "")
	b := observation(rpc.SourceRef{Name: "b", ID: "b"}, feeHistory("0x1", "0.50", ""), "")
	d := observation(rpc.SourceRef{Name: "d", ID: "d"}, feeHistory("0x2", "0.1", ""), "")
	c := observation(rpc.SourceRef{Name: "c", ID: "c"}, feeHistory("0x1", "0.30000000000000001", ""), "")
	decision, err := policy.(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 2}, []rpc.Observation{a, b, d, c})
	if !errors.Is(err, rpc.ErrConflict) {
		t.Fatalf("err = %v, want the floor to hold against the unreduced denominator", err)
	}
	if decision.Status != rpc.Unknown || len(decision.Support) != 2 {
		t.Fatalf("status %v support %d, want unknown with the two encodable sources", decision.Status, len(decision.Support))
	}
	if len(decision.Faults) != 1 || !strings.Contains(decision.Faults[0].Error(), "encode:") {
		t.Fatalf("faults = %v", decision.Faults)
	}
}

// TestFeeHistoryCanonicalBytesDisagreeWithinCluster is the narrow-key defect.
// Both sources pass EncodeCheck and share the feeHistory key, but the
// injected encoder spells one of them differently. The result is Unknown,
// ErrMalformedResponse, and there is no second vote. The registered encoder
// cannot produce this on its own: the same key yields the same bytes.
func TestFeeHistoryCanonicalBytesDisagreeWithinCluster(t *testing.T) {
	injected := rpc.Strict(normalizeFeeHistory, rpc.WithQuorum(1), rpc.WithCanonicalObject(func(raw json.RawMessage) (json.RawMessage, error) {
		encoded, err := feehistory.Encode(raw)
		if err != nil {
			return nil, err
		}
		if bytes.Contains(raw, []byte(`"mark"`)) {
			return append(json.RawMessage(nil), append(encoded, ' ')...), nil
		}
		return encoded, nil
	}))
	decision, err := injected.(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 1}, []rpc.Observation{
		observation(rpc.SourceRef{Name: "a", ID: "a"}, `{"mark":1,"oldestBlock":"0x1","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[0.5]}`, ""),
		observation(rpc.SourceRef{Name: "b", ID: "b"}, feeHistory("0x1", "0.50", ""), ""),
	})
	if !errors.Is(err, rpc.ErrMalformedResponse) || !strings.Contains(err.Error(), "result encoding disagrees within cluster") {
		t.Fatalf("err = %v", err)
	}
	if decision.Status != rpc.Unknown || len(decision.Value) != 0 {
		t.Fatalf("decision = %s status %v", decision.Value, decision.Status)
	}
	if decision.Representative != (rpc.SourceRef{Name: "a", ID: "a"}) {
		t.Fatalf("representative = %+v, want the first captured supporter", decision.Representative)
	}
}

// TestFeeHistoryPendingEncodesTheSelectedSource pins the local path. The
// selected source is encoded, the representative stays zero, and the status
// stays Selected.
func TestFeeHistoryPendingEncodesTheSelectedSource(t *testing.T) {
	policy := FeeHistoryPolicy(func(context.Context) (uint64, error) { return 1, nil })
	local := policy.(rpc.LocalObservationPolicy)
	decision, err := local.ResolveLocal([]rpc.Observation{
		{Source: rpc.SourceRef{Name: "a", ID: "a"}, Value: json.RawMessage(feeHistory("0xA", "0.50", ""))},
		{Source: rpc.SourceRef{Name: "b", ID: "b"}, Value: json.RawMessage(feeHistory("0x1", "0.1", ""))},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"oldestBlock":"0xa","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[0.5]}`
	if decision.Status != rpc.Selected || string(decision.Value) != want {
		t.Fatalf("pending = %s status %v", decision.Value, decision.Status)
	}
	if bytes.Contains(decision.Value, []byte("reward")) {
		t.Fatal("a missing reward was filled in")
	}
	if decision.Representative != (rpc.SourceRef{}) || len(decision.Support) != 1 {
		t.Fatalf("decision = %+v", decision)
	}
}
