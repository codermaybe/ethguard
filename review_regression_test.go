package ethguard_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpctest"
	"sync"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

// Regression counterexamples added during T01 acceptance review.
func TestReviewDecoratedMethods(t *testing.T) {
	cases := []struct {
		name   string
		params []any
	}{
		{methods.MethodGetBlockReceipts, []any{"0x1"}},
		{methods.MethodGetLogs, []any{map[string]any{"fromBlock": "0x1", "toBlock": "0x1"}}},
		{methods.MethodFeeHistory, []any{"0x1", "0x1", []float64{}}},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/runtime-mode", func(t *testing.T) {
			c, nodes := fleet(t, 1, 0, 3)
			for _, n := range nodes {
				n.Return(tc.name, []any{})
			}
			nodes[2].Return(tc.name, []any{map[string]any{"different": true}})
			d, err := c.Call(context.Background(), tc.name, tc.params...)
			if err == nil && d.Status == rpc.Corroborated && len(d.Faults) == 0 {
				t.Fatalf("malformed source was not recorded: %+v", d)
			}
		})
		t.Run(tc.name+"/fixed-members", func(t *testing.T) {
			c, nodes := fleet(t, 1, 0, 5)
			for _, n := range nodes {
				n.Return(tc.name, []any{})
			}
			for _, n := range nodes[:3] {
				n.Fail(methods.MethodChainID, -32700, "outage")
			}
			for i := 0; i < 3; i++ {
				_, _ = c.Call(context.Background(), methods.MethodChainID)
			}
			for _, n := range nodes[:3] {
				n.Return(methods.MethodChainID, "0x1")
			}
			d, err := c.Call(context.Background(), tc.name, tc.params...)
			if len(d.Participants) != 5 || (err == nil && d.Status == rpc.Corroborated && len(d.Faults) == 0) {
				t.Fatalf("2-of-5 basis lost: %+v %v", d, err)
			}
		})
	}
}

func TestReviewDuplicateObservationCannotCorroborate(t *testing.T) {
	p := rpc.Strict(rpc.CanonicalJSON, rpc.WithQuorum(2))
	source := rpc.SourceRef{ID: "same", Name: "same", URL: "https://same.example"}
	obs := rpc.Observation{Source: source, Value: json.RawMessage(`"0x1"`)}
	d, err := p.Resolve([]rpc.Observation{obs, obs})
	if err == nil && d.Status == rpc.Corroborated {
		t.Fatalf("one source counted twice: %+v", d)
	}
}

func TestReviewInFlightChanges(t *testing.T) {
	for _, action := range []string{"add", "remove", "replace"} {
		t.Run(action, func(t *testing.T) {
			n := 3
			if action == "remove" || action == "replace" {
				n = 5
			}
			c, nodes := fleet(t, 1, 0, n)
			for _, node := range nodes[2:] {
				node.Fail(methods.MethodChainID, -32603, "unavailable")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			var signal sync.Once
			nodes[0].Handle(methods.MethodChainID, func(call rpctest.Call) (any, error) {
				signal.Do(func() { close(entered) })
				select {
				case <-release:
					return "0x1", nil
				case <-call.Context.Done():
					return nil, call.Context.Err()
				}
			})
			type result struct {
				d   rpc.Decision
				err error
			}
			done := make(chan result, 1)
			go func() { _, d, err := c.ChainID(ctx); done <- result{d, err} }()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("call did not reach captured voting basis")
			}
			join := func(name string) {
				node := rpctest.NewNode(1)
				t.Cleanup(node.Close)
				if _, err := c.Pool().Join(name, node.URL()); err != nil {
					t.Fatal(err)
				}
			}
			switch action {
			case "add":
				for i := 0; i < 2; i++ {
					node := rpctest.NewNode(1)
					t.Cleanup(node.Close)
					node.Fail(methods.MethodChainID, -32603, "unavailable")
					if _, err := c.Pool().Join(fmt.Sprintf("extra-%d", i), node.URL()); err != nil {
						t.Fatal(err)
					}
				}
			case "remove", "replace":
				for i := 2; i < 5; i++ {
					name := fmt.Sprintf("node-%c", 'a'+i)
					if err := c.Pool().Remove(name); err != nil {
						t.Fatal(err)
					}
					if action == "replace" {
						join(name)
					}
				}
			}
			unblock()
			var first result
			select {
			case first = <-done:
			case <-ctx.Done():
				t.Fatal("call failed to finish")
			}
			wantFirstSuccess := action != "remove" && action != "replace"
			if wantFirstSuccess {
				if first.err != nil || first.d.Status != rpc.Corroborated {
					t.Fatalf("captured rules changed: %+v", first)
				}
			} else if !errors.Is(first.err, ethguard.ErrConflict) {
				t.Fatalf("captured denominator shrank: %+v", first)
			}
			if len(first.d.Participants) != n {
				t.Fatalf("members changed in flight: got %d want %d", len(first.d.Participants), n)
			}
			_, next, err := c.ChainID(ctx)
			wantNextSuccess := !wantFirstSuccess
			if wantNextSuccess {
				if err != nil || next.Status != rpc.Corroborated {
					t.Fatalf("later call did not use new members: %+v %v", next, err)
				}
			} else if !errors.Is(err, ethguard.ErrConflict) {
				t.Fatalf("later call ignored change: %+v %v", next, err)
			}
			expectedN := n
			if action == "add" {
				expectedN += 2
			}
			if action == "remove" {
				expectedN = 2
			}
			if len(next.Participants) != expectedN {
				t.Fatalf("next members got %d want %d", len(next.Participants), expectedN)
			}
		})
	}
}

func TestReviewAllGatedKeepsEvidence(t *testing.T) {
	c, nodes := fleet(t, 1, 0, 3)
	for _, n := range nodes {
		n.Fail(methods.MethodChainID, -32700, "outage")
	}
	for i := 0; i < 3; i++ {
		_, _ = c.Call(context.Background(), methods.MethodChainID)
	}
	d, err := c.Call(context.Background(), methods.MethodChainID)
	if !errors.Is(err, pool.ErrNoEligibleSource) || len(d.Participants) != 3 {
		t.Fatalf("all-gated basis disappeared: %+v %v", d, err)
	}
}
