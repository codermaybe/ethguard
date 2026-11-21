package methods

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/objects/feehistory"
)

// TestFeeHistoryAdmissionBindsRewardWidthToRequestedPercentiles pins the
// official reward semantics: every sample row derives from the percentiles
// the request declared, so a source answering rows of a width the request
// never declared is not an official answer. Without this binding a fleet
// that is uniformly wrong in the same way clusters into Corroborated.
func TestFeeHistoryAdmissionBindsRewardWidthToRequestedPercentiles(t *testing.T) {
	plan := compiledPlan(t, MethodFeeHistory)
	head := `{"oldestBlock":"0x1","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[0.5]`
	reject := []struct {
		name    string
		request []any
		value   string
	}{
		{"wrong width with percentiles", []any{uint64(1), "latest", []float64{10, 50}}, head + `,"reward":[["0x10"]]}`},
		{"absent reward with percentiles", []any{uint64(1), "latest", []float64{10}}, head + `}`},
		{"null reward with percentiles", []any{uint64(1), "latest", []float64{10}}, head + `,"reward":null}`},
		{"samples without requested percentiles", []any{uint64(1), "latest", []float64{}}, head + `,"reward":[["0x10"]]}`},
	}
	for _, tc := range reject {
		_, err := plan.Admit(context.Background(), rpc.Request{Method: MethodFeeHistory, Params: tc.request}, json.RawMessage(tc.value), nil)
		if !errors.Is(err, rpc.ErrMalformedResponse) {
			t.Fatalf("%s: expected ErrMalformedResponse, got %v", tc.name, err)
		}
	}
	admit := []struct {
		name    string
		request []any
		value   string
	}{
		{"matching width", []any{uint64(1), "latest", []float64{10, 50}}, head + `,"reward":[["0x10","0x20"]]}`},
		{"absent without percentiles", []any{uint64(1), "latest", []float64{}}, head + `}`},
		{"null without percentiles", []any{uint64(1), "latest", []float64{}}, head + `,"reward":null}`},
		{"empty rows without percentiles", []any{uint64(1), "latest", []float64{}}, head + `,"reward":[[]]}`},
	}
	for _, tc := range admit {
		if _, err := plan.Admit(context.Background(), rpc.Request{Method: MethodFeeHistory, Params: tc.request}, json.RawMessage(tc.value), nil); err != nil {
			t.Fatalf("%s: official shape rejected: %v", tc.name, err)
		}
	}
}

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

func TestNormalizeFeeHistoryPreservesOfficialBlobFields(t *testing.T) {
	value := []byte(`{"oldestBlock":"0x1","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[0.5],"baseFeePerBlobGas":["0x0","0x2"],"blobGasUsedRatio":[0.25]}`)
	got, err := normalizeFeeHistory(value)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"oldestBlock":"1","baseFeePerGas":["1","2"],"gasUsedRatio":[0.5],"baseFeePerBlobGas":["0","2"],"blobGasUsedRatio":[0.25]}`
	if got != want {
		t.Fatalf("normalized = %s, want %s", got, want)
	}
	for _, invalid := range []string{
		`{"oldestBlock":"0x1","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[0.5],"baseFeePerBlobGas":["0x0"],"blobGasUsedRatio":[0.25]}`,
		`{"oldestBlock":"0x1","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[0.5],"baseFeePerBlobGas":["0x0","0x2"],"blobGasUsedRatio":[0.25,0.5]}`,
	} {
		if _, err := normalizeFeeHistory([]byte(invalid)); err == nil {
			t.Fatalf("invalid blob field lengths accepted: %s", invalid)
		}
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
	if err := validateFeeHistoryRequest(rpc.Request{Params: []any{json.Number("2"), "latest", []any{json.Number("0.5")}}}); err != nil {
		t.Fatal(err)
	}
	if err := validateFeeHistoryRequest(rpc.Request{Params: []any{uint64(0), "latest", nil}}); !errors.Is(err, rpc.ErrInvalidRequest) {
		t.Fatalf("expected invalid zero count, got %v", err)
	}
}

func TestFeeHistoryAdmissionRejectsMalformedResponse(t *testing.T) {
	plan := compiledPlan(t, MethodFeeHistory)
	bad := json.RawMessage(`{"oldestBlock":"0x1","baseFeePerGas":["0x1"],"gasUsedRatio":[0.5]}`)
	_, err := plan.Admit(context.Background(), rpc.Request{Method: MethodFeeHistory, Params: []any{uint64(1), "latest", []float64{}}}, bad, nil)
	if !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("expected malformed response, got %v", err)
	}
}

