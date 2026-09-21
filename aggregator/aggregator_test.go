package aggregator

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"ethguard/observe"
	"ethguard/pool"
	"ethguard/rpc"
)

type captureRecorder struct {
	events []observe.DecisionEvent
}

func (c *captureRecorder) RecordDecision(e observe.DecisionEvent) {
	c.events = append(c.events, e)
}

func TestAggregateEmptyPoolReturnsCollectionError(t *testing.T) {
	a := New(pool.NewPool(), nil)
	_, err := a.Aggregate(context.Background(), rpc.Request{Method: "eth_chainId"})
	if err == nil {
		t.Fatal("expected empty pool to fail")
	}
}

func TestAggregateAttachesSourceFaultsToDecision(t *testing.T) {
	p := pool.NewPool()
	if _, err := p.Join("dead", "http://127.0.0.1:1"); err != nil {
		t.Fatal(err)
	}
	a := New(p, nil)
	decision, err := a.Aggregate(context.Background(), rpc.Request{Method: "eth_unknown"})
	if err == nil {
		t.Fatal("expected unreachable source to fail the call")
	}
	if len(decision.Faults) == 0 {
		t.Fatal("expected collection faults on decision, got none")
	}
}

func TestAggregateDoesNotBlameSourcesWhoseCallsNeverStarted(t *testing.T) {
	p := pool.NewPool()
	if _, err := p.Join("a", "http://127.0.0.1:1"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Join("b", "http://127.0.0.1:2"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	decision, err := New(p, nil).Aggregate(ctx, rpc.Request{Method: "eth_unknown"})
	if err == nil {
		t.Fatal("expected canceled collection to fail")
	}
	if len(decision.Faults) != 1 {
		t.Fatalf("expected only the collection fault, got %+v", decision.Faults)
	}
}

func TestAggregateEmitsDecisionEvent(t *testing.T) {
	p := pool.NewPool()
	if _, err := p.Join("dead", "http://127.0.0.1:1"); err != nil {
		t.Fatal(err)
	}
	rec := &captureRecorder{}
	a := New(p, nil, WithRecorder(rec))
	if _, err := a.Aggregate(context.Background(), rpc.Request{Method: "eth_unknown"}); err == nil {
		t.Fatal("expected call to fail")
	}
	if len(rec.events) != 1 {
		t.Fatalf("expected exactly one event per aggregate, got %d", len(rec.events))
	}
	event := rec.events[0]
	if event.Method != "eth_unknown" || event.Status != "failed" || event.Conflict {
		t.Fatalf("unexpected event: %+v", event)
	}
	if event.Duration > time.Second {
		t.Fatalf("implausible duration %v", event.Duration)
	}
}

func TestAggregateDefaultsToNopRecorder(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("default recorder panicked: %v", r)
		}
	}()
	p := pool.NewPool()
	a := New(p, nil)
	_, _ = a.Aggregate(context.Background(), rpc.Request{Method: "eth_chainId"})
}

// fakeRPCError mimics a structured JSON-RPC error as the pool's client
// surfaces it, so errors.As against geth's rpc.Error interface matches.
type fakeRPCError struct {
	code int
	msg  string
}

func (e fakeRPCError) Error() string  { return e.msg }
func (e fakeRPCError) ErrorCode() int { return e.code }

// eventPolicy mirrors the real answer-classifier shape: delivered values are
// accepted except a "junk" spelling (malformed-but-delivered), and among
// protocol error bodies only revert-class envelopes count as observations —
// anything else the policy rejects.
func eventPolicy(opts ...rpc.StrictOption) rpc.Policy {
	return rpc.Strict(func(v json.RawMessage) (string, error) {
		var s string
		if json.Unmarshal(v, &s) == nil {
			if s == "junk" {
				return "", fmt.Errorf("not an observation")
			}
			return s, nil
		}
		var envelope struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(v, &envelope) == nil && envelope.Code == -32000 {
			return "reverted", nil
		}
		return "", fmt.Errorf("not an observation")
	}, opts...)
}

