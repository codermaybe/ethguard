package rpc

import (
	"encoding/json"
	"errors"
	"testing"
)

// planEvidence is the established evidence the rewrite tests pin against:
// one corroborated head at height 16 with the hash the establishment agreed
// on. The tag is the level the evidence was established at; latest rides as
// the default reference with no tag.
func planEvidence(tag string) ReferenceEvidence {
	return ReferenceEvidence{
		Tag:    taggedPlanEvidence(tag),
		Hash:   "0xabc",
		Height: 16,
		Status: Corroborated,
	}
}

func taggedPlanEvidence(tag string) string {
	if tag == string(ReferenceLatest) {
		return ""
	}
	return tag
}

func TestPlanRequirementDeclaresMovingTagsAtDeclaredPositions(t *testing.T) {
	plan := ReadPlan{
		Method:    "eth_plan",
		Reference: ReferenceSpec{Kind: ReferenceShapeHashAt, Positions: []int{1}},
	}
	cases := []struct {
		name   string
		params []any
		kind   ReferenceKind
		needed bool
	}{
		{"latest", []any{"0x1", "latest"}, ReferenceLatest, true},
		{"safe", []any{"0x1", "Safe"}, ReferenceSafe, true},
		{"finalized", []any{"0x1", "finalized"}, ReferenceFinalized, true},
		{"number", []any{"0x1", "0x5"}, "", false},
		{"hash", []any{"0x1", "0xabc"}, "", false},
		{"pending", []any{"0x1", "pending"}, "", false},
		{"out of range", []any{"0x1"}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request, needed := plan.ReferenceRequirement(Request{Method: plan.Method, Params: tc.params})
			if needed != tc.needed {
				t.Fatalf("needed=%v, want %v", needed, tc.needed)
			}
			if needed && request.Kind != tc.kind {
				t.Fatalf("kind=%q, want %q", request.Kind, tc.kind)
			}
		})
	}
}

func TestPlanRequirementObjectFormIsTheObjectLayersJurisdiction(t *testing.T) {
	plan := ReadPlan{
		Method:    "eth_plan",
		Reference: ReferenceSpec{Kind: ReferenceShapeHashAt, Positions: []int{1}, EIP1898: true},
	}
	// An object with a moving blockNumber names its level; the anchored
	// layer below is never consulted for it.
	request, needed := plan.ReferenceRequirement(Request{Method: plan.Method, Params: []any{"0x1", map[string]any{"blockNumber": "safe"}}})
	if !needed || request.Kind != ReferenceSafe {
		t.Fatalf("object form: needed=%v kind=%q, want safe", needed, request.Kind)
	}
	// A hash pin needs no reference even though the anchored layer would
	// declare nothing for a non-string either.
	if _, needed := plan.ReferenceRequirement(Request{Method: plan.Method, Params: []any{"0x1", map[string]any{"blockHash": "0xabc"}}}); needed {
		t.Fatal("hash pin declared a reference")
	}
	// A string at the position belongs to the anchored layer below.
	if _, needed := plan.ReferenceRequirement(Request{Method: plan.Method, Params: []any{"0x1", "latest"}}); !needed {
		t.Fatal("string form was not declared")
	}
}

func TestPlanRequirementFieldsDefaultMissingBoundsToLatest(t *testing.T) {
	plan := ReadPlan{
		Method:    "eth_plan",
		Reference: ReferenceSpec{Kind: ReferenceShapeHeightFieldsAt, ParamPos: 0, Fields: []string{"fromBlock", "toBlock"}},
	}
	cases := []struct {
		name   string
		filter map[string]any
		kind   ReferenceKind
		needed bool
	}{
		{"both moving", map[string]any{"fromBlock": "latest", "toBlock": "latest"}, ReferenceLatest, true},
		{"missing toBlock", map[string]any{"fromBlock": "0x5"}, ReferenceLatest, true},
		{"explicit numbers", map[string]any{"fromBlock": "0x5", "toBlock": "0x6"}, "", false},
		{"hash pin", map[string]any{"blockHash": "0xabc"}, "", false},
		{"not an object", nil, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := []any{any(nil)}
			if tc.filter != nil {
				params[0] = tc.filter
			}
			request, needed := plan.ReferenceRequirement(Request{Method: plan.Method, Params: params})
			if needed != tc.needed {
				t.Fatalf("needed=%v, want %v", needed, tc.needed)
			}
			if needed && request.Kind != tc.kind {
				t.Fatalf("kind=%q, want %q", request.Kind, tc.kind)
			}
		})
	}
}

