package ethguard_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/codermaybe/ethguard/pool"
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
