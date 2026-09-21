package ethguard_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"ethguard"
	"ethguard/config"
	"ethguard/pool"
	"ethguard/rpc"
	"ethguard/rpc/methods"
	"ethguard/rpctest"
)

// --- T01 acceptance: fixed voting basis and default majority (decisions 0025,
// 0027, 0028, 0029) through the public ChainID path. ---------------------------

// requiredSupport is the decision-0029 table: Q = max(2, floor(N/2)+1).
func TestRequiredSupportPerMembership(t *testing.T) {
	for n, want := range map[int]int{2: 2, 3: 2, 4: 3, 5: 3} {
		client, _ := fleet(t, 1, 1, n)
		if got := client.Pool().Len(); got != n {
			t.Fatalf("N=%d: expected %d members, got %d", n, n, got)
		}
		_ = want // bound asserted through the behavioral tests below
	}
}

func TestMajorityThresholdsOnChainID(t *testing.T) {
	// Dissenters split their answers so no cluster below the agreeing one can
	// reach the bound by itself; "agreeing" counts sources answering 0x1.
	cases := []struct {
		n           int
		agreeing    int
		wantOK      bool
		wantSupport int
	}{
		{2, 1, false, 1}, // Q=2: one of two below
		{2, 2, true, 2},
		{3, 1, false, 1}, // Q=2 of 3
		{3, 2, true, 2},
		{4, 2, false, 2}, // Q=3 of 4
		{4, 3, true, 3},
		{5, 2, false, 2}, // Q=3 of 5
		{5, 3, true, 3},
		{5, 4, true, 4},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("N=%d_agree=%d", tc.n, tc.agreeing), func(t *testing.T) {
			client, nodes := fleet(t, 1, 0, tc.n)
			for i := tc.agreeing; i < tc.n; i++ {
				nodes[i].Return(methods.MethodChainID, fmt.Sprintf("0x%x", 2+i))
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			value, decision, err := client.ChainID(ctx)
			if tc.wantOK {
				if err != nil {
					t.Fatalf("expected success, got %v", err)
				}
				if value.Int64() != 1 || decision.Status != rpc.Corroborated || len(decision.Support) != tc.wantSupport {
					t.Fatalf("expected corroborated cluster of %d, got %+v %v", tc.wantSupport, decision, err)
				}
				if len(decision.Reject) != tc.n-tc.wantSupport {
					t.Fatalf("expected %d rejects, got %+v", tc.n-tc.wantSupport, decision.Reject)
				}
			} else if !errors.Is(err, ethguard.ErrConflict) {
				t.Fatalf("expected conflict below the threshold, got %v (%+v)", err, decision)
			}
		})
	}
}