func TestPlanPrepareRewritesHashPositions(t *testing.T) {
	plan := ReadPlan{Method: "eth_plan", Reference: ReferenceSpec{Kind: ReferenceShapeHashAt, Positions: []int{1}}}
	prepared, pin, err := plan.PrepareWithReference(
		Request{Method: plan.Method, Params: []any{"0x1", "latest"}}, planEvidence("latest"), true)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Params[1] != "0xabc" {
		t.Fatalf("prepared[1]=%v, want the established hash", prepared.Params[1])
	}
	if pin != "0xabc" {
		t.Fatalf("pin=%q, want the established hash", pin)
	}
	// Explicit numbers, hashes, and pending pass through untouched, and no
	// pin is invented for them.
	for _, passthrough := range []any{"0x5", "0xabc", "pending"} {
		params := []any{"0x1", passthrough}
		prepared, pin, err := plan.PrepareWithReference(Request{Method: plan.Method, Params: params}, planEvidence("latest"), false)
		if err != nil {
			t.Fatal(err)
		}
		if prepared.Params[1] != passthrough || pin != "" {
			t.Fatalf("passthrough %v rewrote to %v (pin %q)", passthrough, prepared.Params[1], pin)
		}
	}
}

func TestPlanPrepareRewritesHeightPositionsAndFields(t *testing.T) {
	heightPlan := ReadPlan{Method: "eth_plan", Reference: ReferenceSpec{Kind: ReferenceShapeHeightAt, Positions: []int{1}}}
	prepared, pin, err := heightPlan.PrepareWithReference(
		Request{Method: heightPlan.Method, Params: []any{"0x5", "latest"}}, planEvidence("latest"), true)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Params[1] != "0x10" {
		t.Fatalf("prepared[1]=%v, want hex height 0x10", prepared.Params[1])
	}
	if pin != "0x10" {
		t.Fatalf("pin=%q, want the hex height", pin)
	}

	fieldsPlan := ReadPlan{Method: "eth_plan", Reference: ReferenceSpec{Kind: ReferenceShapeHeightFieldsAt, ParamPos: 0, Fields: []string{"fromBlock", "toBlock"}}}
	prepared, pin, err = fieldsPlan.PrepareWithReference(
		Request{Method: fieldsPlan.Method, Params: []any{map[string]any{"fromBlock": "latest"}}}, planEvidence("latest"), true)
	if err != nil {
		t.Fatal(err)
	}
	filter := prepared.Params[0].(map[string]any)
	if filter["fromBlock"] != "0x10" || filter["toBlock"] != "0x10" {
		t.Fatalf("filter=%v, want both bounds defaulted and rewritten", filter)
	}
	if pin != "0x10" {
		t.Fatalf("pin=%q, want the hex height", pin)
	}
}

func TestPlanPrepareRewritesEIP1898ObjectAndPreservesRequireCanonical(t *testing.T) {
	plan := ReadPlan{Method: "eth_plan", Reference: ReferenceSpec{Kind: ReferenceShapeHashAt, Positions: []int{1}, EIP1898: true}}
	prepared, pin, err := plan.PrepareWithReference(
		Request{Method: plan.Method, Params: []any{"0x1", map[string]any{"blockNumber": "safe", "requireCanonical": true}}},
		planEvidence("safe"), true)
	if err != nil {
		t.Fatal(err)
	}
	obj := prepared.Params[1].(map[string]any)
	if obj["blockHash"] != "0xabc" || obj["requireCanonical"] != true {
		t.Fatalf("object=%v, want the established hash with requireCanonical preserved", obj)
	}
	if pin != "0xabc" {
		t.Fatalf("pin=%q, want the established hash", pin)
	}
}

func TestPlanPrepareRejectsInvalidEIP1898ObjectsBeforeAnyRewrite(t *testing.T) {
	plan := ReadPlan{Method: "eth_plan", Reference: ReferenceSpec{Kind: ReferenceShapeHashAt, Positions: []int{1}, EIP1898: true}}
	cases := [][]any{
		{"0x1", map[string]any{"blockHash": "0xabc", "blockNumber": "latest"}},
		{"0x1", map[string]any{}},
		{"0x1", map[string]any{"blockHash": "nothex"}},
		{"0x1", map[string]any{"blockNumber": "latest", "requireCanonical": "yes"}},
	}
	for _, params := range cases {
		if _, _, err := plan.PrepareWithReference(Request{Method: plan.Method, Params: params}, planEvidence("latest"), true); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("params %v: err=%v, want ErrInvalidRequest", params, err)
		}
	}
}

