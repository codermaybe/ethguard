package livecanary

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/pool"
	"github.com/codermaybe/ethguard/rpc"
)

const (
	testVitalik = "0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045"
	testHash    = "0x1111111111111111111111111111111111111111111111111111111111111111"
	testHeight  = uint64(100)
)

func canarySources() []rpc.SourceRef {
	return []rpc.SourceRef{
		{Name: "node-01", URL: "https://rpc-a.example"},
		{Name: "node-02", URL: "https://rpc-b.example"},
		{Name: "node-03", URL: "https://rpc-c.example"},
	}
}

// canarySummaries records the relations exercised by these report fixtures;
// only an absent member was not attempted.
func canarySummaries(sources []rpc.SourceRef, relations ...rpc.VerdictKind) []rpc.SourceSummary {
	summaries := make([]rpc.SourceSummary, len(relations))
	for i, relation := range relations {
		summaries[i] = rpc.SourceSummary{Source: sources[i], Attempted: relation != rpc.VerdictAbsent, Relation: relation}
	}
	return summaries
}

func canaryReference(tag string) *rpc.ReferenceEvidence {
	sources := canarySources()
	return &rpc.ReferenceEvidence{
		Tag:          tag,
		Hash:         testHash,
		Height:       testHeight,
		Status:       rpc.Corroborated,
		Origin:       "finalized",
		ObservedAt:   time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC),
		Participants: sources,
		Sources:      canarySummaries(sources, rpc.VerdictSupported, rpc.VerdictSupported, rpc.VerdictDissented),
		Support:      sources[:2],
		Reject:       sources[2:],
	}
}

func canaryDecision(value string) *rpc.Decision {
	sources := canarySources()
	return &rpc.Decision{
		Participants:   sources,
		Sources:        canarySummaries(sources, rpc.VerdictSupported, rpc.VerdictSupported, rpc.VerdictDissented),
		Status:         rpc.Corroborated,
		Value:          json.RawMessage(value),
		Support:        sources[:2],
		Reject:         sources[2:],
		Representative: sources[0],
	}
}

func fixedPlanStep(t *testing.T, id string) PlanStep {
	t.Helper()
	for _, step := range FixedPlan().Steps {
		if step.ID == id {
			return step
		}
	}
	t.Fatalf("plan step %q is not in the fixed plan", id)
	return PlanStep{}
}

func feeHistoryExecution(t *testing.T, reference *rpc.ReferenceEvidence) stepExecution {
	t.Helper()
	planStep := fixedPlanStep(t, "business.fee-history.finalized")
	return stepExecution{
		plan: planStep,
		logical: RequestReport{
			Method: planStep.Method,
			Params: []any{2, "finalized", []any{25, 50, 75}},
		},
		decision:  canaryDecision("{}"),
		reference: reference,
		prepared:  mustPrepared(t, planStep.Method, 2, "0x64", []any{25, 50, 75}),
	}
}

func evaluateSynthetic(t *testing.T, execution stepExecution) (StepReport, []StateReason) {
	t.Helper()
	state := &runState{manifest: testManifest(t)}
	return state.evaluateStep(execution, context.Background(), context.Background())
}

func TestRequiredSupportMatchesProductionThreshold(t *testing.T) {
	want := map[int]int{0: 1, 1: 2, 2: 2, 3: 2, 4: 3, 5: 3, 6: 4, 7: 4, 8: 5, 9: 5, 10: 6, 11: 6, 12: 7}
	for basis, expected := range want {
		if got := requiredSupport(basis); got != expected {
			t.Errorf("requiredSupport(%d) = %d, want %d", basis, got, expected)
		}
	}
}

func TestMovingTagIsRejectedAtAnyDepth(t *testing.T) {
	if !containsMovingTag([]any{"0x1", map[string]any{"block": "latest"}}) {
		t.Error("nested moving tag was not detected")
	}
	if !containsMovingTag([]any{[]any{"safe"}}) {
		t.Error("array-nested moving tag was not detected")
	}
	if containsMovingTag([]any{"0x1", map[string]any{"block": "0x64"}}) {
		t.Error("a pinned block number was reported as a moving tag")
	}
}

