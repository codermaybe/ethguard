package ethadapter_test

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"

	"ethguard"
	"ethguard/rpc"
	"ethguard/rpc/methods"
	"ethguard/rpctest"
)

const (
	wireHashLatest    = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	wireHashSafe      = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	wireHashFinalized = "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	wireHashExplicit  = "0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
)

func tagHead(tag string) map[string]any {
	switch tag {
	case "safe":
		return map[string]any{"number": "0x20", "hash": wireHashSafe}
	case "finalized":
		return map[string]any{"number": "0x18", "hash": wireHashFinalized}
	default:
		return map[string]any{"number": "0x10", "hash": wireHashLatest}
	}
}

func businessCalls(calls []rpctest.Call, method string) []rpctest.Call {
	var out []rpctest.Call
	for _, c := range calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func anchorCalls(calls []rpctest.Call) []rpctest.Call {
	return businessCalls(calls, methods.MethodGetBlockByNumber)
}

func callMsgTo(addr common.Address) ethereum.CallMsg {
	return ethereum.CallMsg{To: &addr}
}

// wireString decodes one JSON string param.
func wireString(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("param is not a JSON string: %s", raw)
	}
	return s
}

// wireObject decodes one JSON object param.
func wireObject(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("param is not a JSON object: %s", raw)
	}
	return obj
}

