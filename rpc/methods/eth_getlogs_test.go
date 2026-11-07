package methods

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

func TestLogsPolicyRejectsMixedBlockSelectors(t *testing.T) {
	plan := compiledPlan(t, MethodGetLogs)
	_, _, err := preparePlanRequest(t, plan, rpc.Request{Method: MethodGetLogs, Params: []any{
		map[string]any{"blockHash": "0xabc", "fromBlock": "0x1"},
	}})
	if !errors.Is(err, rpc.ErrInvalidRequest) {
		t.Fatalf("expected invalid request, got %v", err)
	}
}

func TestLogsPolicyRejectsPendingBounds(t *testing.T) {
	plan := compiledPlan(t, MethodGetLogs)
	for _, key := range []string{"fromBlock", "toBlock"} {
		_, _, err := preparePlanRequest(t, plan, rpc.Request{Method: MethodGetLogs, Params: []any{
			map[string]any{key: "pending"},
		}})
		if !errors.Is(err, rpc.ErrInvalidRequest) {
			t.Fatalf("%s=pending: expected invalid request, got %v", key, err)
		}
	}
}

func TestLogsRejectPendingRangeLocally(t *testing.T) {
	// The rejection is the plan's local Validate phase: no establishment
	// exists on the plan path, so a pending range cannot reach one.
	plan := compiledPlan(t, MethodGetLogs)
	filters := []map[string]any{
		{"fromBlock": "pending"},
		{"toBlock": "pending"},
		{"fromBlock": "pending", "toBlock": "pending"},
		{"fromBlock": "Pending", "toBlock": "0x10"},
		{"fromBlock": "0x1", "toBlock": "PENDING"},
		{"fromBlock": " pending ", "toBlock": "latest"},
		{"address": "0x0000000000000000000000000000000000000001", "fromBlock": "pending", "topics": []any{}},
		{"blockHash": "pending"},
		{"blockHash": "PENDING"},
		{"blockHash": "0xabc", "fromBlock": "pending"},
		{"blockHash": "0xabc", "toBlock": "Pending"},
		{"fromBlock": json.RawMessage(`"pending"`)},
		{"toBlock": json.RawMessage(`"PENDING"`)},
		{"fromBlock": map[string]any{"blockNumber": "pending"}},
	}
	for _, filter := range filters {
		_, _, err := preparePlanRequest(t, plan, rpc.Request{Method: MethodGetLogs, Params: []any{filter}})
		if !errors.Is(err, rpc.ErrInvalidRequest) {
			t.Fatalf("filter %v: got %v, want ErrInvalidRequest", filter, err)
		}
		if err == nil || !strings.Contains(err.Error(), "no stable corroboration semantics") {
			t.Fatalf("filter %v: error %v must name the missing pending-range corroboration semantics", filter, err)
		}
		if errors.Is(err, rpc.ErrConflict) || errors.Is(err, rpc.ErrNoValidResponse) || errors.Is(err, rpc.ErrMalformedResponse) {
			t.Fatalf("filter %v: pending rejection classified as a business or provider failure: %v", filter, err)
		}
	}
}

func TestLogsRejectAbnormalPendingBoundsLocally(t *testing.T) {
	plan := compiledPlan(t, MethodGetLogs)
	filters := []map[string]any{
		{"fromBlock": 1},
		{"toBlock": nil},
		{"fromBlock": ""},
		{"toBlock": "   "},
		{"fromBlock": true},
		{"fromBlock": json.RawMessage(`null`)},
		{"toBlock": json.RawMessage(`1`)},
		{"fromBlock": json.RawMessage(`not-json`)},
	}
	for _, filter := range filters {
		_, _, err := preparePlanRequest(t, plan, rpc.Request{Method: MethodGetLogs, Params: []any{filter}})
		if !errors.Is(err, rpc.ErrInvalidRequest) {
			t.Fatalf("filter %v: got %v, want ErrInvalidRequest", filter, err)
		}
		if strings.Contains(err.Error(), "no stable corroboration semantics") {
			t.Fatalf("filter %v: non-pending bound mislabeled as pending: %v", filter, err)
		}
	}
}

