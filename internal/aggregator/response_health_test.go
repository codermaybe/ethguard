package aggregator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/codermaybe/ethguard/internal/rpcengine"

	"github.com/codermaybe/ethguard/internal/methods"
	"github.com/codermaybe/ethguard/internal/pool"
	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpctest"
)

func responseHealthAggregator(t *testing.T, plan rpcengine.ReadPlan, mode string, bodies ...string) (*Aggregator, []*rpctest.Node) {
	t.Helper()
	var members []pool.Member
	var nodes []*rpctest.Node
	for i, body := range bodies {
		node := rpctest.NewNode(0)
		t.Cleanup(node.Close)
		node.ReturnRaw("m", json.RawMessage(body))
		nodes = append(nodes, node)
		members = append(members, pool.Member{Name: fmt.Sprintf("health-%d", i), URL: node.URL()})
	}
	p := fixedPoolWith(t, 0, []pool.PoolOption{pool.WithDialer(nativehttp.NewDialer())}, members...)
	plan.Method = "m"
	opts := []Option{WithPlans(map[string]rpcengine.ReadPlan{"m": plan})}
	if mode == "voting" || mode == "forward" {
		corroborationMode, floor := rpc.CorroborationMajority, 2
		if mode == "forward" {
			corroborationMode, floor = rpc.CorroborationForwarded, 0
		}
		shared, err := rpcengine.NewCorroborationShared(corroborationMode, floor)
		if err != nil {
			t.Fatal(err)
		}
		opts = append(opts, WithCorroboration(shared))
	}
	return New(p, opts...), nodes
}

func rawHealthPlan() rpcengine.ReadPlan {
	return rpcengine.ReadPlan{Resolver: rpcengine.Strict(func(raw json.RawMessage) (string, error) {
		return string(raw), nil
	}), ResolverKind: methods.ResolverStrict}
}

func TestResponseHealthMalformedReachesGateWithFixedBasis(t *testing.T) {
	for _, stage := range []string{"jsonsafe", "admission", "encode"} {
		t.Run(stage, func(t *testing.T) {
			plan := rawHealthPlan()
			bad := `"bad"`
			switch stage {
			case "jsonsafe":
				bad = `{"duplicate":1,"duplicate":2}`
			case "admission":
				plan.Admit = func(_ context.Context, _ rpc.Request, raw json.RawMessage, _ rpc.AdmissionCaller) (json.RawMessage, error) {
					if string(raw) == bad {
						return nil, fmt.Errorf("required field: %w", rpc.ErrMalformedResponse)
					}
					return nil, nil
				}
			case "encode":
				plan.Resolver = rpcengine.Strict(func(raw json.RawMessage) (string, error) { return string(raw), nil },
					rpcengine.WithCanonicalValue(func(raw json.RawMessage) (json.RawMessage, error) {
						if string(raw) == bad {
							return nil, fmt.Errorf("cannot encode official value: %w", rpc.ErrMalformedResponse)
						}
						return raw, nil
					}))
			}
			agg, nodes := responseHealthAggregator(t, plan, "voting", `"good"`, `"good"`, bad)
			badSource := agg.pool.VotingMembers()[2]
			for attempt := uint64(1); attempt <= 3; attempt++ {
				d, err := agg.Aggregate(context.Background(), rpc.Request{Method: "m"})
				if err != nil || d.Status != rpc.Corroborated || len(d.Support) != 2 || len(d.Participants) != 3 {
					t.Fatalf("attempt %d: decision=%+v err=%v", attempt, d, err)
				}
				h := badSource.Health()
				if h.Successes != 0 || h.Failures != attempt || h.ConsecutiveFailures != attempt || !errors.Is(h.LastError, rpc.ErrMalformedResponse) {
					t.Fatalf("attempt %d: health=%+v", attempt, h)
				}
			}
			for range 2 {
				d, err := agg.Aggregate(context.Background(), rpc.Request{Method: "m"})
				if err != nil || len(d.Participants) != 3 || len(d.Support) != 2 || len(d.SourceFaults) != 1 || !errors.Is(d.SourceFaults[0].Err, pool.ErrSourceGated) {
					t.Fatalf("gated fixed basis: decision=%+v err=%v", d, err)
				}
			}
			if h := badSource.Health(); h.Failures != 3 || h.Successes != 0 || len(nodes[2].Calls()) != 3 {
				t.Fatalf("gated source was attempted or penalized: health=%+v calls=%d", h, len(nodes[2].Calls()))
			}
			if err := agg.corrob.Set(rpc.CorroborationMajority, 3); err != nil {
				t.Fatal(err)
			}
			d, err := agg.Aggregate(context.Background(), rpc.Request{Method: "m"})
			if !errors.Is(err, rpc.ErrInsufficientSupport) || len(d.Participants) != 3 || len(d.Support) != 2 {
				t.Fatalf("gate weakened floor: decision=%+v err=%v", d, err)
			}
		})
	}
}