func TestStateRefMovingTagsRewriteToAnchorHash(t *testing.T) {
	client, nodes := fleet(t, 3)
	for _, node := range nodes {
		node.Handle(methods.MethodGetBlockByNumber, func(c rpctest.Call) (any, error) {
			return tagHead(wireString(t, c.Params[0])), nil
		})
		node.Return(methods.MethodGetBalance, "0x64")
		node.Return(methods.MethodGetTransactionCount, "0x1")
		node.Return(methods.MethodGetStorageAt, "0x"+strings.Repeat("00", 32))
		node.Return(methods.MethodGetCode, "0x1234")
		node.Handle(methods.MethodCall, func(c rpctest.Call) (any, error) { return "0x1234", nil })
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	addr := common.HexToAddress("0xdad")

	if _, err := client.BalanceAt(ctx, addr, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := client.NonceAt(ctx, addr, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := client.StorageAt(ctx, addr, common.Hash{}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CodeAt(ctx, addr, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CallContract(ctx, callMsgTo(addr), nil); err != nil {
		t.Fatal(err)
	}

	expect := map[string]struct {
		pos int
	}{
		methods.MethodGetBalance:          {1},
		methods.MethodGetTransactionCount: {1},
		methods.MethodGetStorageAt:        {2},
		methods.MethodGetCode:             {1},
		methods.MethodCall:                {1},
	}
	for _, node := range nodes {
		calls := node.Calls()
		if len(anchorCalls(calls)) == 0 {
			t.Fatal("no anchor establishment observed")
		}
		for method, e := range expect {
			bus := businessCalls(calls, method)
			if len(bus) != 1 {
				t.Fatalf("%s calls = %d, want 1", method, len(bus))
			}
			if got := wireString(t, bus[0].Params[e.pos]); got != wireHashLatest {
				t.Fatalf("%s ref = %q, want anchor hash %q", method, got, wireHashLatest)
			}
			// Anchor establishment precedes the business read on every node.
			firstAnchor, firstBus := -1, -1
			for i, c := range calls {
				if c.Method == methods.MethodGetBlockByNumber && firstAnchor < 0 {
					firstAnchor = i
				}
				if c.Method == method && firstBus < 0 {
					firstBus = i
				}
			}
			if firstAnchor < 0 || firstAnchor > firstBus {
				t.Fatalf("%s: anchor not established before business read", method)
			}
		}
	}
}

// TestStateRefSafeFinalizedKeepTheirLevel guards the downgrade the matrix
// forbids: safe/finalized must anchor at their own reference, never latest.
func TestStateRefSafeFinalizedKeepTheirLevel(t *testing.T) {
	client, nodes := fleet(t, 3)
	for _, node := range nodes {
		node.Handle(methods.MethodGetBlockByNumber, func(c rpctest.Call) (any, error) {
			return tagHead(wireString(t, c.Params[0])), nil
		})
		node.Return(methods.MethodGetBalance, "0x64")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	addr := common.HexToAddress("0xdad")

	if _, err := client.BalanceAt(ctx, addr, big.NewInt(-4)); err != nil { // safe
		t.Fatal(err)
	}
	if _, err := client.BalanceAt(ctx, addr, big.NewInt(-3)); err != nil { // finalized
		t.Fatal(err)
	}
	var safeAnchors, finAnchors int
	for _, node := range nodes {
		for _, c := range node.Calls() {
			if c.Method != methods.MethodGetBlockByNumber {
				continue
			}
			var tag, fullTx any
			json.Unmarshal(c.Params[0], &tag)
			if len(c.Params) > 1 {
				json.Unmarshal(c.Params[1], &fullTx)
			}
			switch tag {
			case "safe":
				safeAnchors++
			case "finalized":
				finAnchors++
			case "latest":
				t.Fatalf("unexpected latest anchor; safe/finalized must keep their level")
			}
		}
		bus := businessCalls(node.Calls(), methods.MethodGetBalance)
		if len(bus) != 2 {
			t.Fatalf("balance calls = %d, want 2", len(bus))
		}
		refs := map[string]bool{}
		for _, b := range bus {
			refs[wireString(t, b.Params[1])] = true
		}
		if !refs[wireHashSafe] || !refs[wireHashFinalized] {
			t.Fatalf("business refs = %v, want safe and finalized pins", refs)
		}
		if refs[wireHashLatest] {
			t.Fatalf("business read pinned to latest; safe/finalized were downgraded")
		}
	}
	if safeAnchors == 0 || finAnchors == 0 {
		t.Fatalf("safe anchors = %d, finalized anchors = %d", safeAnchors, finAnchors)
	}
}

// TestStateRefEIP1898ObjectPreservedOnWire pins the standard form: a caller
// object reaches providers byte-equivalent, with no anchor rewrite.
func TestStateRefEIP1898ObjectPreservedOnWire(t *testing.T) {
	client, nodes := fleet(t, 3)
	for _, node := range nodes {
		node.Return(methods.MethodGetBalance, "0x64")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	eg := client.Unwrap()
	addr := common.HexToAddress("0xdad").Hex()

	for _, obj := range []map[string]any{
		{"blockHash": wireHashExplicit},
		{"blockHash": wireHashExplicit, "requireCanonical": true},
		{"blockNumber": "0x10", "requireCanonical": true},
		{"blockNumber": "0x10"},
	} {
		decision, err := eg.Call(ctx, methods.MethodGetBalance, addr, obj)
		if err != nil {
			t.Fatalf("%v: %v", obj, err)
		}
		if decision.Status != rpc.Corroborated {
			t.Fatalf("%v: status = %v, want corroborated", obj, decision.Status)
		}
	}
	for _, node := range nodes {
		calls := node.Calls()
		if len(anchorCalls(calls)) != 0 {
			t.Fatalf("explicit object triggered anchor establishment: %+v", calls)
		}
		bus := businessCalls(calls, methods.MethodGetBalance)
		if len(bus) != 4 {
			t.Fatalf("balance calls = %d, want 4", len(bus))
		}
		for i, want := range []map[string]any{
			{"blockHash": wireHashExplicit},
			{"blockHash": wireHashExplicit, "requireCanonical": true},
			{"blockNumber": "0x10", "requireCanonical": true},
			{"blockNumber": "0x10"},
		} {
			got := wireObject(t, bus[i].Params[1])
			if len(got) != len(want) {
				t.Fatalf("call %d params = %v, want %v", i, got, want)
			}
			for k, v := range want {
				if got[k] != v {
					t.Fatalf("call %d params = %v, want %v (requireCanonical must survive)", i, got, want)
				}
			}
		}
	}
}

// TestStateRefObjectTagRewritesToHashObject pins the object-form anchor path:
// {blockNumber: tag} becomes {blockHash: anchor} — an object, never a bare
// string — with requireCanonical preserved across the rewrite.
func TestStateRefObjectTagRewritesToHashObject(t *testing.T) {
	client, nodes := fleet(t, 3)
	for _, node := range nodes {
		node.Handle(methods.MethodGetBlockByNumber, func(c rpctest.Call) (any, error) {
			return tagHead(wireString(t, c.Params[0])), nil
		})
		node.Return(methods.MethodGetBalance, "0x64")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	eg := client.Unwrap()
	addr := common.HexToAddress("0xdad").Hex()

	for _, obj := range []map[string]any{
		{"blockNumber": "finalized"},
		{"blockNumber": "safe", "requireCanonical": true},
	} {
		if _, err := eg.Call(ctx, methods.MethodGetBalance, addr, obj); err != nil {
			t.Fatalf("%v: %v", obj, err)
		}
	}
	for _, node := range nodes {
		bus := businessCalls(node.Calls(), methods.MethodGetBalance)
		if len(bus) != 2 {
			t.Fatalf("balance calls = %d, want 2", len(bus))
		}
		got := wireObject(t, bus[0].Params[1])
		if got["blockHash"] != wireHashFinalized {
			t.Fatalf("finalized object rewrote to %v, want blockHash %s", got, wireHashFinalized)
		}
		if _, exists := got["blockNumber"]; exists {
			t.Fatalf("pinned object kept blockNumber: %v", got)
		}
		got = wireObject(t, bus[1].Params[1])
		if got["blockHash"] != wireHashSafe || got["requireCanonical"] != true {
			t.Fatalf("safe object rewrote to %v, want safe pin with requireCanonical", got)
		}
	}
}

// TestStateRefPendingPassesThroughUnrewritten pins the R1 deviation visibly:
// pending is never rewritten into a mined-state read (full 0035 local
// semantics remain deferred, see the matrix).
func TestStateRefPendingPassesThroughUnrewritten(t *testing.T) {
	client, nodes := fleet(t, 3)
	for _, node := range nodes {
		node.Return(methods.MethodGetBalance, "0x64")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := client.BalanceAt(ctx, common.HexToAddress("0xdad"), big.NewInt(-1)); err != nil {
		t.Fatal(err)
	}
	for _, node := range nodes {
		if len(anchorCalls(node.Calls())) != 0 {
			t.Fatal("pending triggered anchor establishment; it must never be rewritten")
		}
		bus := businessCalls(node.Calls(), methods.MethodGetBalance)
		if len(bus) != 1 {
			t.Fatalf("balance calls = %d, want 1", len(bus))
		}
		if got := wireString(t, bus[0].Params[1]); got != "pending" {
			t.Fatalf("pending on wire = %q, want \"pending\"", got)
		}
	}
}

// TestStateRefAnchorFailureSendsNoBusinessRequest: a failed establishment
// must fail the call before any business fan-out, with an Unknown decision.
func TestStateRefAnchorFailureSendsNoBusinessRequest(t *testing.T) {
	client, nodes := fleet(t, 3)
	for _, node := range nodes {
		node.Fail(methods.MethodGetBlockByNumber, -32000, "anchor down")
		node.Handle(methods.MethodGetBalance, func(c rpctest.Call) (any, error) {
			t.Error("business request sent despite anchor failure")
			return "0x64", nil
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	decision, err := client.Unwrap().Call(ctx, methods.MethodGetBalance, common.HexToAddress("0xdad").Hex(), "latest")
	if err == nil {
		t.Fatal("expected anchor failure, got nil")
	}
	if decision.Status != rpc.Unknown {
		t.Fatalf("failed decision status = %v, want unknown", decision.Status)
	}
	for _, node := range nodes {
		if len(businessCalls(node.Calls(), methods.MethodGetBalance)) != 0 {
			t.Fatal("business request escaped a failed anchor")
		}
	}
}

// TestStateRefContradictoryObjectRejectedLocally: a moving tag mixed with an
// explicit hash fails before any upstream contact.
func TestStateRefContradictoryObjectRejectedLocally(t *testing.T) {
	client, nodes := fleet(t, 3)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := client.Unwrap().Call(ctx, methods.MethodGetBalance,
		common.HexToAddress("0xdad").Hex(),
		map[string]any{"blockHash": wireHashExplicit, "blockNumber": "0x1"})
	if err == nil || !errors.Is(err, ethguard.ErrInvalidRequest) {
		t.Fatalf("got %v, want ErrInvalidRequest", err)
	}
	for _, node := range nodes {
		if len(node.Calls()) != 0 {
			t.Fatalf("rejected request contacted upstream: %+v", node.Calls())
		}
	}
}

// TestStateRefEarliestAndNumberPassThrough: immutable references never
// consult the anchor.
func TestStateRefEarliestAndNumberPassThrough(t *testing.T) {
	client, nodes := fleet(t, 3)
	for _, node := range nodes {
		node.Return(methods.MethodGetBalance, "0x64")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	addr := common.HexToAddress("0xdad")

	if _, err := client.BalanceAt(ctx, addr, big.NewInt(-5)); err != nil { // earliest → 0x0
		t.Fatal(err)
	}
	if _, err := client.BalanceAt(ctx, addr, big.NewInt(16)); err != nil {
		t.Fatal(err)
	}
	for _, node := range nodes {
		if len(anchorCalls(node.Calls())) != 0 {
			t.Fatal("explicit reference consulted the anchor")
		}
		bus := businessCalls(node.Calls(), methods.MethodGetBalance)
		if len(bus) != 2 {
			t.Fatalf("balance calls = %d, want 2", len(bus))
		}
		if got := wireString(t, bus[0].Params[1]); got != "0x0" {
			t.Fatalf("earliest on wire = %q, want \"0x0\"", got)
		}
		if got := wireString(t, bus[1].Params[1]); got != "0x10" {
			t.Fatalf("number on wire = %q, want \"0x10\"", got)
		}
	}
}

// TestFeeHistoryRejectsHashBeforeUpstream: a hash must fail locally instead
// of riding the BlockNumber position to providers.
func TestFeeHistoryRejectsHashBeforeUpstream(t *testing.T) {
	client, nodes := fleet(t, 3)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	hashAsNewest := "0x" + strings.Repeat("ab", 32)
	_, err := client.Unwrap().Call(ctx, methods.MethodFeeHistory, uint64(4), hashAsNewest, []float64{})
	if err == nil || !errors.Is(err, ethguard.ErrInvalidRequest) {
		t.Fatalf("hash in newestBlock: got %v, want ErrInvalidRequest", err)
	}
	for _, node := range nodes {
		if len(node.Calls()) != 0 {
			t.Fatalf("rejected feeHistory contacted upstream: %+v", node.Calls())
		}
	}
}

// TestAdapterBlockByNumberNegativeTagsAnchorFirst pins the D5 contract:
// -2/-3/-4 establish their own level's reference through the tag-aware
// anchor seam and fetch by hash. Levels are never downgraded (each tag pins
// a different block), no moving tag reaches a business call, and the anchor
// request carries the original tag.
func TestAdapterBlockByNumberNegativeTagsAnchorFirst(t *testing.T) {
	client, nodes := fleet(t, 3)
	latestFixture := fixtureBlock(t, 16)
	safeFixture := fixtureBlock(t, 17)
	finalizedFixture := fixtureBlock(t, 18)
	byHash := map[string]json.RawMessage{
		blockHashOf(latestFixture):    json.RawMessage(latestFixture),
		blockHashOf(safeFixture):      json.RawMessage(safeFixture),
		blockHashOf(finalizedFixture): json.RawMessage(finalizedFixture),
	}
	for _, node := range nodes {
		node.Handle(methods.MethodGetBlockByNumber, func(c rpctest.Call) (any, error) {
			var ref string
			json.Unmarshal(c.Params[0], &ref)
			var fullTx bool
			if len(c.Params) > 1 {
				json.Unmarshal(c.Params[1], &fullTx)
			}
			if fullTx {
				t.Errorf("business call fetched %q with bodies; anchored reads fetch by hash", ref)
				return json.RawMessage(latestFixture), nil
			}
			switch ref {
			case "latest":
				return map[string]any{"number": "0x10", "hash": blockHashOf(latestFixture)}, nil
			case "safe":
				return map[string]any{"number": "0x11", "hash": blockHashOf(safeFixture)}, nil
			case "finalized":
				return map[string]any{"number": "0x12", "hash": blockHashOf(finalizedFixture)}, nil
			default:
				return nil, rpctest.RPCError(-32602, "unexpected anchor tag "+ref)
			}
		})
		node.Handle(methods.MethodGetBlockByHash, func(c rpctest.Call) (any, error) {
			var hash string
			json.Unmarshal(c.Params[0], &hash)
			if fixture, ok := byHash[hash]; ok {
				return json.RawMessage(fixture), nil
			}
			return nil, rpctest.RPCError(-32602, "unknown hash "+hash)
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for num, want := range map[int64]uint64{-2: 16, -4: 17, -3: 18} {
		block, err := client.BlockByNumber(ctx, big.NewInt(num))
		if err != nil {
			t.Fatalf("BlockByNumber(%d): %v", num, err)
		}
		if block.NumberU64() != want {
			t.Fatalf("BlockByNumber(%d) = height %d, want %d (level downgraded?)", num, block.NumberU64(), want)
		}
	}
	seenTags := map[string]bool{}
	for _, node := range nodes {
		for _, c := range node.Calls() {
			if c.Method != methods.MethodGetBlockByNumber || len(c.Params) < 2 {
				continue
			}
			var fullTx bool
			json.Unmarshal(c.Params[1], &fullTx)
			if fullTx {
				var ref string
				json.Unmarshal(c.Params[0], &ref)
				t.Fatalf("business call carried %q; anchored reads fetch by hash", ref)
			}
			seenTags[wireString(t, c.Params[0])] = true
		}
		for _, c := range node.Calls() {
			if c.Method != methods.MethodGetBlockByHash || len(c.Params) < 1 {
				continue
			}
			hash := wireString(t, c.Params[0])
			if _, ok := byHash[hash]; !ok {
				t.Fatalf("byHash fetched non-pin %q", hash)
			}
		}
	}
	for _, tag := range []string{"latest", "safe", "finalized"} {
		if !seenTags[tag] {
			t.Fatalf("no anchor establishment for %q; tag level lost", tag)
		}
	}
}

// TestAdapterBlockByNumberRejectsUnknownNegativesLocally: unknown or
// out-of-range negatives fail before any upstream contact.
func TestAdapterBlockByNumberRejectsUnknownNegativesLocally(t *testing.T) {
	client, nodes := fleet(t, 3)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	huge := new(big.Int).Neg(new(big.Int).Lsh(big.NewInt(1), 100))
	for _, n := range []*big.Int{big.NewInt(-6), huge} {
		if _, err := client.BlockByNumber(ctx, n); !errors.Is(err, ethguard.ErrInvalidRequest) {
			t.Fatalf("BlockByNumber(%v): got %v, want ErrInvalidRequest", n, err)
		}
	}
	for _, node := range nodes {
		if len(node.Calls()) != 0 {
			t.Fatalf("rejected block number contacted upstream: %+v", node.Calls())
		}
	}
}

// TestAdapterBlockByNumberTagAnchorFailureSendsNoBusinessRequest: a failed
// tag-aware establishment fails the call before any business fan-out.
func TestAdapterBlockByNumberTagAnchorFailureSendsNoBusinessRequest(t *testing.T) {
	client, nodes := fleet(t, 3)
	for _, node := range nodes {
		node.Fail(methods.MethodGetBlockByNumber, -32000, "anchor down")
		node.Handle(methods.MethodGetBlockByHash, func(c rpctest.Call) (any, error) {
			t.Error("business request sent despite anchor failure")
			return json.RawMessage(fixtureBlock(t, 16)), nil
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := client.BlockByNumber(ctx, big.NewInt(-3)); err == nil {
		t.Fatal("expected anchor failure, got nil")
	}
	for _, node := range nodes {
		if len(businessCalls(node.Calls(), methods.MethodGetBlockByHash)) != 0 {
			t.Fatal("business request escaped a failed anchor")
		}
	}
}

// TestTypedDecodeFailureClearsSuccessStatus: a value that corroborates but
// cannot be shaped at the typed boundary returns Unknown, never Corroborated.
func TestTypedDecodeFailureClearsSuccessStatus(t *testing.T) {
	client, nodes := fleet(t, 3)
	for _, node := range nodes {
		node.Handle(methods.MethodGetBlockByNumber, func(c rpctest.Call) (any, error) {
			return tagHead("latest"), nil
		})
		node.Return(methods.MethodCall, "0x1234")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	eg := client.Unwrap()
	msg := map[string]any{"to": common.HexToAddress("0xdad").Hex()}

	decision, err := eg.Call(ctx, methods.MethodCall, msg, "latest")
	if err != nil {
		t.Fatalf("setup call failed: %v", err)
	}
	if decision.Status != rpc.Corroborated {
		t.Fatalf("setup status = %v, want corroborated", decision.Status)
	}
	out, bad, err := ethguard.CallInto[bool](ctx, eg, methods.MethodCall, msg, "latest")
	if err == nil || !errors.Is(err, ethguard.ErrMalformedResponse) {
		t.Fatalf("typed decode: got %v, %v; want ErrMalformedResponse", out, err)
	}
	if bad.Status != rpc.Unknown {
		t.Fatalf("failed typed decode status = %v, want unknown", bad.Status)
	}
}