func TestLogsRejectReversedNumericRangeLocally(t *testing.T) {
	plan := compiledPlan(t, MethodGetLogs)
	for _, filter := range []map[string]any{
		{"fromBlock": "0x10", "toBlock": "0xf"},
		{"fromBlock": "0X10", "toBlock": "0x0f"},
		{"fromBlock": json.RawMessage(`"0x10"`), "toBlock": json.RawMessage(`"0xf"`)},
	} {
		_, _, err := preparePlanRequest(t, plan, rpc.Request{Method: MethodGetLogs, Params: []any{filter}})
		if !errors.Is(err, rpc.ErrInvalidRequest) {
			t.Fatalf("filter %v: got %v, want ErrInvalidRequest", filter, err)
		}
		if !strings.Contains(err.Error(), "fromBlock is greater than toBlock") {
			t.Fatalf("filter %v: error %v does not identify the inverted range", filter, err)
		}
	}

	// Equal and moving-tag bounds remain valid; their ordering is either
	// equal or intentionally deferred to the reference rewrite.
	for _, filter := range []map[string]any{
		{"fromBlock": "0x10", "toBlock": "0x10"},
		{"fromBlock": "latest", "toBlock": "latest"},
	} {
		if err := plan.Validate(context.Background(), rpc.Request{Method: MethodGetLogs, Params: []any{filter}}); err != nil {
			t.Fatalf("filter %v: got %v, want valid range", filter, err)
		}
	}
}

func TestLogsPendingDoesNotConsultAnchor(t *testing.T) {
	// The rejection is the plan's local Validate phase: the pending shape
	// fails with a request-class error before any establishment could run,
	// never with a reference or conflict classification.
	plan := compiledPlan(t, MethodGetLogs)
	_, _, err := preparePlanRequest(t, plan, rpc.Request{Method: MethodGetLogs, Params: []any{
		map[string]any{"fromBlock": "latest", "toBlock": "Pending"},
	}})
	if !errors.Is(err, rpc.ErrInvalidRequest) {
		t.Fatalf("got %v, want ErrInvalidRequest", err)
	}
	if errors.Is(err, rpc.ErrConflict) || errors.Is(err, rpc.ErrReferenceRequired) {
		t.Fatalf("pending range reached the establishment seam: %v", err)
	}
}

func TestLogsHistoricalRangePreparesWithoutPendingRejection(t *testing.T) {
	// An explicit historical range declares no reference need: it prepares
	// without establishment and stays untouched.
	plan := compiledPlan(t, MethodGetLogs)
	req := rpc.Request{Method: MethodGetLogs, Params: []any{
		map[string]any{"fromBlock": "0x1", "toBlock": "0x10", "address": "0x0000000000000000000000000000000000000001"},
	}}
	if _, needed := plan.ReferenceRequirement(req); needed {
		t.Fatal("an explicit historical range declared a reference need")
	}
	prepared, _, err := plan.PrepareWithReference(req, rpc.ReferenceEvidence{}, false)
	if err != nil {
		t.Fatal(err)
	}
	filter := prepared.Params[0].(map[string]any)
	if filter["fromBlock"] != "0x1" || filter["toBlock"] != "0x10" {
		t.Fatalf("historical range rewritten: %+v", filter)
	}
}

func TestNormalizeLogsKeepsOrderAndDuplicates(t *testing.T) {
	value := []byte(`[{"address":"0x0000000000000000000000000000000000000001","topics":[],"data":"0x","blockNumber":"0x1","transactionHash":"0x0000000000000000000000000000000000000000000000000000000000000001","transactionIndex":"0x0","blockHash":"0x0000000000000000000000000000000000000000000000000000000000000002","logIndex":"0x0","removed":false},{"address":"0x0000000000000000000000000000000000000001","topics":[],"data":"0x","blockNumber":"0x1","transactionHash":"0x0000000000000000000000000000000000000000000000000000000000000001","transactionIndex":"0x0","blockHash":"0x0000000000000000000000000000000000000000000000000000000000000002","logIndex":"0x0","removed":false}]`)
	got, err := normalizeLogs(value)
	if err != nil {
		t.Fatal(err)
	}
	if got == "[]" || len(got) == 0 {
		t.Fatalf("expected two normalized logs, got %s", got)
	}
	if got[0] != '[' || got[len(got)-1] != ']' {
		t.Fatalf("not an array: %s", got)
	}
}

