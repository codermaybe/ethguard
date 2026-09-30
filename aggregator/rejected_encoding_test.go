package aggregator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"ethguard/pool"
	"ethguard/rpc"
	"ethguard/rpctest"
)

func TestAggregateRejectedEncodingDoesNotChangeVotingBasis(t *testing.T) {
	sources := pool.NewPoolWithChainID(1)
	t.Cleanup(sources.Close)
	const method = "test_canonical_object"
	for i, body := range []string{`{"key":"winner","value":"v1"}`, `{"key":"winner","value":"v1"}`, `{"key":"winner","value":"v1"}`, `{"key":"loser","value":"v2"}`, `{"key":"loser","value":"v3"}`} {
		node := rpctest.NewNode(1)
		t.Cleanup(node.Close)
		node.Return(method, json.RawMessage(body))
		if _, err := sources.Join(fmt.Sprintf("source-%d", i), node.URL()); err != nil {
			t.Fatal(err)
		}
	}
	policy := rpc.Strict(func(raw json.RawMessage) (string, error) {
		var x struct {
			Key string `json:"key"`
		}
		err := json.Unmarshal(raw, &x)
		return x.Key, err
	}, rpc.WithCanonicalObject(func(raw json.RawMessage) (json.RawMessage, error) {
		var x struct {
			Value string `json:"value"`
		}
		if err := json.Unmarshal(raw, &x); err != nil {
			return nil, err
		}
		return json.Marshal(x.Value)
	}))
	corrob, err := rpc.NewCorroborationShared(rpc.CorroborationMajority, 3)
	if err != nil {
		t.Fatal(err)
	}
	agg := New(sources, rpc.Registry{method: policy}, WithCorroboration(corrob))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, floor := range []int{3, 4} {
		if err := corrob.Set(rpc.CorroborationMajority, floor); err != nil {
			t.Fatal(err)
		}
		d, err := agg.Aggregate(ctx, rpc.Request{Method: method})
		if floor == 3 {
			if err != nil || d.Status != rpc.Corroborated {
				t.Fatalf("floor3 = %+v err %v", d, err)
			}
		} else if !errors.Is(err, rpc.ErrConflict) || d.Status != rpc.Unknown {
			t.Fatalf("floor4 = %+v err %v", d, err)
		}
		if len(d.Participants) != 5 || len(d.Support) != 3 || len(d.Reject) != 2 || string(d.Value) != `"v1"` || len(d.Faults) != 1 || !errors.Is(d.Faults[0], rpc.ErrMalformedResponse) {
			t.Fatalf("voting basis = %+v", d)
		}
		for _, source := range d.Reject {
			if !strings.Contains(d.Faults[0].Error(), source.ID) {
				t.Fatalf("fault did not identify rejected source %s: %v", source.ID, d.Faults)
			}
		}
		if d.Representative != d.Support[0] {
			t.Fatalf("representative = %+v", d)
		}
	}
}
