package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/aggregator"
	"github.com/codermaybe/ethguard/observe"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpctest"
)

// The plan-and-chain equivalence harness. One scripted fleet is built twice,
// and the read runs through both routes: the production plan-driven
// aggregator, and an aggregator with the hand-written chain for the same
// method. The two outcomes must agree on every decision fact; the fleets are
// separate, so source identity differs by construction.

// scriptNodes scripts one fleet. It is called once per route with fresh
// nodes, so each route observes only its own traffic.
type scriptNodes func(t *testing.T, nodes []*rpctest.Node)

// chainFor assembles the hand-written chain for the method under test,
// bound to the same establishment stage the production bundle owns.
type chainFor func(stage *ReferenceStage) rpc.Policy

// runPlanAndChain returns both routes' outcomes for one scripted read.
func runPlanAndChain(t *testing.T, script scriptNodes, chain chainFor, req rpc.Request) (planDecision rpc.Decision, planErr error, chainDecision rpc.Decision, chainErr error) {
	t.Helper()
	planDecision, planErr, _ = runEquivalenceRoute(t, script, nil, req, true)
	chainDecision, chainErr, _ = runEquivalenceRoute(t, script, chain, req, false)
	return
}

// runEquivalenceRoute drives one route. planned=true uses the production
// bundle's aggregator (the method must be registered as planned); planned=
// false builds an aggregator around the hand-written chain policy.
func runEquivalenceRoute(t *testing.T, script scriptNodes, chain chainFor, req rpc.Request, planned bool) (rpc.Decision, error, bool) {
	t.Helper()
	nodes := []*rpctest.Node{rpctest.NewNode(1), rpctest.NewNode(1), rpctest.NewNode(1)}
	defer func() {
		for _, node := range nodes {
			node.Close()
		}
	}()
	script(t, nodes)
	p := planPool(t, nodes)
	bundle, err := Build(Inputs{
		Pool:           p,
		Recorder:       observe.Nop,
		CollectTimeout: 2 * time.Second,
		Corroboration:  mustCorroboration(t, rpc.CorroborationMajority, 2),
	})
	if err != nil {
		t.Fatal(err)
	}
	var agg *aggregator.Aggregator
	if planned {
		agg = bundle.Aggregator
	} else {
		agg = aggregator.New(p, rpc.Registry{req.Method: chain(bundle.Stage)},
			aggregator.WithCorroboration(mustCorroboration(t, rpc.CorroborationMajority, 2)),
			aggregator.WithCollectTimeout(2*time.Second),
			aggregator.WithReferenceStage(bundle.Stage),
			aggregator.WithProjectors(bundle.Projectors))
	}
	decision, aggErr := agg.Aggregate(context.Background(), req)
	businessServed := false
	for _, node := range nodes {
		for _, call := range node.Calls() {
			if call.Method == req.Method {
				businessServed = true
			}
		}
	}
	return decision, aggErr, businessServed
}
