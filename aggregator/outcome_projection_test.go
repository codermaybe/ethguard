package aggregator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpctest"
)

// outcomeOpaquePolicy hides the per-source outcome capabilities while keeping
// the corroborating one, so the same resolution runs through the legacy path
// with the aggregator's own classification. It is the "no interface" branch
// of the compatibility contract.
type outcomeOpaquePolicy struct{ rpc.Policy }

func (p outcomeOpaquePolicy) ResolveCorroborated(c rpc.Corroboration, obs []rpc.Observation) (rpc.Decision, error) {
	return p.Policy.(rpc.CorroboratingPolicy).ResolveCorroborated(c, obs)
}

// quantityStrict is a plain strict policy over hex-quantity strings.
func quantityStrict(opts ...rpc.StrictOption) rpc.Policy {
	return rpc.Strict(func(v json.RawMessage) (string, error) {
		var s string
		return s, json.Unmarshal(v, &s)
	}, opts...)
}

// The outcome path and the legacy path must produce observationally identical
// aggregates: same decision evidence, same per-attempt failed flags. The
// five-node script covers every verdict class — three supporters, one valid
// dissenter, one unusable transport failure — over a five-member basis with
// the default floor.
func TestOutcomePathMatchesLegacyPath(t *testing.T) {
	corrob := mustCorroboration(t, rpc.CorroborationMajority, 2)
	nodes := []*rpctest.Node{rpctest.NewNode(1), rpctest.NewNode(1), rpctest.NewNode(1), rpctest.NewNode(1), rpctest.NewNode(1)}
	defer func() {
		for _, node := range nodes {
			node.Close()
		}
	}()
	for i := 0; i < 3; i++ {
		nodes[i].Return("eth_chainId", "0x64")
	}
	nodes[3].Return("eth_chainId", "0x65") // dissent: loses the cluster
	nodes[4].Fail("eth_chainId", -32000, "boom")

	for _, variant := range []struct {
		name   string
		hidden bool
	}{
		{name: "outcome path states verdicts"},
		{name: "legacy path hides the outcome capability", hidden: true},
	} {
		t.Run(variant.name, func(t *testing.T) {
			p := pool.NewPool()
			for i, node := range nodes {
				if _, err := p.Join(fmt.Sprintf("node-%d", i), node.URL()); err != nil {
					t.Fatal(err)
				}
			}
			policy := quantityStrict(rpc.WithQuorum(2))
			if variant.hidden {
				policy = outcomeOpaquePolicy{Policy: policy}
			}
			rec := &captureRecorder{}
			aggregate := New(p, rpc.Registry{"eth_chainId": policy},
				WithRecorder(rec), WithCorroboration(corrob), WithCollectTimeout(2*time.Second))
			decision, err := aggregate.Aggregate(context.Background(), rpc.Request{Method: "eth_chainId"})
			if err != nil {
				t.Fatalf("Aggregate: %v", err)
			}
			if decision.Status != rpc.Corroborated {
				t.Fatalf("status = %s, want corroborated", decision.Status)
			}
			if len(decision.Support) != 3 || len(decision.Reject) != 1 {
				t.Fatalf("support %v reject %v, want three supporters and one dissenter", decision.Support, decision.Reject)
			}
			if len(rec.events) != 1 {
				t.Fatalf("expected one decision event, got %d", len(rec.events))
			}
			attempts := rec.events[0].Attempts
			if len(attempts) != 5 {
				t.Fatalf("expected five attempted sources, got %+v", attempts)
			}
			failed := map[string]bool{}
			for _, attempt := range attempts {
				failed[attempt.Source] = attempt.Failed
			}
			for _, name := range []string{"node-0", "node-1", "node-2"} {
				if failed[name] {
					t.Fatalf("%s supported but was marked failed", name)
				}
			}
			if failed["node-3"] {
				t.Fatal("a valid dissenting answer must not count as failed")
			}
			if !failed["node-4"] {
				t.Fatal("a source whose answer was rejected must count as failed")
			}
		})
	}
}