func TestRequestBindingMismatchFailsStep(t *testing.T) {
	planStep := fixedPlanStep(t, "reference.finalized-balance")
	reference := canaryReference("finalized")
	execution := stepExecution{
		plan: planStep,
		logical: RequestReport{
			Method: planStep.Method,
			Params: []any{testVitalik, "finalized"},
		},
		decision:  canaryDecision(`"0x1"`),
		reference: reference,
		prepared:  mustPrepared(t, planStep.Method, testVitalik, "0x2222222222222222222222222222222222222222222222222222222222222222"),
	}
	step, reasons := evaluateSynthetic(t, execution)
	if step.Invariants.RequestBinding == nil || *step.Invariants.RequestBinding {
		t.Fatalf("request_binding = %v, want false", step.Invariants.RequestBinding)
	}
	if !hasStateReason(reasons, "request_binding") {
		t.Fatalf("missing request_binding reason: %+v", reasons)
	}
	if step.Outcome != OutcomeInvariantFailed || stateFromReasons(reasons) != StateFailed {
		t.Fatalf("outcome=%s state=%s", step.Outcome, stateFromReasons(reasons))
	}
}

func TestMissingPreparedRequestIsEvidenceMissing(t *testing.T) {
	planStep := fixedPlanStep(t, "reference.finalized-balance")
	execution := stepExecution{
		plan: planStep,
		logical: RequestReport{
			Method: planStep.Method,
			Params: []any{testVitalik, "finalized"},
		},
		decision:  canaryDecision(`"0x1"`),
		reference: canaryReference("finalized"),
	}
	step, reasons := evaluateSynthetic(t, execution)
	if !hasStateReason(reasons, "evidence_missing") {
		t.Fatalf("missing evidence_missing reason: %+v", reasons)
	}
	if step.Outcome != OutcomeInvariantFailed || stateFromReasons(reasons) != StateFailed {
		t.Fatalf("outcome=%s state=%s", step.Outcome, stateFromReasons(reasons))
	}
}

func TestIllegalStatusFailsStep(t *testing.T) {
	execution := feeHistoryExecution(t, canaryReference("finalized"))
	execution.decision = canaryDecision("{}")
	execution.decision.Status = rpc.Selected
	step, reasons := evaluateSynthetic(t, execution)
	if step.Invariants.StatusAllowed == nil || *step.Invariants.StatusAllowed {
		t.Fatalf("status_allowed = %v, want false", step.Invariants.StatusAllowed)
	}
	if !hasStateReason(reasons, "illegal_state") {
		t.Fatalf("missing illegal_state reason: %+v", reasons)
	}
	if step.Outcome != OutcomeInvariantFailed || stateFromReasons(reasons) != StateFailed {
		t.Fatalf("outcome=%s state=%s", step.Outcome, stateFromReasons(reasons))
	}
}

func TestQuorumBelowThresholdFailsStep(t *testing.T) {
	execution := feeHistoryExecution(t, canaryReference("finalized"))
	sources := canarySources()
	execution.decision = &rpc.Decision{
		Participants:   sources,
		Sources:        canarySummaries(sources, rpc.VerdictSupported, rpc.VerdictDissented, rpc.VerdictUnusable),
		Status:         rpc.Corroborated,
		Value:          json.RawMessage("{}"),
		Support:        sources[:1],
		Reject:         sources[1:2],
		SourceFaults:   []rpc.SourceFault{{Source: sources[2], Err: errors.New("collect failed")}},
		Representative: sources[0],
	}
	step, reasons := evaluateSynthetic(t, execution)
	if step.Invariants.StatusAllowed == nil || *step.Invariants.StatusAllowed {
		t.Fatalf("status_allowed = %v, want false", step.Invariants.StatusAllowed)
	}
	if !hasStateReason(reasons, "illegal_state") {
		t.Fatalf("missing illegal_state reason: %+v", reasons)
	}
	if step.Outcome != OutcomeInvariantFailed || stateFromReasons(reasons) != StateFailed {
		t.Fatalf("outcome=%s state=%s", step.Outcome, stateFromReasons(reasons))
	}
}

