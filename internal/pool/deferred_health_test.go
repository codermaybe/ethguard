package pool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpctest"
)

func TestDeferredHealthPreservesTransportClassification(t *testing.T) {
	for _, route := range []string{"collect", "voting", "forward"} {
		for _, outcome := range []string{"success", "malformed", "unclassified", "capability", "invalid-params", "revert", "server", "rate-limit", "transport"} {
			t.Run(route+"/"+outcome, func(t *testing.T) {
				node := rpctest.NewNode(0)
				t.Cleanup(node.Close)
				node.Return("m", "ok")
				var responseErr error
				wantFailure := false
				switch outcome {
				case "malformed":
					responseErr, wantFailure = fmt.Errorf("contract: %w", rpc.ErrMalformedResponse), true
				case "unclassified":
					responseErr = errors.New("resolver unavailable")
				case "capability", "invalid-params", "revert":
					code := map[string]int{"capability": -32601, "invalid-params": -32602, "revert": 3}[outcome]
					node.Fail("m", code, "legal error")
					responseErr = rpc.ErrMalformedResponse
				case "server", "rate-limit":
					code := map[string]int{"server": -32603, "rate-limit": -32005}[outcome]
					node.Fail("m", code, "provider unavailable")
					wantFailure = true
				case "transport":
					node.FailHTTP("m", 503)
					wantFailure = true
				}
				p, err := NewWithMembers(0, []Member{{Name: "one", URL: node.URL()}}, WithDialer(nativehttp.NewDialer()))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(p.Close)
				collect := p.Collect
				switch route {
				case "voting":
					collect = p.CollectVoting
				case "forward":
					collect = p.Forward
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				emissions, _ := collect(ctx, "m", nil, CollectOptions{DeferHealth: true})
				if len(emissions) != 1 || !emissions[0].Attempted {
					t.Fatalf("missing attempt: %+v", emissions)
				}
				src := emissions[0].Src
				if h := src.Health(); h.Successes != 0 || h.Failures != 0 {
					t.Fatalf("transport accounted before validation: %+v", h)
				}
				originalErr := emissions[0].Response.Err()
				emissions[0].Response.Exchange.Cause = errors.New("changed after collection")
				emissions[0].Response.Answer.Result = nil
				cancel()
				var wg sync.WaitGroup
				for range 8 {
					copy := emissions[0]
					wg.Add(1)
					go func() { defer wg.Done(); copy.FinalizeHealth(responseErr) }()
				}
				wg.Wait()
				h := src.Health()
				if wantFailure {
					if h.Failures != 1 || h.Successes != 0 || h.ConsecutiveFailures != 1 {
						t.Fatalf("failure not accounted once: %+v", h)
					}
					wantErr := originalErr
					if outcome == "malformed" {
						wantErr = rpc.ErrMalformedResponse
					}
					if !errors.Is(h.LastError, wantErr) {
						t.Fatalf("lost original failure: %v, want %v", h.LastError, wantErr)
					}
				} else if h.Successes != 1 || h.Failures != 0 || h.ConsecutiveFailures != 0 {
					t.Fatalf("healthy transport classification changed: %+v", h)
				}
				if outcome == "server" && h.ProtocolServerErrors != 1 || outcome == "rate-limit" && h.RateLimited != 1 {
					t.Fatalf("lost existing failure bucket: %+v", h)
				}
			})
		}
	}
}

func TestDeferredHealthGateAndRecovery(t *testing.T) {
	node := rpctest.NewNode(0)
	t.Cleanup(node.Close)
	node.Return("m", "bad")
	p, err := NewWithMembers(0, []Member{{Name: "one", URL: node.URL()}}, WithDialer(nativehttp.NewDialer()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	src := p.VotingMembers()[0]
	for range eligibleFailureThreshold {
		emissions, err := p.CollectVoting(context.Background(), "m", nil, CollectOptions{DeferHealth: true})
		if err != nil || len(emissions) != 1 || !emissions[0].Attempted {
			t.Fatalf("attempt: %+v, %v", emissions, err)
		}
		emissions[0].FinalizeHealth(rpc.ErrMalformedResponse)
	}
	emissions, err := p.CollectVoting(context.Background(), "m", nil, CollectOptions{DeferHealth: true})
	if !errors.Is(err, ErrNoEligibleSource) || len(emissions) != 1 || emissions[0].Attempted || !errors.Is(emissions[0].Response.Err(), ErrSourceGated) {
		t.Fatalf("expected retained gated member: %+v, %v", emissions, err)
	}
	emissions[0].FinalizeHealth(rpc.ErrMalformedResponse)
	writeEmissions, writeErr := p.Broadcast(context.Background(), "eth_sendRawTransaction", nil)
	if !errors.Is(writeErr, ErrNoEligibleSource) || len(writeEmissions) != 1 || writeEmissions[0].Attempted || !errors.Is(writeEmissions[0].Response.Err(), ErrSourceGated) {
		t.Fatalf("read contract failures did not reach shared write gate: %+v, %v", writeEmissions, writeErr)
	}
	if h := src.Health(); h.Failures != eligibleFailureThreshold || h.Successes != 0 {
		t.Fatalf("gate was penalized: %+v", h)
	}
	emissions, err = p.Collect(context.Background(), "m", nil, CollectOptions{Sources: []*Source{src}, DeferHealth: true})
	if err != nil {
		t.Fatal(err)
	}
	emissions[0].FinalizeHealth(nil)
	if h := src.Health(); h.Failures != eligibleFailureThreshold || h.Successes != 1 || h.ConsecutiveFailures != 0 || !src.requestable(h.LastSuccess) {
		t.Fatalf("probe did not recover source: %+v", h)
	}
}

func TestImmediateHealthCannotBeReclassified(t *testing.T) {
	node := rpctest.NewNode(0)
	t.Cleanup(node.Close)
	node.Return("m", "ok")
	p, err := NewWithMembers(0, []Member{{Name: "one", URL: node.URL()}}, WithDialer(nativehttp.NewDialer()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	for _, collect := range []func(context.Context, string, []any, CollectOptions) ([]Emission, error){p.Collect, p.CollectVoting, p.Forward} {
		emissions, err := collect(context.Background(), "m", nil, CollectOptions{})
		if err != nil {
			t.Fatal(err)
		}
		emissions[0].FinalizeHealth(rpc.ErrMalformedResponse)
	}
	emissions, err := p.Broadcast(context.Background(), "m", nil)
	if err != nil {
		t.Fatal(err)
	}
	emissions[0].FinalizeHealth(rpc.ErrMalformedResponse)
	if h := p.VotingMembers()[0].Health(); h.Successes != 4 || h.Failures != 0 {
		t.Fatalf("default/write behavior changed: %+v", h)
	}
}