// The projection from verdicts to the structured fault list keeps the exact
// attribution the legacy classification produced, including gated members
// recorded as availability faults.
func TestVerdictProjectionMatchesLegacyFaults(t *testing.T) {
	p := pool.NewPoolWithChainID(1)
	source, err := p.Join("node-01", "https://rpc-a.example")
	if err != nil {
		t.Fatal(err)
	}
	emissions := []pool.Emission{
		{Src: source, Err: pool.ErrSourceGated, Attempted: false},
	}
	verdicts := []rpc.SourceVerdict{
		{Source: rpc.SourceRef{Name: "node-01", ID: source.ID()}, Kind: rpc.VerdictAbsent},
	}
	legacy := structuredSourceFaults(nil, emissions, nil)
	verdictPath := structuredSourceFaults(nil, emissions, verdicts)
	if len(legacy) != 1 || len(verdictPath) != 1 {
		t.Fatalf("legacy %v verdict %v", legacy, verdictPath)
	}
	if legacy[0].Source.Name != verdictPath[0].Source.Name || legacy[0].Err.Error() != verdictPath[0].Err.Error() {
		t.Fatalf("attribution diverged: %+v vs %+v", legacy[0], verdictPath[0])
	}
}

// A scheduling-gated member is absent, not unusable: the resolver saw no
// observation from it, the coordinator knows the call never started.
func TestFinalizeVerdictsMarksGatedMembersAbsent(t *testing.T) {
	p := pool.NewPoolWithChainID(1)
	source, err := p.Join("node-01", "https://rpc-a.example")
	if err != nil {
		t.Fatal(err)
	}
	emissions := []pool.Emission{{Src: source, Err: pool.ErrSourceGated, Attempted: false}}
	verdicts := finalizeVerdicts([]rpc.SourceVerdict{
		{Source: rpc.SourceRef{ID: source.ID()}, Kind: rpc.VerdictUnusable},
	}, emissions)
	if len(verdicts) != 1 || verdicts[0].Kind != rpc.VerdictAbsent {
		t.Fatalf("verdicts = %+v, want the gated member absent", verdicts)
	}
}

// When verdicts are stated, the observability seam consumes them instead of
// re-running the policy's normalizer: a second Normalize on the resolve path
// is exactly the double semantics the outcome statement exists to remove.
func TestStatedVerdictsSkipSecondNormalize(t *testing.T) {
	normalizeCalls := 0
	policy := rpc.Strict(func(v json.RawMessage) (string, error) {
		normalizeCalls++
		var s string
		return s, json.Unmarshal(v, &s)
	})
	p := pool.NewPool()
	rec := &captureRecorder{}
	a := New(p, nil, WithRecorder(rec))
	sources := namedSources(t, "a")
	obs := []rpc.Observation{
		{Source: rpc.SourceRef{Name: sources[0].Name(), ID: sources[0].ID()}, Value: json.RawMessage(`"1"`)},
	}
	outcome, err := policy.(rpc.OutcomeResolver).ResolveOutcome(obs)
	if err != nil {
		t.Fatalf("ResolveOutcome: %v", err)
	}
	if len(outcome.Verdicts) != 1 || outcome.Verdicts[0].Kind != rpc.VerdictSupported {
		t.Fatalf("verdicts = %+v, want the delivered answer supported", outcome.Verdicts)
	}
	before := normalizeCalls
	a.recordDecision(time.Now(), "m", policy, rpc.Decision{Status: rpc.Unknown}, errors.New("failed"),
		[]pool.Emission{{Src: sources[0], Val: json.RawMessage(`"1"`), Attempted: true}}, outcome.Verdicts)
	if normalizeCalls != before {
		t.Fatalf("observation classification re-ran Normalize %d times", normalizeCalls-before)
	}
	if len(rec.events) != 1 || len(rec.events[0].Attempts) != 1 || rec.events[0].Attempts[0].Failed {
		t.Fatalf("a delivered usable answer must not count as failed: %+v", rec.events)
	}
}

func mustCorroboration(t *testing.T, mode rpc.CorroborationMode, floor int) *rpc.CorroborationShared {
	t.Helper()
	shared, err := rpc.NewCorroborationShared(mode, floor)
	if err != nil {
		t.Fatal(err)
	}
	return shared
}