func TestResponseHealthValidDissentNullAndVendorDifferences(t *testing.T) {
	for _, bodies := range [][]string{
		{`"winner"`, `"winner"`, `"dissent"`},
		{`null`, `null`, `null`},
		{`{"core":1,"vendor":"a"}`, `{"core":1,"vendor":"b"}`, `{"core":1}`},
	} {
		t.Run(bodies[0], func(t *testing.T) {
			plan := rawHealthPlan()
			if bodies[0][0] == '{' {
				plan.Resolver = rpcengine.Strict(func(raw json.RawMessage) (string, error) {
					var object map[string]json.RawMessage
					err := json.Unmarshal(raw, &object)
					return string(object["core"]), err
				})
			}
			agg, _ := responseHealthAggregator(t, plan, "voting", bodies...)
			for range 4 {
				if _, err := agg.Aggregate(context.Background(), rpc.Request{Method: "m"}); err != nil {
					t.Fatal(err)
				}
			}
			for _, src := range agg.pool.VotingMembers() {
				if h := src.Health(); h.Successes != 4 || h.Failures != 0 || h.ConsecutiveFailures != 0 {
					t.Fatalf("valid answer penalized: %+v", h)
				}
			}
		})
	}
}

func TestResponseHealthLegalErrorsKeepTransportClassification(t *testing.T) {
	for _, code := range []int{-32601, -32602, 3} {
		for _, mode := range []string{"voting", "symmetric", "forward"} {
			t.Run(fmt.Sprintf("%d/%s", code, mode), func(t *testing.T) {
				plan := rawHealthPlan()
				if mode == "forward" {
					plan.Resolver = rpcengine.ForwardResolver{}
				}
				agg, nodes := responseHealthAggregator(t, plan, mode, `null`)
				nodes[0].Fail("m", code, "legal error response")
				for range 4 {
					_, _ = agg.Aggregate(context.Background(), rpc.Request{Method: "m"})
				}
				if h := agg.pool.VotingMembers()[0].Health(); h.Successes != 4 || h.Failures != 0 {
					t.Fatalf("legal RPC error penalized: %+v", h)
				}
			})
		}
	}
}

func TestResponseHealthEarlyExitsAndCancellation(t *testing.T) {
	for _, mode := range []string{"voting", "symmetric", "forward"} {
		for _, exit := range []string{"all-malformed", "transport-failure", "transport-cancel", "admission-cancel", "admission-malformed-cancel", "encode-failure", "encode-unclassified", "normalize-failure", "prepare-failure"} {
			t.Run(mode+"/"+exit, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				plan := rawHealthPlan()
				if mode == "forward" {
					plan.Resolver = rpcengine.ForwardResolver{}
				}
				body := `"ok"`
				switch exit {
				case "all-malformed":
					body = `{"x":1,"x":2}`
				case "admission-cancel":
					plan.Admit = func(context.Context, rpc.Request, json.RawMessage, rpc.AdmissionCaller) (json.RawMessage, error) {
						cancel()
						return nil, context.Canceled
					}
				case "admission-malformed-cancel":
					plan.Admit = func(context.Context, rpc.Request, json.RawMessage, rpc.AdmissionCaller) (json.RawMessage, error) {
						cancel()
						return nil, rpc.ErrMalformedResponse
					}
				case "encode-failure", "encode-unclassified":
					plan.Resolver = rpcengine.Strict(probeNormalize, rpcengine.WithCanonicalValue(func(json.RawMessage) (json.RawMessage, error) {
						if exit == "encode-unclassified" {
							return nil, rpc.ErrUnsupportedTypedObject
						}
						return nil, rpc.ErrMalformedResponse
					}))
				case "normalize-failure":
					plan.Resolver = rpcengine.Strict(func(json.RawMessage) (string, error) { return "", rpc.ErrMalformedResponse })
				case "prepare-failure":
					plan.Prepare = func(_ context.Context, req rpc.Request) (rpc.Request, error) {
						return req, errors.New("bad request")
					}
				}
				agg, nodes := responseHealthAggregator(t, plan, mode, body)
				if exit == "transport-failure" {
					nodes[0].FailHTTP("m", 503)
				} else if exit == "transport-cancel" {
					nodes[0].Handle("m", func(call rpctest.Call) (any, error) {
						cancel()
						<-call.Context.Done()
						return nil, call.Context.Err()
					})
				}
				_, err := agg.Aggregate(ctx, rpc.Request{Method: "m"})
				if err == nil {
					t.Fatal("expected early failure")
				}
				h := agg.pool.VotingMembers()[0].Health()
				if exit == "transport-cancel" || exit == "prepare-failure" {
					if h.Successes != 0 || h.Failures != 0 || h.RequestInFlight != 0 {
						t.Fatalf("caller cancellation or unattempted call penalized: %+v", h)
					}
				} else if exit == "admission-cancel" || exit == "encode-unclassified" {
					if h.Successes != 1 || h.Failures != 0 {
						t.Fatalf("successful transport lost on cancellation: %+v", h)
					}
				} else if h.Successes != 0 || h.Failures != 1 {
					t.Fatalf("early exit not accounted once: %+v", h)
				}
			})
		}
	}
}

func TestResponseHealthOverallMalformedDoesNotAssignBlame(t *testing.T) {
	plan := rawHealthPlan()
	plan.Resolver = rpcengine.Strict(func(json.RawMessage) (string, error) { return "same", nil }, rpcengine.WithCanonicalObject(probeEncode))
	agg, _ := responseHealthAggregator(t, plan, "voting", `"a"`, `"b"`, `"c"`)
	_, err := agg.Aggregate(context.Background(), rpc.Request{Method: "m"})
	if !errors.Is(err, rpc.ErrInvalidCanonicalResult) {
		t.Fatalf("expected cluster encoding failure, got %v", err)
	}
	for _, src := range agg.pool.VotingMembers() {
		if h := src.Health(); h.Successes != 1 || h.Failures != 0 {
			t.Fatalf("overall error assigned source blame: %+v", h)
		}
	}
}
