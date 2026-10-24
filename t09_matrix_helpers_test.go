package ethguard_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

// T09.1 fixes one basis for every registered method (N=5, H=2, Q=3). Each
// method test names its nine scenario rows and calls t09CheckScenario once per
// row. The helper below executes exactly the row it is given: it has no row
// list, no method dispatch, and no scenario loop. It does not infer status,
// membership, or thresholds from the method's resolver, so a wrong row fails
// at the row's own method name. Scenario coverage and the registered-method
// inventory are checked in both directions.

const (
	t09MatrixN     = 5
	t09MatrixFloor = 2
)

// t09MatrixQuorum is the production formula's value for the fixed basis.
// Rows still assert the actual result; this constant only makes the expected
// boundary legible.
func t09MatrixQuorum() int {
	return rpc.Corroboration{Mode: rpc.CorroborationMajority, Floor: t09MatrixFloor}.Required(t09MatrixN)
}

type t09DimensionWant struct {
	Name     string
	Support  []int
	Reject   []int
	Abstain  []int
	Returned bool
}

type t09Want struct {
	Status       rpc.Status
	ErrIs        []error
	ErrText      string
	Participants int
	Support      []int
	Reject       []int
	Faults       int
	SourceFaults []int
	// Representative is the expected source index, or -1 for the zero value.
	Representative int
	// ValueFrom selects the scripted answer the canonical Value must still
	// decode to. -1 means no success Value is expected.
	ValueFrom int
	// ValueExact pins the canonical bytes when spelling itself is the claim.
	ValueExact string
	// TypedFrom selects the answer whose production typed projection the
	// typed probe must return. -1 skips the typed probe.
	TypedFrom int
	// TypedWantHeader overrides the method's typed projection with a header
	// summary, used only where the typed pending route reads a header.
	TypedWantHeader bool
	// TypedErr requires the typed probe to fail with this sentinel.
	TypedErr error
	// Dimensions is asserted exactly when non-nil; nil means no dimensions.
	Dimensions []t09DimensionWant
	// PreparedPending requires the prepared block parameter to remain pending.
	PreparedPending bool
	// NoProvider requires zero provider calls before any typed probe.
	NoProvider bool
	// CheckNoPending asserts the method rule has no pending form and the
	// prepared request is unchanged for a normal request.
	CheckNoPending bool
	// Note documents a method-specific boundary or expected non-success.
	Note string
}

type t09Row struct {
	Scenario string
	Params   []any
	Answers  []json.RawMessage
	// Faults lists source indices answered with an HTTP transport failure.
	Faults []int
	Want   t09Want
}

// t09MethodCase supplies only method-shape facts: the fixed request, the
// production typed probe, and the typed projection expected for a raw answer.
// The scenario expectations live in each row, not here.
type t09MethodCase struct {
	Method    string
	Params    []any
	Typed     func(t *testing.T, f *t09Fleet, params []any) (string, error)
	TypedWant func(t *testing.T, raw json.RawMessage) string
}

var t09ScenarioNames = []string{
	"all-agree",
	"minority-dissent",
	"outlier",
	"single-fault",
	"all-fault",
	"conflict",
	"notfound",
	"pending-local",
	"quorum-boundary",
}

// t09CheckScenario runs one row that the method test has named explicitly.
// Scenario membership is enforced by TestT091MatrixCoverage at the AST level;
// this helper deliberately cannot decide which rows a method runs.
func t09CheckScenario(t *testing.T, mc t09MethodCase, row t09Row) {
	t.Helper()
	t.Run(row.Scenario, func(t *testing.T) {
		t09RunScenarioRow(t, mc, row)
	})
}

