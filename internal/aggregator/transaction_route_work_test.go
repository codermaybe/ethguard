package aggregator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/codermaybe/ethguard/internal/rpcengine"
	"github.com/codermaybe/ethguard/internal/spec"
	"github.com/codermaybe/ethguard/internal/spec/conformance"

	"github.com/codermaybe/ethguard/internal/methods"
	"github.com/codermaybe/ethguard/internal/pool"
	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpctest"
)

// The route-work witness: what one invocation of the compiled transaction
// plans really spends on full-representation work per route, counted at the
// compiled plan's own hooks — the admission entry, the resolver's canonical
// encoder, the normalizer — plus the processor entries on the canonical
// route. These counters are hook-entry counts. They are not object
// preparation counts by themselves, and they are not JSON parse counts.
//
// The three numbers are measured separately: hook entries here; full
// transaction.Prepare calls and transaction-package json.Unmarshal calls
// with the object-kernel overlay witness recorded in
// docs/design/tmp/codeanalyze-rpc-quality-plan.md. Per usable source that
// witness measured, through the real compiled plan and production assembler:
// canonical route 1 Prepare and 6 unmarshals; raw route 2 Prepares and 9
// unmarshals (admission's Prepare plus the resolver encoder's Prepare; each
// Prepare does 3 unmarshals — the field view, the official projection, the
// typed merge — and each binding does 3 more). The witness excludes the
// wire and jsonsafe packages, so the unmarshal figures are a lower bound of
// the JSON work, not a parse total. Before the admission entries bound
// through the prepared candidate they prepared the canonical bytes a second
// time: the same witness at f12eefe measured 3 Prepares and 13 unmarshals
// per source on the raw route.

type workCounters struct {
	admit     atomic.Int64
	encode    atomic.Int64
	normalize atomic.Int64
	process   atomic.Int64
}

// instrumentedPlan compiles the registered transaction spec with counting
// wrappers over its own hook functions — same functions, same composition,
// same compiler — so the counts are the registered plan's real work.
func instrumentedPlan(tb testing.TB, method string, counts *workCounters) rpcengine.ReadPlan {
	tb.Helper()
	spec, ok := methods.SpecFor(method)
	if !ok {
		tb.Fatalf("%s is not a registered method", method)
	}
	if spec.Admit != nil {
		original := spec.Admit
		spec.Admit = func(ctx context.Context, req rpc.Request, value json.RawMessage, caller rpc.AdmissionCaller) (json.RawMessage, error) {
			counts.admit.Add(1)
			return original(ctx, req, value, caller)
		}
	}
	if spec.Encode != nil {
		original := spec.Encode
		spec.Encode = func(value json.RawMessage) (json.RawMessage, error) {
			counts.encode.Add(1)
			return original(value)
		}
	}
	if spec.Normalize != nil {
		original := spec.Normalize
		spec.Normalize = func(value json.RawMessage) (string, error) {
			counts.normalize.Add(1)
			return original(value)
		}
	}
	if spec.ProcessCanonical != nil {
		original := spec.ProcessCanonical
		spec.ProcessCanonical = func(ctx context.Context, req rpc.Request, value json.RawMessage, caller rpc.AdmissionCaller) (rpcengine.CanonicalResult, error) {
			counts.process.Add(1)
			return original(ctx, req, value, caller)
		}
	}
	plan, err := methods.CompilePlan(spec)
	if err != nil {
		tb.Fatalf("instrumented %s plan does not compile: %v", method, err)
	}
	return plan
}

// minedTransactionVector loads the first official mined by-hash vector — a
// successful result with a real inclusion context — and returns its bytes
// with the declared identity members unquoted, the shape a real request
// carries.
func minedTransactionVector(tb testing.TB) (json.RawMessage, string, string, string) {
	tb.Helper()
	type found struct {
		raw   json.RawMessage
		hash  string
		block string
		index string
	}
	var hit found
	err := fs.WalkDir(spec.VectorsFS(), "vectors/eth_getTransactionByHash", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".io") {
			return nil
		}
		vectors, err := conformance.ParseVector(spec.VectorsFS(), path)
		if err != nil {
			return err
		}
		for _, vector := range vectors {
			if len(vector.Error) > 0 || bytes.Equal(bytes.TrimSpace(vector.Result), []byte("null")) {
				continue
			}
			var context struct {
				Hash             string  `json:"hash"`
				BlockHash        *string `json:"blockHash"`
				TransactionIndex string  `json:"transactionIndex"`
			}
			if err := json.Unmarshal(vector.Result, &context); err != nil {
				return err
			}
			if context.BlockHash == nil || !strings.HasPrefix(context.TransactionIndex, "0x") {
				continue // pending shape: no block binding to witness
			}
			hit = found{raw: vector.Result, hash: context.Hash, block: *context.BlockHash, index: context.TransactionIndex}
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		tb.Fatal(err)
	}
	if hit.raw == nil {
		tb.Fatal("no official mined transaction vector found")
	}
	return hit.raw, hit.hash, hit.block, hit.index
}

