package aggregator

import (
	"context"
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
