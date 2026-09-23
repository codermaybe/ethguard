package aggregator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"ethguard/observe"
	"ethguard/pool"
	"ethguard/rpc"
	"ethguard/rpctest"
)

func TestDecisionEventStatusMatchesSuccessClass(t *testing.T) {
	cases := []struct {
		name       string
		nodes      int
		forwarded  bool
		wantStatus rpc.Status
	}{
		{name: "corroborated", nodes: 2, wantStatus: rpc.Corroborated},
		{name: "selected", nodes: 1, wantStatus: rpc.Selected},
		{name: "forwarded", nodes: 1, forwarded: true, wantStatus: rpc.Forwarded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &captureRecorder{}
			a, _ := scriptedAggregate(t, tc.nodes, rec, tc.forwarded, quantityPolicy(), func(i int, n *rpctest.Node) {
				n.Return("m", "0x1")
			})
			decision, err := a.Aggregate(context.Background(), rpc.Request{Method: "m"})
			if err != nil {
				t.Fatal(err)
			}
			if decision.Status != tc.wantStatus {
				t.Fatalf("decision status = %s, want %s", decision.Status, tc.wantStatus)
			}
			event := oneEvent(t, rec)
			if event.Status != decision.Status.String() {
				t.Fatalf("event status = %q, want Status.String() %q", event.Status, decision.Status.String())
			}
			if event.Status == "failed" || event.Conflict {
				t.Fatalf("success recorded as a failure: %+v", event)
			}
		})
	}
}

func TestDecisionEventFailedStatusForClassifiedErrors(t *testing.T) {
	t.Run("conflict", func(t *testing.T) {
		assertFailedEvent(t, rpc.ErrConflict, true, quantityPolicy(), func(i int, n *rpctest.Node) {
			if i == 0 {
				n.Return("m", "0x1")
			} else {
				n.Return("m", "0x2")
			}
		})
	})
	t.Run("no-valid-response", func(t *testing.T) {
		assertFailedEvent(t, rpc.ErrNoValidResponse, true, quantityPolicy(), nil)
	})
	t.Run("invalid-request", func(t *testing.T) {
		assertFailedEvent(t, rpc.ErrInvalidRequest, false, prepareReject{}, nil)
	})
}

func assertFailedEvent(t *testing.T, want error, wantCalls bool, policy rpc.Policy, script func(int, *rpctest.Node)) {
	t.Helper()
	rec := &captureRecorder{}
	a, nodes := scriptedAggregate(t, 2, rec, false, policy, script)
	decision, err := a.Aggregate(context.Background(), rpc.Request{Method: "m"})
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
	for _, other := range []error{rpc.ErrConflict, rpc.ErrNoValidResponse, rpc.ErrInvalidRequest, rpc.ErrMalformedResponse} {
		if other != want && errors.Is(err, other) {
			t.Fatalf("error class swallowed by %v: %v", other, err)
		}
	}
	if decision.Status != rpc.Unknown || decision.Status.String() != "unknown" {
		t.Fatalf("failed decision status = %s, want unknown", decision.Status)
	}
	event := oneEvent(t, rec)
	if event.Status != "failed" {
		t.Fatalf("event status = %q, want failed", event.Status)
	}
	if event.Status == decision.Status.String() {
		t.Fatal("failed event reused Status.String(); failed is not a Status")
	}
	if event.Conflict != errors.Is(err, rpc.ErrConflict) {
		t.Fatalf("Conflict = %v, want %v", event.Conflict, errors.Is(err, rpc.ErrConflict))
	}
	contacted := 0
	for _, node := range nodes {
		for _, call := range node.Calls() {
			if call.Method == "m" {
				contacted++
			}
		}
	}
	if wantCalls && contacted == 0 {
		t.Fatal("expected the classified error to come from a real fan-out")
	}
	if !wantCalls && contacted != 0 {
		t.Fatalf("local rejection contacted upstream %d times", contacted)
	}
}

type prepareReject struct{ rpc.ForwardPolicy }

func (prepareReject) Prepare(context.Context, rpc.Request) (rpc.Request, error) {
	return rpc.Request{}, fmt.Errorf("rejected before fan-out: %w", rpc.ErrInvalidRequest)
}

func quantityPolicy() rpc.Policy {
	return rpc.Strict(func(v json.RawMessage) (string, error) {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return "", err
		}
		return s, nil
	})
}

func scriptedAggregate(t *testing.T, count int, rec observe.Recorder, forwarded bool, policy rpc.Policy, script func(int, *rpctest.Node)) (*Aggregator, []*rpctest.Node) {
	t.Helper()
	p := pool.NewPool()
	t.Cleanup(p.Close)
	nodes := make([]*rpctest.Node, 0, count)
	for i := range count {
		node := rpctest.NewNode(0)
		t.Cleanup(node.Close)
		if script != nil {
			script(i, node)
		}
		if _, err := p.Join(fmt.Sprintf("node-%c", 'a'+i), node.URL()); err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, node)
	}
	opts := []Option{WithRecorder(rec), WithCollectTimeout(5 * time.Second)}
	if forwarded {
		shared, err := rpc.NewCorroborationShared(rpc.CorroborationForwarded, 0)
		if err != nil {
			t.Fatal(err)
		}
		opts = append(opts, WithCorroboration(shared))
	}
	return New(p, rpc.Registry{"m": policy}, opts...), nodes
}

func oneEvent(t *testing.T, rec *captureRecorder) observe.DecisionEvent {
	t.Helper()
	if len(rec.events) != 1 {
		t.Fatalf("events = %d, want 1", len(rec.events))
	}
	return rec.events[0]
}