// transactionWorkFleet installs one plan over three scripted HTTP sources,
// all answering the method with the same official body.
func transactionWorkFleet(tb testing.TB, method string, plan rpcengine.ReadPlan, body json.RawMessage) *Aggregator {
	tb.Helper()
	var members []pool.Member
	for i := range 3 {
		node := rpctest.NewNode(0)
		node.ReturnRaw(method, body)
		tb.Cleanup(node.Close)
		members = append(members, pool.Member{Name: fmt.Sprintf("work-%d", i), URL: node.URL()})
	}
	p := fixedPoolWith(tb, 0, []pool.PoolOption{pool.WithDialer(nativehttp.NewDialer())}, members...)
	shared, err := rpcengine.NewCorroborationShared(rpc.CorroborationMajority, 2)
	if err != nil {
		tb.Fatal(err)
	}
	return New(p, WithPlans(map[string]rpcengine.ReadPlan{method: plan}), WithCorroboration(shared))
}

// forwardedFleet installs one plan over one scripted source under the
// Forwarded corroboration mode: one bounded single-source attempt, no
// agreement claimed.
func forwardedFleet(tb testing.TB, method string, plan rpcengine.ReadPlan, body json.RawMessage) *Aggregator {
	tb.Helper()
	node := rpctest.NewNode(0)
	node.ReturnRaw(method, body)
	tb.Cleanup(node.Close)
	p := fixedPoolWith(tb, 0, []pool.PoolOption{pool.WithDialer(nativehttp.NewDialer())},
		pool.Member{Name: "forwarded-0", URL: node.URL()})
	shared, err := rpcengine.NewCorroborationShared(rpc.CorroborationForwarded, 0)
	if err != nil {
		tb.Fatal(err)
	}
	return New(p, WithPlans(map[string]rpcengine.ReadPlan{method: plan}), WithCorroboration(shared))
}

// TestTransactionRouteWorkCounts pins the per-route work shape of the two
// pilot methods with counting witnesses. On the canonical route one
// invocation runs the processor exactly once per source and never touches
// the raw hooks; on the raw route one invocation runs the admission entry
// and the resolver's canonical encoder exactly once per usable source, each
// running the object kernel once — two full preparations, measured at the
// kernel entry by the overlay witness in the plan document, not inferred
// from these hook counts — and never re-normalizes, because the comparison
// key is the canonical bytes. A second identical invocation doubles every
// count: nothing survives an invocation.
func TestTransactionRouteWorkCounts(t *testing.T) {
	raw, hash, blockHash, index := minedTransactionVector(t)
	for _, method := range []string{methods.MethodGetTransactionByHash, methods.MethodGetTransactionByBlockHashAndIndex} {
		request := rpc.Request{Method: method, Params: []any{hash}}
		if method == methods.MethodGetTransactionByBlockHashAndIndex {
			request = rpc.Request{Method: method, Params: []any{blockHash, index}}
		}
		t.Run(method, func(t *testing.T) {
			t.Run("canonical", func(t *testing.T) {
				counts := &workCounters{}
				plan := instrumentedPlan(t, method, counts)
				agg := transactionWorkFleet(t, method, plan, raw)
				for round := 1; round <= 2; round++ {
					decision, err := agg.Aggregate(context.Background(), request)
					if err != nil || decision.Status != rpc.Corroborated || len(decision.Support) != 3 {
						t.Fatalf("round %d = %+v, %v", round, decision, err)
					}
				}
				if counts.process.Load() != 6 {
					t.Fatalf("canonical route ran the processor %d times, want 3 per invocation × 2 rounds", counts.process.Load())
				}
				if counts.admit.Load() != 0 || counts.encode.Load() != 0 || counts.normalize.Load() != 0 {
					t.Fatalf("canonical route re-ran raw hooks: admit=%d encode=%d normalize=%d", counts.admit.Load(), counts.encode.Load(), counts.normalize.Load())
				}
			})
			t.Run("raw", func(t *testing.T) {
				counts := &workCounters{}
				plan := instrumentedPlan(t, method, counts)
				plan.ProcessCanonical = nil
				agg := transactionWorkFleet(t, method, plan, raw)
				for round := 1; round <= 2; round++ {
					decision, err := agg.Aggregate(context.Background(), request)
					if err != nil || decision.Status != rpc.Corroborated || len(decision.Support) != 3 {
						t.Fatalf("round %d = %+v, %v", round, decision, err)
					}
				}
				if counts.admit.Load() != 6 || counts.encode.Load() != 6 {
					t.Fatalf("raw route work = admit %d / encode %d, want 3 per invocation × 2 rounds each", counts.admit.Load(), counts.encode.Load())
				}
				if counts.normalize.Load() != 0 {
					t.Fatalf("raw route re-normalized beside the canonical-bytes key: %d calls", counts.normalize.Load())
				}
				if counts.process.Load() != 0 {
					t.Fatalf("raw route ran the processor %d times", counts.process.Load())
				}
			})
		})
	}
}