func TestParticipantBasisDriftFailsStep(t *testing.T) {
	execution := feeHistoryExecution(t, canaryReference("finalized"))
	sources := canarySources()
	execution.decision = &rpc.Decision{
		Participants:   []rpc.SourceRef{sources[1], sources[0], sources[2]},
		Sources:        canarySummaries(sources, rpc.VerdictDissented, rpc.VerdictSupported, rpc.VerdictDissented),
		Status:         rpc.Corroborated,
		Value:          json.RawMessage("{}"),
		Support:        sources[1:2],
		Reject:         []rpc.SourceRef{sources[0], sources[2]},
		Representative: sources[1],
	}
	step, reasons := evaluateSynthetic(t, execution)
	if step.Invariants.EvidenceConservation == nil || *step.Invariants.EvidenceConservation {
		t.Fatalf("evidence_conservation = %v, want false", step.Invariants.EvidenceConservation)
	}
	if step.Invariants.BasisUnchanged == nil || *step.Invariants.BasisUnchanged {
		t.Fatalf("basis_unchanged = %v, want false", step.Invariants.BasisUnchanged)
	}
	if !hasStateReason(reasons, "evidence_missing") || !hasStateReason(reasons, "denominator_changed") {
		t.Fatalf("missing conservation reasons: %+v", reasons)
	}
	if step.Outcome != OutcomeInvariantFailed || stateFromReasons(reasons) != StateFailed {
		t.Fatalf("outcome=%s state=%s", step.Outcome, stateFromReasons(reasons))
	}
}

func TestReferenceTagMismatchIsInconclusive(t *testing.T) {
	execution := feeHistoryExecution(t, canaryReference("safe"))
	step, reasons := evaluateSynthetic(t, execution)
	if step.Invariants.ReferenceStatus == nil || *step.Invariants.ReferenceStatus {
		t.Fatalf("reference_status = %v, want false", step.Invariants.ReferenceStatus)
	}
	if !hasStateReason(reasons, "reference_not_corroborated") {
		t.Fatalf("missing reference_not_corroborated reason: %+v", reasons)
	}
	if step.Outcome != OutcomeProviderInconclusive || stateFromReasons(reasons) != StateInconclusive {
		t.Fatalf("outcome=%s state=%s", step.Outcome, stateFromReasons(reasons))
	}
}

func TestReferenceBelowQuorumIsInconclusive(t *testing.T) {
	sources := canarySources()
	reference := canaryReference("finalized")
	reference.Support = sources[:1]
	reference.Reject = sources[1:2]
	reference.Sources = canarySummaries(sources, rpc.VerdictSupported, rpc.VerdictDissented, rpc.VerdictUnusable)
	reference.SourceFaults = []rpc.SourceFault{{Source: sources[2], Err: errors.New("collect failed")}}
	execution := feeHistoryExecution(t, reference)
	step, reasons := evaluateSynthetic(t, execution)
	if step.Invariants.ReferenceStatus == nil || *step.Invariants.ReferenceStatus {
		t.Fatalf("reference_status = %v, want false", step.Invariants.ReferenceStatus)
	}
	if !hasStateReason(reasons, "reference_not_corroborated") {
		t.Fatalf("missing reference_not_corroborated reason: %+v", reasons)
	}
	if step.Outcome != OutcomeProviderInconclusive || stateFromReasons(reasons) != StateInconclusive {
		t.Fatalf("outcome=%s state=%s", step.Outcome, stateFromReasons(reasons))
	}
}