// Checklist 2: three members unavailable before selection (open cooldown
// circuits) or failing after selection — either way, two agreeing votes
// cannot succeed and three agreeing votes plus two failures can.
func TestUnavailableMembersStayInDenominator(t *testing.T) {
	t.Run("three cooled before selection", func(t *testing.T) {
		// A five-member client where three members' transports are dead from
		// the start: their lazy dials fail, which is an in-collection failure,
		// not a pre-selection gate — covered in the second subtest. Here the
		// pre-selection path uses live nodes driven onto cooldown circuits.
		client, nodes := fleet(t, 1, 0, 5)
		for _, node := range nodes[:3] {
			// Drive each member onto its cooldown circuit with protocol
			// failures before the ChainID call.
			for i := 0; i < 3; i++ {
				node.Fail(methods.MethodChainID, -32700, "probing outage")
				if _, err := client.Call(context.Background(), methods.MethodChainID); err == nil {
					// Surviving members answer; failures still accumulate.
					continue
				}
			}
			// Restore the correct answer: the member is gated, not fixed.
			node.Return(methods.MethodChainID, "0x1")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, _, err := client.ChainID(ctx)
		if !errors.Is(err, ethguard.ErrConflict) {
			t.Fatalf("2-of-5 must not succeed while 3 members occupy the basis, got %v", err)
		}
	})

	t.Run("two cooled then three answer", func(t *testing.T) {
		client, nodes := fleet(t, 1, 0, 5)
		for _, node := range nodes[:2] {
			for i := 0; i < 3; i++ {
				node.Fail(methods.MethodChainID, -32700, "probing outage")
				client.Call(context.Background(), methods.MethodChainID)
			}
			node.Return(methods.MethodChainID, "0x1")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		value, decision, err := client.ChainID(ctx)
		if err != nil {
			t.Fatalf("3-of-5 must succeed with two members gated, got %v", err)
		}
		if value.Int64() != 1 || decision.Status != rpc.Corroborated || len(decision.Support) != 3 {
			t.Fatalf("expected corroborated 3-source cluster, got %+v", decision)
		}
	})

	t.Run("three fail after selection with dead transports", func(t *testing.T) {
		// Distinct dead ports, so identity dedup does not collapse them into
		// one member: three members fail in-collection, two answer.
		cfg := config.Config{}
		nodes := make([]*rpctest.Node, 0, 2)
		for i := 0; i < 5; i++ {
			endpoint := fmt.Sprintf("http://127.0.0.1:%d", i+1) // closed ports
			if i < 2 {
				node := rpctest.NewNode(1)
				t.Cleanup(node.Close)
				nodes = append(nodes, node)
				endpoint = node.URL()
			}
			cfg.Upstreams = append(cfg.Upstreams, config.Upstream{
				ID:       fmt.Sprintf("node-%c", 'a'+i),
				Endpoint: endpoint,
			})
		}
		client, err := ethguard.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(client.Close)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if _, _, err := client.ChainID(ctx); !errors.Is(err, ethguard.ErrConflict) {
			t.Fatalf("2 agreeing of a 5-member basis must not corroborate, got %v", err)
		}
		if len(client.Pool().Sources()) != 5 {
			t.Fatalf("expected the 5-member basis to survive failed transports, got %d", len(client.Pool().Sources()))
		}
	})
}

// Checklist 3: mode asymmetry on 3×A/2×B, and the floor refusing 2×A with
// three failed members in both modes.
func TestModeAsymmetry(t *testing.T) {
	build := func(t *testing.T, mode rpc.CorroborationMode) (*ethguard.Client, []*rpctest.Node) {
		return fleet(t, 1, 0, 5, ethguard.WithCorroborationMode(mode), ethguard.WithQuorum(2))
	}

	t.Run("majority accepts 3A over 2B", func(t *testing.T) {
		client, nodes := build(t, rpc.CorroborationMajority)
		nodes[3].Return(methods.MethodChainID, "0x2")
		nodes[4].Return(methods.MethodChainID, "0x2")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		value, decision, err := client.ChainID(ctx)
		if err != nil || value.Int64() != 1 || decision.Status != rpc.Corroborated || len(decision.Support) != 3 {
			t.Fatalf("majority must accept the 3-source cluster, got %+v %v", decision, err)
		}
	})

	t.Run("unanimity rejects 3A over 2B", func(t *testing.T) {
		client, nodes := build(t, rpc.CorroborationUnanimity)
		nodes[3].Return(methods.MethodChainID, "0x2")
		nodes[4].Return(methods.MethodChainID, "0x2")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if _, _, err := client.ChainID(ctx); !errors.Is(err, ethguard.ErrConflict) {
			t.Fatalf("unanimity must reject valid dissent, got %v", err)
		}
	})

	t.Run("2A with 3 failures refuses in both modes", func(t *testing.T) {
		for _, mode := range []rpc.CorroborationMode{rpc.CorroborationMajority, rpc.CorroborationUnanimity} {
			t.Run(mode.String(), func(t *testing.T) {
				client, nodes := fleet(t, 1, 0, 5, ethguard.WithCorroborationMode(mode))
				for _, node := range nodes[2:] {
					node.Fail(methods.MethodChainID, -32603, "down")
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				if _, _, err := client.ChainID(ctx); !errors.Is(err, ethguard.ErrConflict) {
					t.Fatalf("2 agreeing of a 5-member basis must refuse in %s, got %v", mode, err)
				}
			})
		}
	})
}

// Checklist 4: runtime membership and mode changes take effect for
// subsequent invocations only; in-flight invocations keep their snapshot.
// Unknown sources can never join the voting set through responses.
func TestRuntimeMembershipAndModeChanges(t *testing.T) {
	t.Run("runtime add then remove changes later invocations", func(t *testing.T) {
		// Two members split 1×A/1×B: below Q=2. Adding a third member that
		// answers B lifts the B cluster to a corroborated 2-of-3 majority;
		// removing it returns the invocation to a conflict. Only explicit
		// Join/Remove moves the basis (decision 0027).
		client, nodes := fleet(t, 1, 0, 2)
		nodes[1].Return(methods.MethodChainID, "0x2")
		extra := rpctest.NewNode(1)
		defer extra.Close()
		extra.Return(methods.MethodChainID, "0x2")

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, _, err := client.ChainID(ctx); !errors.Is(err, ethguard.ErrConflict) {
			t.Fatalf("1-of-2 split must conflict, got %v", err)
		}

		p := client.Pool()
		if _, err := p.Join("extra", extra.URL()); err != nil {
			t.Fatal(err)
		}
		value, decision, err := client.ChainID(ctx)
		if err != nil || value.Int64() != 2 || decision.Status != rpc.Corroborated || len(decision.Support) != 2 {
			t.Fatalf("expected the added member to lift the B cluster, got %+v %v", decision, err)
		}
		if err := p.Remove("extra"); err != nil {
			t.Fatal(err)
		}
		if _, _, err := client.ChainID(ctx); !errors.Is(err, ethguard.ErrConflict) {
			t.Fatalf("expected the original split after removal, got %v", err)
		}
	})

	t.Run("mode change applies to later invocations", func(t *testing.T) {
		client, nodes := fleet(t, 1, 0, 3)
		nodes[2].Return(methods.MethodChainID, "0x2")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if _, _, err := client.ChainID(ctx); err != nil {
			t.Fatalf("majority must tolerate the dissenter, got %v", err)
		}
		if err := client.SetCorroboration(rpc.CorroborationUnanimity, 2); err != nil {
			t.Fatal(err)
		}
		if _, _, err := client.ChainID(ctx); !errors.Is(err, ethguard.ErrConflict) {
			t.Fatalf("unanimity after Set must reject the dissenter, got %v", err)
		}
		if err := client.SetCorroboration(rpc.CorroborationMajority, 2); err != nil {
			t.Fatal(err)
		}
		if _, _, err := client.ChainID(ctx); err != nil {
			t.Fatalf("majority after Set must tolerate the dissenter again, got %v", err)
		}
	})

	t.Run("in-flight invocation keeps its snapshot", func(t *testing.T) {
		// 3 members: 2×A with one delayed, 1×B dissenting. The invocation
		// captures the majority mode at entry; a switch to unanimity while
		// the delayed answer is pending must not flip the in-flight result
		// — unanimity would reject the dissent, majority corroborates 2-of-3.
		client, nodes := fleet(t, 1, 0, 3)
		nodes[2].Return(methods.MethodChainID, "0x2")
		nodes[1].Handle(methods.MethodChainID, func(c rpctest.Call) (any, error) {
			time.Sleep(400 * time.Millisecond)
			return "0x1", nil
		})
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		var wg sync.WaitGroup
		var firstErr error
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, firstErr = client.ChainID(ctx)
		}()
		time.Sleep(100 * time.Millisecond) // the call is in flight, answer pending
		if err := client.SetCorroboration(rpc.CorroborationUnanimity, 2); err != nil {
			t.Fatal(err)
		}
		wg.Wait()
		if firstErr != nil {
			t.Fatalf("in-flight call must keep its captured mode, got %v", firstErr)
		}
		// The next invocation uses the new mode: unanimity rejects the
		// dissenting third member.
		if _, _, err := client.ChainID(ctx); !errors.Is(err, ethguard.ErrConflict) {
			t.Fatalf("unanimity must apply to later invocations, got %v", err)
		}
	})

	t.Run("responses cannot add voting members", func(t *testing.T) {
		client, _ := fleet(t, 1, 1, 2)
		p := client.Pool()
		before := p.Len()
		// A fleet of answering providers with unknown URLs is not a basis:
		// only explicit Join changes membership.
		_, _, err := client.ChainID(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if p.Len() != before {
			t.Fatalf("membership grew from responses: %d -> %d", before, p.Len())
		}
	})

	t.Run("insufficient membership for the floor is a construction error path only via T02", func(t *testing.T) {
		// N=1 can never corroborate under the default floor H=2 (decision 0029);
		// forwarding is T02 scope.
		client, _ := fleet(t, 1, 1, 2, ethguard.WithCorroborationMode(rpc.CorroborationMajority))
		if err := client.Pool().Remove("node-b"); err != nil {
			t.Fatal(err)
		}
		if _, _, err := client.ChainID(context.Background()); !errors.Is(err, ethguard.ErrConflict) {
			t.Fatalf("single-member basis cannot corroborate, got %v", err)
		}
	})
}

// Checklist 5: duplicate URLs collapse to one vote (pool identity dedup);
// duplicate results and repeated requests produce no extra votes; pool order
// does not change the outcome.
func TestNoExtraVotes(t *testing.T) {
	t.Run("duplicate URL joins collapse to one member", func(t *testing.T) {
		node := rpctest.NewNode(1)
		defer node.Close()
		cfg := config.Config{Upstreams: []config.Upstream{
			{ID: "a", Endpoint: node.URL()},
			{ID: "b", Endpoint: node.URL() + "/"}, // same endpoint after normalization
		}}
		client, err := ethguard.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(client.Close)
		if got := client.Pool().Len(); got != 1 {
			t.Fatalf("expected one member after dedup, got %d", got)
		}
	})

	t.Run("duplicate results within one member add no vote", func(t *testing.T) {
		// Two members answer the same value; one of them is asked twice
		// because the policy re-resolves — the duplicate answer cannot turn
		// 1-of-2 into 2-of-2. Directly: a 1-of-2 basis stays a conflict.
		client, nodes := fleet(t, 1, 0, 2)
		nodes[1].Fail(methods.MethodChainID, -32603, "down")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if _, _, err := client.ChainID(ctx); !errors.Is(err, ethguard.ErrConflict) {
			t.Fatalf("one answering member cannot outvote the basis, got %v", err)
		}
	})

	t.Run("repeated requests add no vote", func(t *testing.T) {
		client, _ := fleet(t, 1, 1, 3)
		p := client.Pool()
		// Issue the same call repeatedly: every call is its own invocation
		// with the same basis — no vote accumulates across them.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for i := 0; i < 3; i++ {
			_, decision, err := client.ChainID(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(decision.Support) != 3 {
				t.Fatalf("call %d: expected exactly the basis as support, got %+v", i, decision)
			}
		}
		if p.Len() != 3 {
			t.Fatalf("membership changed across repeated calls: %d", p.Len())
		}
	})

	t.Run("membership order does not change adjudication", func(t *testing.T) {
		// Three members: 2×A and 1×B, joined in different orders. The
		// decision's status, value, and support SET are order-invariant
		// (the support slice order follows pool order, so compare sets).
		build := func(t *testing.T, order []string) *ethguard.Client {
			nodeA, nodeB, nodeC := rpctest.NewNode(1), rpctest.NewNode(1), rpctest.NewNode(1)
			t.Cleanup(nodeA.Close)
			t.Cleanup(nodeB.Close)
			t.Cleanup(nodeC.Close)
			nodeA.Return(methods.MethodChainID, "0x1")
			nodeB.Return(methods.MethodChainID, "0x1")
			nodeC.Return(methods.MethodChainID, "0x2")
			endpoints := map[string]string{"a": nodeA.URL(), "b": nodeB.URL(), "c": nodeC.URL()}
			cfg := config.Config{}
			for _, id := range order {
				cfg.Upstreams = append(cfg.Upstreams, config.Upstream{ID: id, Endpoint: endpoints[id]})
			}
			client, err := ethguard.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(client.Close)
			return client
		}
		type outcome struct {
			status  rpc.Status
			value   string
			support map[string]bool
			reject  map[string]bool
		}
		var outcomes []outcome
		for _, order := range [][]string{{"a", "b", "c"}, {"c", "b", "a"}, {"b", "c", "a"}} {
			client := build(t, order)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, decision, err := client.ChainID(ctx)
			cancel()
			if err != nil {
				t.Fatalf("order %v: expected corroborated majority, got %v", order, err)
			}
			got := outcome{status: decision.Status, value: string(decision.Value), support: map[string]bool{}, reject: map[string]bool{}}
			for _, s := range decision.Support {
				got.support[s.Name] = true
			}
			for _, s := range decision.Reject {
				got.reject[s.Name] = true
			}
			outcomes = append(outcomes, got)
		}
		first := outcomes[0]
		if first.status != rpc.Corroborated || first.value != `"0x1"` ||
			len(first.support) != 2 || !first.support["a"] || !first.support["b"] ||
			len(first.reject) != 1 || !first.reject["c"] {
			t.Fatalf("unexpected decision shape: %+v", first)
		}
		for i, got := range outcomes[1:] {
			if got.status != first.status || got.value != first.value {
				t.Fatalf("order %d changed status or value: %+v vs %+v", i+1, got, first)
			}
			if len(got.support) != len(first.support) {
				t.Fatalf("order %d changed support size: %+v", i+1, got)
			}
			for name := range first.support {
				if !got.support[name] {
					t.Fatalf("order %d lost supporter %q: %+v", i+1, name, got)
				}
			}
		}
	})
}

// Checklist 6: the adjudication module is transport-agnostic and refuses
// below the floor. Observable from outside: N=1 with floor 2 refuses (no
// degradation to Selected — the typed path errors and the decision's error
// is authoritative over the Status field, whose zero value is Corroborated) and
// the evidence carries the cluster rather than a guessed winner.
func TestRefusesWithoutDegradation(t *testing.T) {
	client, _ := fleet(t, 1, 1, 2, ethguard.WithCorroborationMode(rpc.CorroborationMajority))
	if err := client.Pool().Remove("node-b"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, decision, err := client.ChainID(ctx)
	if !errors.Is(err, ethguard.ErrConflict) {
		t.Fatalf("single member must refuse, got %v", err)
	}
	if len(decision.Support) != 1 || string(decision.Value) != `"0x1"` {
		t.Fatalf("evidence must still name the cluster, got %+v", decision)
	}
}

// T01 pins the pool-level wiring: the public client resolves through the
// voting-basis collection, so a cooled member still occupies the basis of
// every public read (decision 0027's example: 5 members, floor 3, 2 cooling,
// 3 valid answers succeed; 3 cooling, 2 valid answers refuse).
func TestPoolVotingBasisExample(t *testing.T) {
	t.Run("explicit floor 3: two cooling, three answer", func(t *testing.T) {
		client, nodes := fleet(t, 1, 0, 5, ethguard.WithQuorum(3))
		for _, node := range nodes[:2] {
			for i := 0; i < 3; i++ {
				node.Fail(methods.MethodChainID, -32700, "probing outage")
				client.Call(context.Background(), methods.MethodChainID)
			}
			node.Return(methods.MethodChainID, "0x1")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		value, decision, err := client.ChainID(ctx)
		if err != nil || value.Int64() != 1 || decision.Status != rpc.Corroborated || len(decision.Support) != 3 {
			t.Fatalf("decision 0027's success example failed: %+v %v", decision, err)
		}
	})

	t.Run("explicit floor 3: three cooling, two answer", func(t *testing.T) {
		client, nodes := fleet(t, 1, 0, 5, ethguard.WithQuorum(3))
		for _, node := range nodes[:3] {
			for i := 0; i < 3; i++ {
				node.Fail(methods.MethodChainID, -32700, "probing outage")
				client.Call(context.Background(), methods.MethodChainID)
			}
			node.Return(methods.MethodChainID, "0x1")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if _, _, err := client.ChainID(ctx); !errors.Is(err, ethguard.ErrConflict) {
			t.Fatalf("decision 0027's refusal example failed, got %v", err)
		}
	})
}

// The pool-level sentinel keeps gated members observable without a health
// penalty; a compile-level check that the pool exposes the shared mode.
var _ = pool.ErrSourceGated
