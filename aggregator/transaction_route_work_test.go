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

	"github.com/codermaybe/ethguard/internal/spec"
	"github.com/codermaybe/ethguard/internal/spec/conformance"
	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/codermaybe/ethguard/rpctest"
)

// The route-work witness: what one invocation of the compiled transaction
// plans really spends on full-representation work per route, counted at the
// compiled plan's own hooks — the admission entry, the resolver's canonical
// encoder, the normalizer — plus the processor entries on the canonical
// route. Each admission or encoder entry runs the object's full preparation
// (the JSON-safety gate, the whole-key corrections, and the object encode),
// so an entry count is a full-representation preparation count. These are
// hook-entry counts, not JSON parse counts: sub-steps inside one preparation
// unmarshal and marshal several times, and none of them is measured here.

type workCounters struct {
	admit     atomic.Int64
	encode    atomic.Int64
	normalize atomic.Int64
	process   atomic.Int64
}

// instrumentedPlan compiles the registered transaction spec with counting
// wrappers over its own hook functions — same functions, same composition,
// same compiler — so the counts are the registered plan's real work.
func instrumentedPlan(t *testing.T, method string, counts *workCounters) rpc.ReadPlan {
	t.Helper()
	spec, ok := methods.SpecFor(method)
	if !ok {
		t.Fatalf("%s is not a registered method", method)
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
		spec.ProcessCanonical = func(ctx context.Context, req rpc.Request, value json.RawMessage, caller rpc.AdmissionCaller) (rpc.CanonicalResult, error) {
			counts.process.Add(1)
			return original(ctx, req, value, caller)
		}
	}
	plan, err := methods.CompilePlan(spec)
	if err != nil {
		t.Fatalf("instrumented %s plan does not compile: %v", method, err)
	}
	return plan
}

// minedTransactionVector loads the first official mined by-hash vector — a
// successful result with a real inclusion context — and returns its bytes
// with the declared identity members unquoted, the shape a real request
// carries.
func minedTransactionVector(t *testing.T) (json.RawMessage, string, string, string) {
	t.Helper()
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
		t.Fatal(err)
	}
	if hit.raw == nil {
		t.Fatal("no official mined transaction vector found")
	}
	return hit.raw, hit.hash, hit.block, hit.index
}

// transactionWorkFleet installs one plan over three scripted HTTP sources,
// all answering the method with the same official body.
func transactionWorkFleet(t *testing.T, method string, plan rpc.ReadPlan, body json.RawMessage) *Aggregator {
	t.Helper()
	var members []pool.Member
	for i := range 3 {
		node := rpctest.NewNode(0)
		node.ReturnRaw(method, body)
		t.Cleanup(node.Close)
		members = append(members, pool.Member{Name: fmt.Sprintf("work-%d", i), URL: node.URL()})
	}
	p := fixedPoolWith(t, 0, []pool.PoolOption{pool.WithDialer(nativehttp.NewDialer())}, members...)
	shared, err := rpc.NewCorroborationShared(rpc.CorroborationMajority, 2)
	if err != nil {
		t.Fatal(err)
	}
	return New(p, WithPlans(map[string]rpc.ReadPlan{method: plan}), WithCorroboration(shared))
}

// TestTransactionRouteWorkCounts pins the per-route work shape of the two
// pilot methods with counting witnesses. On the canonical route one
// invocation runs the processor exactly once per source and never touches
// the raw hooks; on the raw route one invocation runs the admission entry
// and the resolver's canonical encoder exactly once per usable source — the
// two full-representation preparations — and never re-normalizes, because
// the comparison key is the canonical bytes. A second identical invocation
// doubles every count: nothing survives an invocation.
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