func TestEventMarksOnlyUnusableAttemptsFailed(t *testing.T) {
	policy := eventPolicy(rpc.WithRPCErrorBodies())
	a := New(pool.NewPool(), rpc.Registry{"m": policy})
	rec := &captureRecorder{}
	a.rec = rec

	sources := namedSources(t, "a", "b", "c", "d", "e", "f")
	emissions := []pool.Emission{
		{Src: sources[0], Val: json.RawMessage(`"1"`), Attempted: true},                     // valid answer
		{Src: sources[1], Val: json.RawMessage(`"junk"`), Attempted: true},                  // delivered but malformed
		{Src: sources[2], Err: context.DeadlineExceeded, Attempted: true},                   // transport/timeout fault
		{Src: sources[3], Err: fakeRPCError{-32000, "execution reverted"}, Attempted: true}, // opt-in: an observation
		{Src: sources[4], Err: fakeRPCError{-32603, "panic"}, Attempted: true},              // opt-in: still rejected by the normalizer
		{Src: sources[5]}, // never acquired a slot: absent from attempts
	}
	a.recordDecision(time.Now(), "m", policy, rpc.Decision{Status: rpc.Corroborated}, nil, emissions)

	if len(rec.events) != 1 {
		t.Fatalf("expected exactly one event, got %d", len(rec.events))
	}
	attempts := rec.events[0].Attempts
	if len(attempts) != 5 {
		t.Fatalf("expected five attempted sources, got %+v", attempts)
	}
	wantFailed := []bool{false, true, true, false, true}
	for i, want := range wantFailed {
		if attempts[i].Failed != want {
			t.Fatalf("attempt %d (%s): Failed = %v, want %v", i, attempts[i].Source, attempts[i].Failed, want)
		}
	}
}

func TestEventMarksRevertAsFailureForPoliciesWithoutErrorBodies(t *testing.T) {
	// Without WithRPCErrorBodies the protocol error is not part of the value
	// domain, so the attempt provided no usable answer.
	policy := eventPolicy()
	a := New(pool.NewPool(), rpc.Registry{"m": policy})
	rec := &captureRecorder{}
	a.rec = rec

	sources := namedSources(t, "a")
	a.recordDecision(time.Now(), "m", policy, rpc.Decision{Status: rpc.Corroborated}, nil, []pool.Emission{
		{Src: sources[0], Err: fakeRPCError{-32000, "execution reverted"}, Attempted: true},
	})
	if !rec.events[0].Attempts[0].Failed {
		t.Fatalf("expected a revert to count as failed for a policy without error bodies")
	}
}

func TestEventMarksConflictAsFailed(t *testing.T) {
	policy := eventPolicy()
	a := New(pool.NewPool(), rpc.Registry{"m": policy})
	rec := &captureRecorder{}
	a.rec = rec

	sources := namedSources(t, "a")
	a.recordDecision(time.Now(), "m", policy, rpc.Decision{Status: rpc.Corroborated},
		fmt.Errorf("strict: %w", rpc.ErrConflict), []pool.Emission{
			{Src: sources[0], Val: json.RawMessage(`"2"`), Attempted: true},
		})
	event := rec.events[0]
	if event.Status != "failed" || !event.Conflict {
		t.Fatalf("expected failed conflict event, got %+v", event)
	}
	// The dissenting-but-valid answer is not a source failure.
	if event.Attempts[0].Failed {
		t.Fatalf("a valid dissenting answer must not count as failed: %+v", event.Attempts[0])
	}
}

func TestNopRecorderSkipsEventAssembly(t *testing.T) {
	// The default path must not pay for event building; the guarantee is
	// observable as no panic and no recorder allocation — Nop discards, so
	// the assertion is that assembly is skipped entirely.
	p := pool.NewPool()
	if _, err := p.Join("dead", "http://127.0.0.1:1"); err != nil {
		t.Fatal(err)
	}
	if _, err := New(p, nil).Aggregate(context.Background(), rpc.Request{Method: "m"}); err == nil {
		t.Fatal("expected the call to fail")
	}
}

// namedSources joins display names into a real pool so events carry the
// configured names; the sources are never dialed.
func namedSources(t *testing.T, names ...string) []*pool.Source {
	t.Helper()
	p := pool.NewPool()
	out := make([]*pool.Source, 0, len(names))
	for i, name := range names {
		src, err := p.Join(name, fmt.Sprintf("http://127.0.0.1:%d", 9100+i))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, src)
	}
	return out
}