func TestPlanPrepareFailsWhenEvidenceDoesNotServeTheTag(t *testing.T) {
	plan := ReadPlan{Method: "eth_plan", Reference: ReferenceSpec{Kind: ReferenceShapeHashAt, Positions: []int{0}}}
	// The read declared safe; only latest evidence exists. A level is never
	// silently downgraded: the read fails the way a missing reference would.
	_, _, err := plan.PrepareWithReference(
		Request{Method: plan.Method, Params: []any{"safe"}}, planEvidence("latest"), true)
	if !errors.Is(err, ErrReferenceRequired) {
		t.Fatalf("err=%v, want ErrReferenceRequired", err)
	}
	// Uncorroborated evidence never rewrites a request.
	uncorroborated := planEvidence("latest")
	uncorroborated.Status = Unknown
	if _, _, err := plan.PrepareWithReference(
		Request{Method: plan.Method, Params: []any{"latest"}}, uncorroborated, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("err=%v, want ErrConflict", err)
	}
}

func TestPlanPrepareRejectsMixedMovingTagsLocally(t *testing.T) {
	plan := ReadPlan{Method: "eth_plan", Reference: ReferenceSpec{Kind: ReferenceShapeHashAt, Positions: []int{0, 1}}}
	_, _, err := plan.PrepareWithReference(
		Request{Method: plan.Method, Params: []any{"latest", "safe"}}, planEvidence("latest"), true)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("err=%v, want ErrInvalidRequest", err)
	}
}

func TestPlanPendingRejectMatchesTheDecoratorSpellings(t *testing.T) {
	reject := PendingRejectAt(0, "eth_plan")
	for _, pending := range []any{"pending", "PENDING", json.RawMessage(`"pending"`), map[string]any{"blockNumber": "pending"}} {
		if err := reject(Request{Method: "eth_plan", Params: []any{pending}}); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("pending %v: err=%v, want ErrInvalidRequest", pending, err)
		}
	}
	for _, ordinary := range []any{"latest", "0x5", map[string]any{"blockHash": "0xabc"}} {
		if err := reject(Request{Method: "eth_plan", Params: []any{ordinary}}); err != nil {
			t.Fatalf("ordinary %v: err=%v", ordinary, err)
		}
	}
}

func TestPlanMethodRuleProjectsDeclarationsAndResolverRule(t *testing.T) {
	plan := ReadPlan{
		Method:       "eth_plan",
		Resolver:     Strict(func(json.RawMessage) (string, error) { return "", nil }, WithRuleCompareKind(RuleCompareQuantity), WithCanonicalValue(func(json.RawMessage) (json.RawMessage, error) { return nil, nil })),
		ResolverKind: ResolverStrict,
		Reference:    ReferenceSpec{Kind: ReferenceShapeHashAt, Positions: []int{1}, EIP1898: true},
		Pending:      PendingSpec{Mode: PendingObserve, Pos: 1},
	}
	rule := plan.MethodRule()
	want := MethodRule{
		Anchor:      "hash@1",
		BlockRef:    RuleBlockRefEIP1898,
		Pending:     RulePendingLocalObservation,
		Resolver:    RuleResolverStrict,
		CompareKind: RuleCompareQuantity,
		KeySource:   RuleKeyCanonicalBytes,
		Result:      RuleResultCanonical,
	}
	if rule != want {
		t.Fatalf("rule=%+v, want %+v", rule, want)
	}
}

func TestPlanResolveLocalObservationStandsFirstNormalizableSource(t *testing.T) {
	policy := Strict(func(value json.RawMessage) (string, error) { return string(value), nil })
	obs := []Observation{
		{Source: SourceRef{Name: "a", ID: "a"}, Err: errors.New("transport fault")},
		{Source: SourceRef{Name: "b", ID: "b"}, Value: json.RawMessage(`"0x64"`), Evidence: json.RawMessage(`"ev"`)},
		{Source: SourceRef{Name: "c", ID: "c"}, Value: json.RawMessage(`"0x65"`)},
	}
	decision, err := ResolveLocalObservation(policy, obs)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != Selected {
		t.Fatalf("status=%v, want Selected", decision.Status)
	}
	if string(decision.Value) != `"0x64"` || len(decision.Support) != 1 || decision.Support[0].ID != "b" {
		t.Fatalf("decision=%+v, want the first usable source's value", decision)
	}
	if string(decision.Provenance) != `"ev"` {
		t.Fatalf("provenance=%s, want the source's own evidence", decision.Provenance)
	}
	// No usable source fails Unknown with the no-valid-response class.
	decision, err = ResolveLocalObservation(policy, obs[:1])
	if decision.Status != Unknown || err == nil {
		t.Fatalf("no usable source: status=%v err=%v, want Unknown with an error", decision.Status, err)
	}
}
