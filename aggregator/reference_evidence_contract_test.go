package aggregator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

// The reference evidence handoff contract between the establishment stage and
// this coordinator. The stage returns the evidence its rounds formed beside
// the error; these tests pin what the coordinator does with each shape:
//
//   - success without evidence fails the read with ErrReferenceRequired
//     immediately — never a fabricated empty reference;
//   - a failed establishment keeps the partial evidence on the decision and
//     never starts the business round;
//   - a refused second reference attachment fails the read internally while
//     keeping the stage's evidence.
func probeAnchorRequest() rpc.Request {
	return rpc.Request{Method: probeMethod, Params: []any{"0x1", "latest"}}
}

func probePlanForTest(t *testing.T) rpc.ReadPlan {
	t.Helper()
	plan, err := methods.CompilePlan(probeSpec())
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestEstablishmentSuccessWithoutEvidenceFailsWithReferenceRequired(t *testing.T) {
	nodes, p := probeNodes(t, "0x64", 2)
	stage := &scriptedStage{noEvidence: true}
	plan := probePlanForTest(t)
	agg := New(p, WithCorroboration(mustShared(t)), WithReferenceStage(stage),
		WithPlans(map[string]rpc.ReadPlan{probeMethod: plan}))
	decision, aggErr := agg.Aggregate(context.Background(), probeAnchorRequest())
	if aggErr == nil || decision.Status != rpc.Unknown {
		t.Fatalf("evidence-less success did not fail the read: %+v %v", decision, aggErr)
	}
	if !errors.Is(aggErr, rpc.ErrReferenceRequired) {
		t.Fatalf("error %v does not wrap ErrReferenceRequired", aggErr)
	}
	if !strings.Contains(aggErr.Error(), "anchor:") {
		t.Fatalf("error %v lost the anchor wrapper", aggErr)
	}
	if decision.Reference != nil {
		t.Fatalf("empty reference fabricated: %+v", decision.Reference)
	}
	for _, node := range nodes {
		if len(node.Calls()) != 0 {
			t.Fatalf("business fan-out started without a reference: %+v", node.Calls())
		}
	}
	if len(stage.rounds) != 1 {
		t.Fatalf("establishment ran %d times, want 1", len(stage.rounds))
	}
}

func TestFailedEstablishmentKeepsItsPartialEvidence(t *testing.T) {
	nodes, p := probeNodes(t, "0x64", 2)
	cause := errors.New("reference: fleet disagreed")
	partial := rpc.ReferenceEvidence{
		Tag:    "latest",
		Status: rpc.Unknown,
		Faults: []error{errors.New("source a: connection refused")},
	}
	stage := &scriptedStage{err: cause, failEvidence: &partial}
	plan := probePlanForTest(t)
	agg := New(p, WithCorroboration(mustShared(t)), WithReferenceStage(stage),
		WithPlans(map[string]rpc.ReadPlan{probeMethod: plan}))
	decision, aggErr := agg.Aggregate(context.Background(), probeAnchorRequest())
	if aggErr == nil || decision.Status != rpc.Unknown {
		t.Fatalf("failed establishment did not fail the read: %+v %v", decision, aggErr)
	}
	if aggErr.Error() != "anchor: reference: fleet disagreed" {
		t.Fatalf("establishment cause lost: %v", aggErr)
	}
	if decision.Reference == nil {
		t.Fatal("failed establishment dropped the evidence it formed")
	}
	if decision.Reference.Tag != "latest" || decision.Reference.Status != rpc.Unknown {
		t.Fatalf("partial evidence shape changed: %+v", decision.Reference)
	}
	if len(decision.Reference.Faults) != 1 || !strings.Contains(decision.Reference.Faults[0].Error(), "source a: connection refused") {
		t.Fatalf("partial evidence lost its faults: %+v", decision.Reference.Faults)
	}
	for _, node := range nodes {
		if len(node.Calls()) != 0 {
			t.Fatalf("failed establishment still fanned out: %+v", node.Calls())
		}
	}
	if len(stage.rounds) != 1 {
		t.Fatalf("establishment ran %d times, want 1", len(stage.rounds))
	}
}

// TestSecondReferenceAttachFailsClosed pins the one-attachment rule at the
// coordinator: an invocation that already carries its reference refuses the
// stage's evidence, the read fails internally with the anchor wrapper, the
// stage's evidence stays the decision's, and no business round starts.
func TestSecondReferenceAttachFailsClosed(t *testing.T) {
	_, p := probeNodes(t, "0x64", 2)
	stage := &scriptedStage{ref: rpc.ReferenceEvidence{Hash: "0xabc", Height: 16, Status: rpc.Corroborated}}
	plan := probePlanForTest(t)
	agg := New(p, WithReferenceStage(stage), WithPlans(map[string]rpc.ReadPlan{probeMethod: plan}))
	state := &invocationState{ctx: context.Background(), plan: &plan}
	state.invocation = beginInvocation(rpc.Corroboration{}, false, nil, nil, false)
	pre, err := state.invocation.WithReference(rpc.ReferenceEvidence{Tag: "latest", Status: rpc.Corroborated})
	if err != nil {
		t.Fatal(err)
	}
	state.invocation = pre

	var (
		ref   rpc.ReferenceEvidence
		refOK bool
	)
	exit := agg.establishPlannedReference(state, time.Now(), probeAnchorRequest(), rpc.ReferenceRequest{Kind: rpc.ReferenceLatest}, &ref, &refOK)
	if exit == nil {
		t.Fatal("second attachment succeeded; the invocation must refuse it")
	}
	if !errors.Is(exit.err, rpc.ErrInvalidRequest) || !strings.Contains(exit.err.Error(), "anchor:") {
		t.Fatalf("attach refusal lost its shape: %v", exit.err)
	}
	if state.reference == nil || state.reference.Hash != "0xabc" {
		t.Fatalf("stage evidence dropped on attach refusal: %+v", state.reference)
	}
	if exit.decision.Reference == nil || exit.decision.Reference.Hash != "0xabc" {
		t.Fatalf("refusal decision lost the stage evidence: %+v", exit.decision.Reference)
	}
	if refOK {
		t.Fatal("rewrite anchor set despite the refused attachment")
	}
}
