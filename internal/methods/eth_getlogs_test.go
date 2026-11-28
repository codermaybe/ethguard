package methods

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/internal/rpcengine"

	logobject "github.com/codermaybe/ethguard/internal/ethobject/log"
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
	// The rejection is the plan's local Prepare phase: no establishment
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
		if _, err := plan.Prepare(context.Background(), rpc.Request{Method: MethodGetLogs, Params: []any{filter}}); err != nil {
			t.Fatalf("filter %v: got %v, want valid range", filter, err)
		}
	}
}

func TestLogsPendingDoesNotConsultAnchor(t *testing.T) {
	// The rejection is the plan's local Prepare phase: the pending shape
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
	prepared, _, err := plan.PrepareWithReference(req, rpc.ReferenceEvidence{}, false, planTestBasis())
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
	policy := planResolver(t, MethodGetLogs, rpcengine.WithQuorum(1))
	a := rpc.SourceRef{Name: "a", ID: "a"}
	b := rpc.SourceRef{Name: "b", ID: "b"}
	upper := "[" + logEntry("0x0000000000000000000000000000000000000001", "0x00FF", "0xA", "false", "") + "]"
	reordered := `[{"removed":false,"vendor":"kept-out","logIndex":"0x1","blockHash":"0x` + strings.Repeat("33", 32) + `","transactionIndex":"0x0","transactionHash":"0x` + strings.Repeat("22", 32) + `","blockNumber":"0xa","data":"0x00ff","topics":["0x` + strings.Repeat("11", 32) + `"],"address":"0x0000000000000000000000000000000000000001"}]`
	first := observation(a, upper, `{"from":"a"}`)
	second := observation(b, reordered, `{"from":"b"}`)
	want := canonicalLogValue()

	resolve := func(order []rpc.Observation) rpc.Decision {
		t.Helper()
		decision, err := policy.(rpcengine.CorroboratingResolver).ResolveCorroborated(rpcengine.Corroboration{Floor: 1}, order)
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
	policy := planResolver(t, MethodGetLogs, rpcengine.WithQuorum(2))
	good := "[" + logEntry("0x0000000000000000000000000000000000000001", "0x00ff", "0xa", "false", "") + "]"
	other := "[" + logEntry("0x0000000000000000000000000000000000000002", "0x00ff", "0xa", "false", "") + "]"
	incomplete := `[{"address":"0x0000000000000000000000000000000000000001","topics":[],"data":"0x"}]`
	a := observation(rpc.SourceRef{Name: "a", ID: "a"}, good, "")
	b := observation(rpc.SourceRef{Name: "b", ID: "b"}, good, "")
	d := observation(rpc.SourceRef{Name: "d", ID: "d"}, other, "")
	c := observation(rpc.SourceRef{Name: "c", ID: "c"}, incomplete, "")
	decision, err := policy.(rpcengine.CorroboratingResolver).ResolveCorroborated(rpcengine.Corroboration{Floor: 2}, []rpc.Observation{a, b, d, c})
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

func TestLogsEncodeOmitsAbsentTimestampAndKeepsEmptyArray(t *testing.T) {
	policy := planResolver(t, MethodGetLogs, rpcengine.WithQuorum(1))
	withTime := "[" + logEntry("0x0000000000000000000000000000000000000001", "0x00ff", "0xa", "false", `"blockTimestamp":"0x10"`) + "]"
	decision, err := policy.(rpcengine.CorroboratingResolver).ResolveCorroborated(rpcengine.Corroboration{Floor: 1}, []rpc.Observation{
		observation(rpc.SourceRef{Name: "a", ID: "a"}, withTime, ""),
		observation(rpc.SourceRef{Name: "b", ID: "b"}, withTime, ""),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(decision.Decision.Value, []byte(`"blockTimestamp":"0x10"`)) {
		t.Fatalf("timestamp dropped: %s", decision.Decision.Value)
	}
	empty, err := policy.(rpcengine.CorroboratingResolver).ResolveCorroborated(rpcengine.Corroboration{Floor: 1}, []rpc.Observation{
		observation(rpc.SourceRef{Name: "a", ID: "a"}, `[]`, ""),
		observation(rpc.SourceRef{Name: "b", ID: "b"}, `[]`, ""),
	})
	if err != nil || string(empty.Decision.Value) != "[]" {
		t.Fatalf("empty = %s err %v", empty.Decision.Value, err)
	}
	if _, err := policy.(rpcengine.CorroboratingResolver).ResolveCorroborated(rpcengine.Corroboration{Floor: 1}, []rpc.Observation{
		observation(rpc.SourceRef{Name: "a", ID: "a"}, `null`, ""),
	}); err == nil {
		t.Fatal("JSON null was accepted")
	}
}

// Admission validates the complete ordered response before filter binding.
// Optional timestamps and case aliases retain their separate encode-stage rules.
func TestLogsAdmissionPreservesArrayAndBindingErrorOrder(t *testing.T) {
	good := logEntry("0x0000000000000000000000000000000000000001", "0x00ff", "0xa", "false", "")
	row := func(extra string) string {
		return logEntry("0x0000000000000000000000000000000000000001", "0x00ff", "0xa", "false", extra)
	}
	array := func(value string) string { return "[" + value + "]" }
	foreignHash := "0x" + strings.Repeat("44", 32)
	cases := []struct {
		name          string
		raw           string
		filter        map[string]any
		wantErr       string
		encodeErrPart string
	}{
		{name: "empty", wantErr: "logs: expected an array"},
		{name: "whitespace", raw: " \n\t", wantErr: "logs: expected an array"},
		{name: "null", raw: "null", wantErr: "logs: expected an array"},
		{name: "spaced null", raw: " \nnull\t", wantErr: "logs: expected an array"},
		{name: "object", raw: `{}`, wantErr: "logs: expected an array"},
		{name: "number", raw: `1`, wantErr: "logs: expected an array"},
		{name: "string", raw: `"[]"`, wantErr: "logs: expected an array"},
		{name: "malformed array", raw: `[`, wantErr: "logs: expected an array"},
		{name: "trailing value", raw: `[] null`, wantErr: "logs: expected an array"},
		{name: "empty array", raw: `[]`},
		{name: "null row", raw: `[null]`, wantErr: "logs[0]: expected an object"},
		{name: "array row", raw: `[[]]`, wantErr: "logs[0]: expected an object"},
		{name: "first required field", raw: `[{}]`, wantErr: "logs[0]: missing address"},
		{name: "exact key required", raw: array(strings.Replace(good, `"data":`, `"Data":`, 1)), wantErr: "logs[0]: missing data"},
		{name: "duplicate key", raw: array(row(`"data":"0x00ff"`)), wantErr: `logs[0]: log JSON: jsonsafe: duplicate object key: "data"`},
		{name: "alias checked during encode", raw: array(row(`"Data":"oops"`)), encodeErrPart: "data"},
		{name: "timestamp checked during encode", raw: array(row(`"blockTimestamp":"oops"`)), encodeErrPart: "blockTimestamp"},
		{name: "timestamp alias checked during encode", raw: array(row(`"BlockTimestamp":"oops"`)), encodeErrPart: "blockTimestamp"},
		{name: "vendor ignored", raw: array(row(`"vendor":{"data":true}`))},
		{name: "duplicates retained", raw: array(good + "," + good)},
		{name: "first row error", raw: `[{},null]`, wantErr: "logs[0]: missing address"},
		{name: "last row precedes hash binding", raw: array(good + ",{}"), filter: map[string]any{"blockHash": foreignHash}, wantErr: "logs[1]: missing address"},
		{name: "row precedes range parsing", raw: `[{}]`, filter: map[string]any{"fromBlock": "broken"}, wantErr: "logs[0]: missing address"},
		{name: "empty array still binds filter", raw: `[]`, filter: map[string]any{"blockHash": "0x1"}, wantErr: "blockHash filter must be a 32-byte hash"},
		{name: "hash binding", raw: array(good), filter: map[string]any{"blockHash": foreignHash}, wantErr: "log 0 blockHash does not match requested blockHash"},
		{name: "hash error precedes range", raw: array(good), filter: map[string]any{"blockHash": "0x1", "fromBlock": "broken"}, wantErr: "blockHash filter must be a 32-byte hash"},
		{name: "from error precedes to", raw: array(good), filter: map[string]any{"fromBlock": "broken", "toBlock": "broken"}, wantErr: "fromBlock is invalid: invalid quantity"},
		{name: "range before optional timestamp", raw: array(row(`"blockTimestamp":"oops"`)), filter: map[string]any{"toBlock": "broken"}, wantErr: "toBlock is invalid: invalid quantity"},
		{name: "reversed range", raw: array(good), filter: map[string]any{"fromBlock": "0x10", "toBlock": "0x1"}, wantErr: "fromBlock is greater than toBlock"},
		{name: "out of range", raw: array(good), filter: map[string]any{"fromBlock": "0x0", "toBlock": "0x9"}, wantErr: "log 0 blockNumber is outside requested range"},
		{name: "earliest and upper prefix", raw: array(good), filter: map[string]any{"fromBlock": "earliest", "toBlock": "0XA"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			filter := tc.filter
			if filter == nil {
				filter = map[string]any{"fromBlock": "0x0", "toBlock": "0x10"}
			}
			req := rpc.Request{Method: MethodGetLogs, Params: []any{filter}}
			input := json.RawMessage(tc.raw)
			evidence, err := admitLogs(context.Background(), req, input, nil)
			if string(input) != tc.raw {
				t.Fatal("admission changed the input")
			}
			if evidence != nil {
				t.Fatalf("admission returned evidence: %s", evidence)
			}
			if tc.wantErr != "" {
				want := rpc.ErrMalformedResponse.Error() + ": " + tc.wantErr
				if !errors.Is(err, rpc.ErrMalformedResponse) || err.Error() != want {
					t.Fatalf("error = %v; want malformed response with text %q", err, want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.encodeErrPart != "" {
				if _, err := logobject.Encode(input); err == nil || !strings.Contains(err.Error(), tc.encodeErrPart) {
					t.Fatalf("encode error = %v; want %s", err, tc.encodeErrPart)
				}
			}
		})
	}
}
