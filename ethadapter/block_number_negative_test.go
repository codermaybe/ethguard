package ethadapter_test

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"ethguard"
	"ethguard/rpc/methods"
	"ethguard/rpctest"
)

// TestBlockByNumberRejectsPendingLocally locks the typed -1 contract:
// pending is not projected into *types.Block, not rewritten to a mined tag,
// and not classified as a corroboration failure.
func TestBlockByNumberRejectsPendingLocally(t *testing.T) {
	client, nodes := fleet(t, 3)
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockByNumber, fixtureBlock(t, 16))
		node.Return(methods.MethodGetBlockByHash, fixtureBlock(t, 16))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	block, err := client.BlockByNumber(ctx, big.NewInt(-1))
	if block != nil {
		t.Fatalf("pending block was projected: height %d", block.NumberU64())
	}
	if !errors.Is(err, ethguard.ErrInvalidRequest) {
		t.Fatalf("BlockByNumber(-1): got %v, want ErrInvalidRequest", err)
	}
	if !strings.Contains(err.Error(), "pending block typed projection") {
		t.Fatalf("BlockByNumber(-1): error %v must name the missing typed projection", err)
	}
	if errors.Is(err, ethguard.ErrConflict) || errors.Is(err, ethguard.ErrNoValidResponse) || errors.Is(err, ethguard.ErrMalformedResponse) {
		t.Fatalf("pending rejection swallowed by corroboration: %v", err)
	}
	again, errAgain := client.BlockByNumber(ctx, big.NewInt(-1))
	if again != nil || errAgain == nil || errAgain.Error() != err.Error() {
		t.Fatalf("pending rejection unstable: %v / %v", err, errAgain)
	}
}

// TestBlockByNumberPendingSendsNoBusinessRequest proves -1 never reaches a
// provider, including as an illegal negative quantity such as -0x1.
func TestBlockByNumberPendingSendsNoBusinessRequest(t *testing.T) {
	client, nodes := fleet(t, 3)
	for _, node := range nodes {
		node.Handle(methods.MethodGetBlockByNumber, func(c rpctest.Call) (any, error) {
			t.Errorf("business or anchor request escaped: %s", c.Params)
			return fixtureBlock(t, 16), nil
		})
		node.Handle(methods.MethodGetBlockByHash, func(c rpctest.Call) (any, error) {
			t.Errorf("hash fetch escaped a pending rejection: %s", c.Params)
			return fixtureBlock(t, 16), nil
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := client.BlockByNumber(ctx, big.NewInt(-1)); !errors.Is(err, ethguard.ErrInvalidRequest) {
		t.Fatalf("BlockByNumber(-1): got %v, want ErrInvalidRequest", err)
	}
	for _, node := range nodes {
		for _, call := range node.Calls() {
			if illegalNegativeQuantity(call.Params) {
				t.Fatalf("illegal negative wire param on %s: %s", call.Method, call.Params)
			}
			t.Fatalf("BlockByNumber(-1) contacted %s", call.Method)
		}
	}
}

// TestBlockByNumberPreservesKnownNegativeTagMeaning keeps -2/-3/-4/-5 and
// unknown negatives from collapsing into pending or a shared anchor.
func TestBlockByNumberPreservesKnownNegativeTagMeaning(t *testing.T) {
	client, nodes := fleet(t, 3)
	latestFixture := fixtureBlock(t, 16)
	safeFixture := fixtureBlock(t, 17)
	finalizedFixture := fixtureBlock(t, 18)
	earliestFixture := fixtureBlock(t, 0)
	byHash := map[string]json.RawMessage{
		blockHashOf(latestFixture):    json.RawMessage(latestFixture),
		blockHashOf(safeFixture):      json.RawMessage(safeFixture),
		blockHashOf(finalizedFixture): json.RawMessage(finalizedFixture),
	}
	for _, node := range nodes {
		node.Handle(methods.MethodGetBlockByNumber, func(c rpctest.Call) (any, error) {
			if illegalNegativeQuantity(c.Params) {
				t.Errorf("illegal negative wire param: %s", c.Params)
			}
			var ref string
			if len(c.Params) > 0 {
				_ = json.Unmarshal(c.Params[0], &ref)
			}
			var fullTx bool
			if len(c.Params) > 1 {
				_ = json.Unmarshal(c.Params[1], &fullTx)
			}
			if fullTx {
				if ref == "0x0" {
					return json.RawMessage(earliestFixture), nil
				}
				t.Errorf("business call fetched moving tag %q", ref)
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
				return nil, rpctest.RPCError(-32602, "unexpected tag "+ref)
			}
		})
		node.Handle(methods.MethodGetBlockByHash, func(c rpctest.Call) (any, error) {
			var hash string
			if len(c.Params) > 0 {
				_ = json.Unmarshal(c.Params[0], &hash)
			}
			if fixture, ok := byHash[hash]; ok {
				return json.RawMessage(fixture), nil
			}
			return nil, rpctest.RPCError(-32602, "unknown hash "+hash)
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for num, want := range map[int64]uint64{-2: 16, -4: 17, -3: 18, -5: 0} {
		block, err := client.BlockByNumber(ctx, big.NewInt(num))
		if err != nil {
			t.Fatalf("BlockByNumber(%d): %v", num, err)
		}
		if block.NumberU64() != want {
			t.Fatalf("BlockByNumber(%d) = height %d, want %d", num, block.NumberU64(), want)
		}
	}
	seenTags := map[string]bool{}
	sawEarliest := false
	for _, node := range nodes {
		for _, call := range node.Calls() {
			if illegalNegativeQuantity(call.Params) {
				t.Fatalf("illegal negative wire param on %s: %s", call.Method, call.Params)
			}
			if call.Method != methods.MethodGetBlockByNumber || len(call.Params) < 2 {
				continue
			}
			var fullTx bool
			_ = json.Unmarshal(call.Params[1], &fullTx)
			ref := wireString(t, call.Params[0])
			if ref == "pending" {
				t.Fatal("known negative tag was rewritten to pending")
			}
			if fullTx {
				if ref == "0x0" {
					sawEarliest = true
				}
				continue
			}
			seenTags[ref] = true
		}
	}
	for _, tag := range []string{"latest", "safe", "finalized"} {
		if !seenTags[tag] {
			t.Fatalf("no anchor establishment for %q", tag)
		}
	}
	if !sawEarliest {
		t.Fatal("earliest (-5) was not fetched as explicit height 0x0")
	}

	before := map[string]int{}
	for _, node := range nodes {
		before[node.URL()] = len(node.Calls())
	}
	for _, n := range []*big.Int{big.NewInt(-6), new(big.Int).Neg(new(big.Int).Lsh(big.NewInt(1), 100))} {
		if _, err := client.BlockByNumber(ctx, n); !errors.Is(err, ethguard.ErrInvalidRequest) {
			t.Fatalf("BlockByNumber(%v): got %v, want ErrInvalidRequest", n, err)
		}
	}
	for _, node := range nodes {
		if len(node.Calls()) != before[node.URL()] {
			t.Fatalf("unknown negative contacted upstream: %+v", node.Calls()[before[node.URL()]:])
		}
	}
}

func illegalNegativeQuantity(params []json.RawMessage) bool {
	for _, param := range params {
		var text string
		if json.Unmarshal(param, &text) == nil && (strings.HasPrefix(text, "-0x") || strings.HasPrefix(text, "-0X") || text == "-1") {
			return true
		}
	}
	return false
}
