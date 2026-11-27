package aggregator

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/rpcengine"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpctest"
)

// Mandatory source judgments feed public faults, events and health. Median
// records judgments while selecting the lower delivered decimal quantity.
func medianQuantityPlan() rpcengine.ReadPlan {
	return rpcengine.ReadPlan{Method: "m", Resolver: rpcengine.Median(func(v json.RawMessage) (string, error) {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return "", err
		}
		return s, nil
	})}
}

// A median outcome attributes each failed source once across all projections.
func TestMedianOutcomeSharesSourceJudgments(t *testing.T) {
	rec := &captureRecorder{}
	agg, _ := faultAggregator(t, medianQuantityPlan().Resolver, false, func(i int, node *rpctest.Node) {
		if i == 0 {
			node.Return("m", "5")
			return
		}
		node.FailHTTP("m", 503)
	}, WithRecorder(rec))

	decision, err := agg.Aggregate(context.Background(), rpc.Request{Method: "m"})
	if err != nil || decision.Status != rpc.Selected {
		t.Fatalf("median read = %+v, %v, want a selected decision", decision.Status, err)
	}
	if string(decision.Value) != `"5"` {
		t.Fatalf("value = %s, want the sole delivered sample", decision.Value)
	}
	if len(decision.SourceFaults) != 1 || decision.SourceFaults[0].Source.Name != "node-b" || decision.SourceFaults[0].Err == nil {
		t.Fatalf("the failed attempt must be attributed once: %+v", decision.SourceFaults)
	}
	if len(decision.Faults) != 1 || !errors.Is(decision.Faults[0], decision.SourceFaults[0].Err) {
		t.Fatalf("Faults must carry the attributed cause once: %+v / %+v", decision.Faults, decision.SourceFaults)
	}
	assertNoDuplicateFaults(t, decision)

	event := oneEvent(t, rec)
	if event.Status != rpc.Selected.String() || event.Conflict {
		t.Fatalf("event = %+v, want the selected success class", event)
	}
	if len(event.Attempts) != 2 {
		t.Fatalf("expected two attempted sources, got %+v", event.Attempts)
	}
	for i, want := range []bool{false, true} {
		if event.Attempts[i].Failed != want {
			t.Fatalf("attempt %d: Failed = %v, want %v", i, event.Attempts[i].Failed, want)
		}
	}
	for i, source := range agg.pool.VotingMembers() {
		h := source.Health()
		if i == 1 {
			if h.Successes != 0 || h.Failures != 1 {
				t.Fatalf("failed source health = %+v, want one failure", h)
			}
			continue
		}
		if h.Successes != 1 || h.Failures != 0 {
			t.Fatalf("usable source penalized: %+v", h)
		}
	}
}

// TestCollectionTimeoutExitClassifiesFromItsOwnRecords pins the degraded
// collection-timeout exit: no usable answer arrives, the fan-out deadline
// surfaces as the returned error and one collection fault, both attempted
// sources are attributed in both tables, the event reports the failed class
// with both attempts marked, and each source's health records the failed
// attempt exactly once. One early outcome preserves the collected facts for
// all projections, without evaluating candidate payloads.
func TestCollectionTimeoutExitClassifiesFromItsOwnRecords(t *testing.T) {
	rec := &captureRecorder{}
	agg, _ := faultAggregator(t, eventPolicy(), false, func(i int, node *rpctest.Node) {
		// The answer never lands before the fan-out deadline: the handler
		// waits for the request's own context, which the collect timeout
		// cancels.
		node.Handle("m", func(call rpctest.Call) (any, error) {
			<-call.Context.Done()
			return nil, call.Context.Err()
		})
	}, WithRecorder(rec), WithCollectTimeout(50*time.Millisecond))

	decision, err := agg.Aggregate(context.Background(), rpc.Request{Method: "m"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the fan-out deadline", err)
	}
	if decision.Status != rpc.Unknown {
		t.Fatalf("status = %v, want Unknown", decision.Status)
	}
	if len(decision.CollectionFaults) != 1 || !errors.Is(decision.CollectionFaults[0], context.DeadlineExceeded) {
		t.Fatalf("collection fault = %+v, want the pool failure once", decision.CollectionFaults)
	}
	if len(decision.SourceFaults) != 2 {
		t.Fatalf("both attempted sources must be attributed: %+v", decision.SourceFaults)
	}
	if len(decision.Faults) != 3 ||
		!errors.Is(decision.Faults[0], context.DeadlineExceeded) ||
		!errors.Is(decision.Faults[1], decision.SourceFaults[0].Err) ||
		!errors.Is(decision.Faults[2], decision.SourceFaults[1].Err) {
		t.Fatalf("Faults = %+v, want the collection fault then both source causes in order", decision.Faults)
	}
	assertNoDuplicateFaults(t, decision)

	event := oneEvent(t, rec)
	if event.Status != "failed" || event.Conflict {
		t.Fatalf("event = %+v, want the failed class", event)
	}
	if len(event.Attempts) != 2 {
		t.Fatalf("expected two attempted sources, got %+v", event.Attempts)
	}
	for i, attempt := range event.Attempts {
		if !attempt.Failed {
			t.Fatalf("attempt %d did not count as failed: %+v", i, attempt)
		}
	}
	for _, source := range agg.pool.VotingMembers() {
		if h := source.Health(); h.Successes != 0 || h.Failures != 1 {
			t.Fatalf("timeout health = %+v, want exactly one failure", h)
		}
	}
}