func TestFeeHistoryAdmissionRequiresRequestedBlockCount(t *testing.T) {
	plan := compiledPlan(t, MethodFeeHistory)
	value := json.RawMessage(`{"oldestBlock":"0x1","baseFeePerGas":["0x1","0x2","0x3"],"gasUsedRatio":[0.5],"reward":[[]]}`)
	_, err := plan.Admit(context.Background(), rpc.Request{Method: MethodFeeHistory, Params: []any{uint64(2), "0x2", []float64{}}}, value, nil)
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
	policy := planResolver(t, MethodFeeHistory, rpc.WithQuorum(1))
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
		if decision.Decision.Status != rpc.Corroborated || string(decision.Decision.Value) != want {
			t.Fatalf("decision = %s status %v, want %s", decision.Decision.Value, decision.Decision.Status, want)
		}
		return decision.Decision
	}
	forward := resolve([]rpc.Observation{first, second})
	backward := resolve([]rpc.Observation{second, first})
	if forward.Representative != a || backward.Representative != b {
		t.Fatalf("representatives = %+v %+v", forward.Representative, backward.Representative)
	}
	if !bytes.Equal(forward.Provenance, json.RawMessage(`{"from":"a"}`)) || !bytes.Equal(backward.Provenance, json.RawMessage(`{"from":"b"}`)) {
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
	policy := planResolver(t, MethodFeeHistory, rpc.WithQuorum(2))
	a := observation(rpc.SourceRef{Name: "a", ID: "a"}, feeHistory("0x1", "0.5", ""), "")
	b := observation(rpc.SourceRef{Name: "b", ID: "b"}, feeHistory("0x1", "0.50", ""), "")
	d := observation(rpc.SourceRef{Name: "d", ID: "d"}, feeHistory("0x2", "0.1", ""), "")
	c := observation(rpc.SourceRef{Name: "c", ID: "c"}, feeHistory("0x1", "0.30000000000000001", ""), "")
	decision, err := policy.(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 2}, []rpc.Observation{a, b, d, c})
	if !errors.Is(err, rpc.ErrConflict) {
		t.Fatalf("err = %v, want the floor to hold against the unreduced denominator", err)
	}
	if decision.Decision.Status != rpc.Unknown || len(decision.Decision.Support) != 2 {
		t.Fatalf("status %v support %d, want unknown with the two encodable sources", decision.Decision.Status, len(decision.Decision.Support))
	}
	if len(decision.Decision.Faults) != 1 || !strings.Contains(decision.Decision.Faults[0].Error(), "encode:") {
		t.Fatalf("faults = %v", decision.Decision.Faults)
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
	if !errors.Is(err, rpc.ErrInvalidCanonicalResult) || !strings.Contains(err.Error(), "result encoding disagrees within cluster") {
		t.Fatalf("err = %v", err)
	}
	if decision.Decision.Status != rpc.Unknown || len(decision.Decision.Value) != 0 {
		t.Fatalf("decision = %s status %v", decision.Decision.Value, decision.Decision.Status)
	}
	if decision.Decision.Representative != (rpc.SourceRef{Name: "a", ID: "a"}) {
		t.Fatalf("representative = %+v, want the first captured supporter", decision.Decision.Representative)
	}
}

// TestFeeHistoryPendingEncodesTheSelectedSource pins the local path. The
// selected source is encoded, the representative stays zero, and the status
// stays Selected.
func TestFeeHistoryPendingEncodesTheSelectedSource(t *testing.T) {
	plan := compiledPlan(t, MethodFeeHistory)
	decision, err := rpc.ResolveLocalObservation(plan.Resolver, []rpc.Observation{
		{Attempted: true, Source: rpc.SourceRef{Name: "a", ID: "a"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(feeHistory("0xA", "0.50", ""))}}},
		{Attempted: true, Source: rpc.SourceRef{Name: "b", ID: "b"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(feeHistory("0x1", "0.1", ""))}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"oldestBlock":"0xa","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[0.5]}`
	if decision.Decision.Status != rpc.Selected || string(decision.Decision.Value) != want {
		t.Fatalf("pending = %s status %v", decision.Decision.Value, decision.Decision.Status)
	}
	if bytes.Contains(decision.Decision.Value, []byte("reward")) {
		t.Fatal("a missing reward was filled in")
	}
	if decision.Decision.Representative != (rpc.SourceRef{}) || len(decision.Decision.Support) != 1 {
		t.Fatalf("decision = %+v", decision)
	}
}