func TestErrorWithSuccessStatusIsIllegal(t *testing.T) {
	planStep := fixedPlanStep(t, "identity.chain-id")
	execution := stepExecution{
		plan: planStep,
		logical: RequestReport{
			Method: planStep.Method,
			Params: []any{},
		},
		decision:    canaryDecision(`"0x1"`),
		decisionErr: errors.New("provider failed after returning a value"),
		prepared:    mustPrepared(t, planStep.Method),
		identity:    big.NewInt(1),
	}
	step, reasons := evaluateSynthetic(t, execution)
	if step.Invariants.ErrorStatusExclusive == nil || *step.Invariants.ErrorStatusExclusive {
		t.Fatalf("error_status_exclusive = %v, want false", step.Invariants.ErrorStatusExclusive)
	}
	if !hasStateReason(reasons, "illegal_state") {
		t.Fatalf("missing illegal_state reason: %+v", reasons)
	}
	if step.Outcome != OutcomeInvariantFailed || stateFromReasons(reasons) != StateFailed {
		t.Fatalf("outcome=%s state=%s", step.Outcome, stateFromReasons(reasons))
	}
}

func TestTypedDecodeDriftFailsStep(t *testing.T) {
	planStep := fixedPlanStep(t, "typed.block")
	reference := canaryReference("finalized")
	height := testHeight + 1
	execution := stepExecution{
		plan: planStep,
		logical: RequestReport{
			Method: planStep.Method,
			Params: []any{testHash},
		},
		typed:   &TypedReport{Kind: TypedKindBlock, OK: true, Height: &height, Hash: testHash},
		typedOK: true,
	}
	state := &runState{manifest: testManifest(t), runReference: reference}
	step, reasons := state.evaluateStep(execution, context.Background(), context.Background())
	if step.Invariants.TypedDecode == nil || *step.Invariants.TypedDecode {
		t.Fatalf("typed_decode = %v, want false", step.Invariants.TypedDecode)
	}
	if !hasStateReason(reasons, "typed_decode") {
		t.Fatalf("missing typed_decode reason: %+v", reasons)
	}
	if step.Outcome != OutcomeInvariantFailed || stateFromReasons(reasons) != StateFailed {
		t.Fatalf("outcome=%s state=%s", step.Outcome, stateFromReasons(reasons))
	}
}

func TestShapeDriftIsRecordedWithoutStateReason(t *testing.T) {
	execution := feeHistoryExecution(t, canaryReference("finalized"))
	execution.decision = canaryDecision("[]")
	step, reasons := evaluateSynthetic(t, execution)
	if step.Invariants.ShapeMatchesPlan == nil || *step.Invariants.ShapeMatchesPlan {
		t.Fatalf("shape_matches_plan = %v, want false", step.Invariants.ShapeMatchesPlan)
	}
	if step.Decision == nil || step.Decision.ValueKind != ValueKindArray {
		t.Fatalf("actual value kind was not recorded: %+v", step.Decision)
	}
	if len(reasons) != 0 {
		t.Fatalf("shape drift must not become a state reason: %+v", reasons)
	}
	if step.Outcome != OutcomeDegraded || stateFromReasons(reasons) != StatePassed {
		t.Fatalf("outcome=%s state=%s", step.Outcome, stateFromReasons(reasons))
	}
}

func TestAvailabilityFaultMakesStepInconclusive(t *testing.T) {
	execution := feeHistoryExecution(t, canaryReference("finalized"))
	sources := canarySources()
	execution.decision = &rpc.Decision{
		Participants:   sources,
		Sources:        canarySummaries(sources, rpc.VerdictSupported, rpc.VerdictSupported, rpc.VerdictUnusable),
		Status:         rpc.Corroborated,
		Value:          json.RawMessage("{}"),
		Support:        sources[:2],
		SourceFaults:   []rpc.SourceFault{{Source: sources[2], Err: errors.New("rate limit exceeded")}},
		Representative: sources[0],
	}
	step, reasons := evaluateSynthetic(t, execution)
	if !hasStateReason(reasons, "provider_rate_limited") {
		t.Fatalf("missing provider_rate_limited reason: %+v", reasons)
	}
	if step.Outcome != OutcomeProviderInconclusive || stateFromReasons(reasons) != StateInconclusive {
		t.Fatalf("outcome=%s state=%s", step.Outcome, stateFromReasons(reasons))
	}
}