func TestNormalizeLogsRejectsMalformedEntries(t *testing.T) {
	if _, err := normalizeLogs([]byte(`[null]`)); err == nil {
		t.Fatal("expected malformed log entry to be rejected")
	}
}

func TestLogsAdmissionRejectsIncompleteLog(t *testing.T) {
	plan := compiledPlan(t, MethodGetLogs)
	_, err := plan.Admit(context.Background(), rpc.Request{Method: MethodGetLogs, Params: []any{map[string]any{}}}, json.RawMessage(`[{"address":"0x0000000000000000000000000000000000000001"}]`), nil)
	if !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("expected malformed response, got %v", err)
	}
}

func TestLogsAdmissionBindsBlockHash(t *testing.T) {
	plan := compiledPlan(t, MethodGetLogs)
	row := `{"address":"0x0000000000000000000000000000000000000001","topics":[],"data":"0x","blockNumber":"0x1","transactionHash":"0x0000000000000000000000000000000000000000000000000000000000000001","transactionIndex":"0x0","blockHash":"0x0000000000000000000000000000000000000000000000000000000000000002","logIndex":"0x0","removed":false}`
	_, err := plan.Admit(context.Background(), rpc.Request{Method: MethodGetLogs, Params: []any{map[string]any{"blockHash": "0x0000000000000000000000000000000000000000000000000000000000000003"}}}, json.RawMessage("["+row+"]"), nil)
	if !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("expected block binding failure, got %v", err)
	}
}

func TestLogsAdmissionBindsRequestedRange(t *testing.T) {
	plan := compiledPlan(t, MethodGetLogs)
	row := `{"address":"0x0000000000000000000000000000000000000001","topics":[],"data":"0x","blockNumber":"0x5","transactionHash":"0x0000000000000000000000000000000000000000000000000000000000000001","transactionIndex":"0x0","blockHash":"0x0000000000000000000000000000000000000000000000000000000000000002","logIndex":"0x0","removed":false}`
	_, err := plan.Admit(context.Background(), rpc.Request{Method: MethodGetLogs, Params: []any{map[string]any{"fromBlock": "0x1", "toBlock": "0x4"}}}, json.RawMessage("["+row+"]"), nil)
	if !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("expected range binding failure, got %v", err)
	}
}

func logEntry(address, data, blockNumber, removed, extra string) string {
	body := `{"address":"` + address + `","topics":["0x` + strings.Repeat("11", 32) + `"],"data":"` + data + `","blockNumber":"` + blockNumber + `","transactionHash":"0x` + strings.Repeat("22", 32) + `","transactionIndex":"0x0","blockHash":"0x` + strings.Repeat("33", 32) + `","logIndex":"0x1","removed":` + removed
	if extra != "" {
		body += "," + extra
	}
	return body + `}`
}

func canonicalLogValue() string {
	return `[{"address":"0x` + "0000000000000000000000000000000000000001" + `","topics":["0x` + strings.Repeat("11", 32) + `"],"data":"0x00ff","blockNumber":"0xa","transactionHash":"0x` + strings.Repeat("22", 32) + `","transactionIndex":"0x0","blockHash":"0x` + strings.Repeat("33", 32) + `","logIndex":"0x1","removed":false}]`
}