func t09RunScenarioRow(t *testing.T, mc t09MethodCase, row t09Row) {
	t.Helper()
	if mc.TypedWant == nil {
		mc.TypedWant = func(t *testing.T, raw json.RawMessage) string { return "" }
	}
	if len(row.Answers) != t09MatrixN && len(row.Answers) != 0 {
		t.Fatalf("%s/%s: %d answers, want fixed basis %d", mc.Method, row.Scenario, len(row.Answers), t09MatrixN)
	}
	fleet := t09NewFleet(t, t09MatrixN)
	for i, answer := range row.Answers {
		fleet.nodes[i].ReturnRaw(mc.Method, answer)
	}
	for _, index := range row.Faults {
		fleet.failHTTP(mc.Method, index)
	}
	if row.Want.CheckNoPending {
		t09AssertNoPendingForm(t, mc.Method)
	}
	params := row.Params
	if params == nil {
		params = mc.Params
	}
	decision, err := fleet.client.Call(fleet.context(t), mc.Method, params...)
	if row.Want.NoProvider && fleet.totalCalls() != 0 {
		t.Fatalf("%s/%s: %d provider calls, want 0", mc.Method, row.Scenario, fleet.totalCalls())
	}
	t09AssertWant(t, mc, row, decision, err, fleet)
	if row.Want.TypedFrom >= 0 || row.Want.TypedErr != nil {
		if mc.Typed == nil {
			t.Fatalf("%s/%s: typed expectation on a method without a typed probe", mc.Method, row.Scenario)
		}
		got, typedErr := mc.Typed(t, fleet, params)
		if row.Want.TypedErr != nil {
			if !errors.Is(typedErr, row.Want.TypedErr) {
				t.Fatalf("%s/%s: typed error = %v, want %v", mc.Method, row.Scenario, typedErr, row.Want.TypedErr)
			}
			return
		}
		if typedErr != nil {
			t.Fatalf("%s/%s: typed probe: %v", mc.Method, row.Scenario, typedErr)
		}
		typedWant := mc.TypedWant
		if row.Want.TypedWantHeader {
			typedWant = t09TypedWantHeader
		}
		want := typedWant(t, row.Answers[row.Want.TypedFrom])
		if got != want {
			t.Fatalf("%s/%s: typed(Value) = %q, want %q", mc.Method, row.Scenario, got, want)
		}
	}
}

func t09AssertWant(t *testing.T, mc t09MethodCase, row t09Row, decision rpc.Decision, err error, fleet *t09Fleet) {
	t.Helper()
	want := row.Want
	label := mc.Method + "/" + row.Scenario
	if decision.Status != want.Status {
		t.Fatalf("%s: status = %v, want %v (err %v, Value %s)", label, decision.Status, want.Status, err, decision.Value)
	}
	if len(want.ErrIs) == 0 {
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", label, err)
		}
	} else {
		for _, sentinel := range want.ErrIs {
			if !errors.Is(err, sentinel) {
				t.Fatalf("%s: error = %v, want %v", label, err, sentinel)
			}
		}
	}
	if want.ErrText != "" && (err == nil || !strings.Contains(err.Error(), want.ErrText)) {
		t.Fatalf("%s: error = %v, want text %q", label, err, want.ErrText)
	}
	if len(decision.Participants) != want.Participants {
		t.Fatalf("%s: participants = %d, want %d", label, len(decision.Participants), want.Participants)
	}
	if len(decision.Support) != len(want.Support) {
		t.Fatalf("%s: support = %v, want %v", label, t09SourceNames(decision.Support), want.Support)
	}
	for i, index := range want.Support {
		if decision.Support[i].Name != fleet.names[index] {
			t.Fatalf("%s: support[%d] = %q, want %q", label, i, decision.Support[i].Name, fleet.names[index])
		}
	}
	if len(decision.Reject) != len(want.Reject) {
		t.Fatalf("%s: reject = %v, want %v", label, t09SourceNames(decision.Reject), want.Reject)
	}
	for i, index := range want.Reject {
		if decision.Reject[i].Name != fleet.names[index] {
			t.Fatalf("%s: reject[%d] = %q, want %q", label, i, decision.Reject[i].Name, fleet.names[index])
		}
	}
	if len(decision.Faults) != want.Faults {
		t.Fatalf("%s: %d faults, want %d: %v", label, len(decision.Faults), want.Faults, decision.Faults)
	}
	if len(decision.SourceFaults) != len(want.SourceFaults) {
		t.Fatalf("%s: source faults = %+v, want %v", label, decision.SourceFaults, want.SourceFaults)
	}
	for i, index := range want.SourceFaults {
		if decision.SourceFaults[i].Source.Name != fleet.names[index] {
			t.Fatalf("%s: source fault[%d] = %q, want %q", label, i, decision.SourceFaults[i].Source.Name, fleet.names[index])
		}
	}
	if want.Representative < 0 {
		if decision.Representative != (rpc.SourceRef{}) {
			t.Fatalf("%s: representative = %+v, want zero", label, decision.Representative)
		}
	} else if decision.Representative.Name != fleet.names[want.Representative] {
		t.Fatalf("%s: representative = %q, want %q", label, decision.Representative.Name, fleet.names[want.Representative])
	}
	if want.Participants > 0 && decision.Prepared == nil {
		t.Fatalf("%s: prepared request missing", label)
	}
	if want.Participants == 0 && decision.Prepared != nil {
		t.Fatalf("%s: prepared request present on a pre-dispatch rejection", label)
	}
	if want.PreparedPending && !t09HasPendingParam(decision.Prepared.Params) {
		t.Fatalf("%s: prepared params %v lost the pending tag", label, decision.Prepared.Params)
	}
	if want.ValueFrom >= 0 {
		if len(decision.Value) == 0 {
			t.Fatalf("%s: success Value is empty", label)
		}
		wantValue, err := t09FieldRowFor(t, mc.Method).decode(t09SemanticJSON(t, row.Answers[want.ValueFrom]))
		if err != nil {
			t.Fatalf("%s: decode expected answer: %v", label, err)
		}
		gotValue, err := t09FieldRowFor(t, mc.Method).decode(decision.Value)
		if err != nil {
			t.Fatalf("%s: decode Value %s: %v", label, decision.Value, err)
		}
		if gotValue != wantValue {
			t.Fatalf("%s: typed(Value) = %q, want %q (Value %s)", label, gotValue, wantValue, decision.Value)
		}
		if want.ValueExact != "" && string(decision.Value) != want.ValueExact {
			t.Fatalf("%s: Value = %s, want exact %s", label, decision.Value, want.ValueExact)
		}
	} else if len(decision.Value) != 0 {
		// Conflict rows name ValueFrom so their retained cluster value is
		// checked above. Every other row without a Value expectation must not
		// smuggle a stale success value through an error path.
		t.Fatalf("%s: decision without expected Value carries %s", label, decision.Value)
	}
	if want.Dimensions == nil {
		if len(decision.Dimensions) != 0 {
			t.Fatalf("%s: dimensions = %+v, want none", label, decision.Dimensions)
		}
	} else {
		if len(decision.Dimensions) != len(want.Dimensions) {
			t.Fatalf("%s: %d dimensions, want %d: %+v", label, len(decision.Dimensions), len(want.Dimensions), decision.Dimensions)
		}
		for i, dimWant := range want.Dimensions {
			dim := decision.Dimensions[i]
			if dim.Name != dimWant.Name || dim.Returned != dimWant.Returned {
				t.Fatalf("%s: dimension %d = %+v, want %+v", label, i, dim, dimWant)
			}
			t09AssertDimensionSources(t, label, dim.Name+" support", dim.Support, fleet, dimWant.Support)
			t09AssertDimensionSources(t, label, dim.Name+" reject", dim.Reject, fleet, dimWant.Reject)
			t09AssertDimensionSources(t, label, dim.Name+" abstain", dim.Abstain, fleet, dimWant.Abstain)
		}
	}
}