func TestQuorumUnmetIsInconclusive(t *testing.T) {
	execution := feeHistoryExecution(t, canaryReference("finalized"))
	sources := canarySources()
	execution.decision = &rpc.Decision{
		Participants: sources,
		Sources:      canarySummaries(sources, rpc.VerdictUnusable, rpc.VerdictUnusable, rpc.VerdictUnusable),
		Status:       rpc.Unknown,
		SourceFaults: []rpc.SourceFault{
			{Source: sources[0], Err: errors.New("no valid response")},
			{Source: sources[1], Err: errors.New("no valid response")},
			{Source: sources[2], Err: errors.New("no valid response")},
		},
	}
	execution.decisionErr = rpc.ErrNoValidResponse
	step, reasons := evaluateSynthetic(t, execution)
	if !hasStateReason(reasons, "quorum_unmet") {
		t.Fatalf("missing quorum_unmet reason: %+v", reasons)
	}
	if step.Outcome != OutcomeProviderInconclusive || stateFromReasons(reasons) != StateInconclusive {
		t.Fatalf("outcome=%s state=%s", step.Outcome, stateFromReasons(reasons))
	}
}

func TestRepresentativeMustBeFirstSupporter(t *testing.T) {
	sources := canarySources()
	decision := &rpc.Decision{
		Participants:   sources,
		Sources:        canarySummaries(sources, rpc.VerdictSupported, rpc.VerdictSupported, rpc.VerdictDissented),
		Status:         rpc.Corroborated,
		Value:          json.RawMessage("{}"),
		Support:        sources[:2],
		Reject:         sources[2:],
		Representative: sources[1],
	}
	invariant := representativeInSupport(decision)
	if invariant == nil || *invariant {
		t.Fatalf("representative_in_support = %v, want false", invariant)
	}
}

func hasStateReason(reasons []StateReason, category string) bool {
	for _, reason := range reasons {
		if reason.Category == category {
			return true
		}
	}
	return false
}

// mustPrepared builds a prepared request from Go params, mirroring the
// frozen parameter JSON the aggregator records.
func mustPrepared(t *testing.T, method string, params ...any) *rpc.PreparedRequest {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal prepared params: %v", err)
	}
	return &rpc.PreparedRequest{Method: method, Params: raw}
}