// TestLogsSpellingsShareOneCanonicalValue pins the object rule. Case and key
// order share one cluster. Swapping capture order changes the representative
// and leaves the canonical Value untouched. A field outside the mask does not
// split the cluster and does not enter Value.
func TestLogsSpellingsShareOneCanonicalValue(t *testing.T) {
	policy := planResolver(t, MethodGetLogs, rpc.WithQuorum(1))
	a := rpc.SourceRef{Name: "a", ID: "a"}
	b := rpc.SourceRef{Name: "b", ID: "b"}
	upper := "[" + logEntry("0x0000000000000000000000000000000000000001", "0x00FF", "0xA", "false", "") + "]"
	reordered := `[{"removed":false,"vendor":"kept-out","logIndex":"0x1","blockHash":"0x` + strings.Repeat("33", 32) + `","transactionIndex":"0x0","transactionHash":"0x` + strings.Repeat("22", 32) + `","blockNumber":"0xa","data":"0x00ff","topics":["0x` + strings.Repeat("11", 32) + `"],"address":"0x0000000000000000000000000000000000000001"}]`
	first := observation(a, upper, `{"from":"a"}`)
	second := observation(b, reordered, `{"from":"b"}`)
	want := canonicalLogValue()

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
	if !bytes.Equal(forward.Provenance, json.RawMessage(`{"from":"a"}`)) || !bytes.Equal(backward.Provenance, json.RawMessage(`{"from":"b"}`)) {
		t.Fatal("evidence did not follow the first captured supporter")
	}
	if bytes.Contains(forward.Value, []byte("vendor")) || bytes.Contains(backward.Value, []byte("vendor")) {
		t.Fatal("a field outside the mask entered Value")
	}
}

// TestLogsMissingRequiredFieldStaysInTheDenominator pins the failure path.
// Four sources, floor 2. The required support is 3. The fourth log is missing
// a field log.Validate requires, so EncodeCheck fails, it is a Fault, and it
// does not vote. Dropping it would shrink the denominator to 3 and let the
// other three succeed. They must not.
func TestLogsMissingRequiredFieldStaysInTheDenominator(t *testing.T) {
	policy := planResolver(t, MethodGetLogs, rpc.WithQuorum(2))
	good := "[" + logEntry("0x0000000000000000000000000000000000000001", "0x00ff", "0xa", "false", "") + "]"
	other := "[" + logEntry("0x0000000000000000000000000000000000000002", "0x00ff", "0xa", "false", "") + "]"
	incomplete := `[{"address":"0x0000000000000000000000000000000000000001","topics":[],"data":"0x"}]`
	a := observation(rpc.SourceRef{Name: "a", ID: "a"}, good, "")
	b := observation(rpc.SourceRef{Name: "b", ID: "b"}, good, "")
	d := observation(rpc.SourceRef{Name: "d", ID: "d"}, other, "")
	c := observation(rpc.SourceRef{Name: "c", ID: "c"}, incomplete, "")
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

func TestLogsEncodeOmitsAbsentTimestampAndKeepsEmptyArray(t *testing.T) {
	policy := planResolver(t, MethodGetLogs, rpc.WithQuorum(1))
	withTime := "[" + logEntry("0x0000000000000000000000000000000000000001", "0x00ff", "0xa", "false", `"blockTimestamp":"0x10"`) + "]"
	decision, err := policy.(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 1}, []rpc.Observation{
		observation(rpc.SourceRef{Name: "a", ID: "a"}, withTime, ""),
		observation(rpc.SourceRef{Name: "b", ID: "b"}, withTime, ""),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(decision.Value, []byte(`"blockTimestamp":"0x10"`)) {
		t.Fatalf("timestamp dropped: %s", decision.Value)
	}
	empty, err := policy.(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 1}, []rpc.Observation{
		observation(rpc.SourceRef{Name: "a", ID: "a"}, `[]`, ""),
		observation(rpc.SourceRef{Name: "b", ID: "b"}, `[]`, ""),
	})
	if err != nil || string(empty.Value) != "[]" {
		t.Fatalf("empty = %s err %v", empty.Value, err)
	}
	if _, err := policy.(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 1}, []rpc.Observation{
		observation(rpc.SourceRef{Name: "a", ID: "a"}, `null`, ""),
	}); err == nil {
		t.Fatal("JSON null was accepted")
	}
}