func t09AssertDimensionSources(t *testing.T, label, field string, got []rpc.SourceRef, fleet *t09Fleet, want []int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %s = %v, want %v", label, field, t09SourceNames(got), want)
	}
	for i, index := range want {
		if got[i].Name != fleet.names[index] {
			t.Fatalf("%s: %s[%d] = %q, want %q", label, field, i, got[i].Name, fleet.names[index])
		}
	}
}

// t09AssertNoPendingForm proves the region has no pending form at all: the
// registered policy constructor reports no pending declaration and no local
// observation route. This is the explicit not-applicable result, not a
// repeated normal request.
func t09AssertNoPendingForm(t *testing.T, method string) {
	t.Helper()
	var policy rpc.Policy
	switch method {
	case methods.MethodChainID:
		policy = methods.ChainIDPolicy()
	case methods.MethodGetBlockByHash:
		policy = methods.BlockByHashPolicy()
	case methods.MethodGetTransactionReceipt:
		policy = methods.TransactionReceiptPolicy()
	case methods.MethodGasPrice:
		policy = methods.GasPricePolicy()
	case methods.MethodMaxPriorityFeePerGas:
		policy = methods.MaxPriorityFeePerGasPolicy()
	case methods.MethodBlobBaseFee:
		policy = methods.BlobBaseFeePolicy()
	case methods.MethodBlockNumber:
		policy = methods.BlockNumberPolicy()
	case methods.MethodNetVersion:
		policy = methods.NetVersionPolicy()
	case methods.MethodPeerCount:
		policy = methods.PeerCountPolicy()
	case methods.MethodSyncing:
		policy = methods.SyncingPolicy()
	case methods.MethodListening:
		policy = methods.ListeningPolicy()
	default:
		t.Fatalf("%s: no pending-form probe declared", method)
	}
	source, ok := policy.(rpc.MethodRuleSource)
	if !ok {
		t.Fatalf("%s: policy %T does not report a method rule", method, policy)
	}
	rule, _ := source.MethodRule()
	if rule.Pending != "" {
		t.Fatalf("%s: pending declaration = %q, want none", method, rule.Pending)
	}
}

func t09Repeat(value json.RawMessage, count int) []json.RawMessage {
	out := make([]json.RawMessage, count)
	for i := range out {
		out[i] = value
	}
	return out
}

func t09HasPendingParam(params []any) bool {
	for _, param := range params {
		switch value := param.(type) {
		case string:
			if strings.EqualFold(strings.TrimSpace(value), "pending") {
				return true
			}
		case map[string]any:
			if number, ok := value["blockNumber"].(string); ok && strings.EqualFold(number, "pending") {
				return true
			}
		}
	}
	return false
}