func TestSourceRelationsPreserveUnadjudicatedMembers(t *testing.T) {
	for _, test := range []struct {
		name     string
		relation rpc.VerdictKind
		cause    error
	}{
		{"excluded reference height", rpc.VerdictExcluded, nil},
		{"unassessed tail", rpc.VerdictUnassessed, nil},
		{"absent", rpc.VerdictAbsent, nil},
		{"unassessed communication fault", rpc.VerdictUnassessed, context.DeadlineExceeded},
		{"absent local gate", rpc.VerdictAbsent, pool.ErrSourceGated},
	} {
		t.Run(test.name, func(t *testing.T) {
			sources := canarySources()
			execution := feeHistoryExecution(t, canaryReference("finalized"))
			execution.decision.Reject = nil
			execution.reference.Reject = nil
			execution.decision.Sources = canarySummaries(sources, rpc.VerdictSupported, rpc.VerdictSupported, test.relation)
			execution.reference.Sources = canarySummaries(sources, rpc.VerdictSupported, rpc.VerdictSupported, test.relation)
			if test.cause != nil {
				execution.decision.SourceFaults = []rpc.SourceFault{{Source: sources[2], Err: test.cause}}
				execution.reference.SourceFaults = []rpc.SourceFault{{Source: sources[2], Err: test.cause}}
			}
			step, reasons := evaluateSynthetic(t, execution)
			if step.Invariants.EvidenceConservation == nil || !*step.Invariants.EvidenceConservation ||
				step.Invariants.ReferenceStatus == nil || !*step.Invariants.ReferenceStatus || hasStateReason(reasons, "evidence_missing") {
				t.Fatalf("unadjudicated member lost evidence: invariants=%+v reasons=%+v", step.Invariants, reasons)
			}
			if err := step.Decision.validate(); err != nil {
				t.Fatal(err)
			}
			if err := step.Reference.validate(); err != nil {
				t.Fatal(err)
			}
			for _, report := range []struct {
				sources []SourceDispositionReport
				reject  []string
				faults  []SourceFaultReport
			}{{step.Decision.Sources, step.Decision.Reject, step.Decision.Faults}, {step.Reference.Sources, step.Reference.Reject, step.Reference.Faults}} {
				if len(report.sources) != 3 || len(report.reject) != 0 {
					t.Fatalf("invented dissent or lost member: %+v", report)
				}
				last := report.sources[2]
				if last.SourceID != "node-03" || last.Relation != test.relation.String() || last.Attempted != (test.relation != rpc.VerdictAbsent) {
					t.Fatalf("source relation changed: %+v", last)
				}
				wantFaults := 0
				if test.cause != nil {
					wantFaults = 1
				}
				if len(report.faults) != wantFaults {
					t.Fatalf("faults=%+v, want %d actual causes", report.faults, wantFaults)
				}
			}
		})
	}
}

func TestCloseAfterFreezeReportsAbsentMembers(t *testing.T) {
	sources := canarySources()
	execution := feeHistoryExecution(t, canaryReference("finalized"))
	// The coordinator retains the frozen slots when Collect sees a closed pool.
	// No slot made a network attempt, and the close cause belongs to collection.
	execution.decision = &rpc.Decision{
		Participants:     sources,
		Sources:          canarySummaries(sources, rpc.VerdictAbsent, rpc.VerdictAbsent, rpc.VerdictAbsent),
		CollectionFaults: []error{pool.ErrClosed},
	}
	execution.decisionErr = pool.ErrClosed
	step, reasons := evaluateSynthetic(t, execution)
	if step.Invariants.EvidenceConservation == nil || !*step.Invariants.EvidenceConservation || hasStateReason(reasons, "evidence_missing") {
		t.Fatalf("close lost frozen members: %+v, %+v", step.Invariants, reasons)
	}
	if len(step.Decision.Sources) != 3 || len(step.Decision.Reject) != 0 || len(step.Decision.Faults) != 0 || len(step.Decision.CollectionFaults) != 1 {
		t.Fatalf("close manufactured source failures or lost collection cause: %+v", step.Decision)
	}
	for _, source := range step.Decision.Sources {
		if source.Attempted || source.Relation != "absent" {
			t.Fatalf("close source=%+v", source)
		}
	}
	if err := step.Decision.validate(); err != nil {
		t.Fatal(err)
	}
}

func TestMissingSourceSummaryFailsEvidenceChecks(t *testing.T) {
	execution := feeHistoryExecution(t, canaryReference("finalized"))
	execution.decision.Sources = execution.decision.Sources[:2]
	execution.reference.Sources = execution.reference.Sources[:2]
	step, reasons := evaluateSynthetic(t, execution)
	if step.Invariants.EvidenceConservation == nil || *step.Invariants.EvidenceConservation ||
		step.Invariants.ReferenceStatus == nil || *step.Invariants.ReferenceStatus || !hasStateReason(reasons, "evidence_missing") {
		t.Fatalf("missing sources accepted: %+v, %+v", step.Invariants, reasons)
	}
}
