package aggregator

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/codermaybe/ethguard/internal/rpcengine"
	"github.com/codermaybe/ethguard/rpc"
)

// A canonical internal contract fault terminates the round without blaming a
// provider: the returned error stays identifiable through errors.Is, the
// decision keeps the internal fault in its own fault list, exactly one
// observability event is recorded, and the event's fault count equals the
// final decision's — the fault is part of the decision before the event is
// folded, never appended afterwards.

func TestCanonicalInternalFaultStaysInDecisionAndEvent(t *testing.T) {
	hooks := &processingHooks{}
	brokenProcessor := func(context.Context, rpc.Request, json.RawMessage, rpc.AdmissionCaller) (rpcengine.CanonicalResult, error) {
		return rpcengine.CanonicalResult{}, rpc.ErrInvalidCanonicalResult
	}
	aggregator, _ := canonicalAggregator(t, processingPlan(hooks, brokenProcessor), `"same"`, `"same"`)
	rec := &captureRecorder{}
	aggregator.rec = rec

	decision, err := aggregator.Aggregate(context.Background(), rpc.Request{Method: "m"})
	if !errors.Is(err, rpc.ErrInvalidCanonicalResult) {
		t.Fatalf("internal fault must stay identifiable through errors.Is, got %v", err)
	}
	if decision.Status != rpc.Unknown {
		t.Fatalf("status = %v, want the internal-exit Unknown", decision.Status)
	}
	canonicalFault := false
	for _, fault := range decision.Faults {
		if errors.Is(fault, rpc.ErrInvalidCanonicalResult) {
			canonicalFault = true
		}
	}
	if !canonicalFault {
		t.Fatalf("decision.Faults must carry the canonical internal fault, got %v", decision.Faults)
	}
	if len(decision.SourceFaults) != 0 {
		t.Fatalf("an internal contract fault must never blame a provider, got %v", decision.SourceFaults)
	}
	if len(rec.events) != 1 {
		t.Fatalf("one round records exactly one decision event, got %d", len(rec.events))
	}
	if rec.events[0].Faults != len(decision.Faults) {
		t.Fatalf("event fault count %d differs from the decision's %d", rec.events[0].Faults, len(decision.Faults))
	}
	// The raw hooks never classify anyone on the internal-fault exit: no
	// normalize or encode re-runs, and no event classification re-parses.
	normalize, encode, _ := hooks.snapshot()
	if normalize != 0 || encode != 0 {
		t.Fatalf("raw hooks ran on the internal-fault exit: normalize=%d encode=%d", normalize, encode)
	}
}
