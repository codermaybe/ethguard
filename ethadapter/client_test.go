package ethadapter_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"ethguard"
	"ethguard/config"
	"ethguard/ethadapter"
	"ethguard/rpc/methods"
	"ethguard/rpctest"
)

// fleet spins up count scripted nodes behind one adapter client.
func fleet(t *testing.T, count int, opts ...ethguard.Option) (*ethadapter.Client, []*rpctest.Node) {
	t.Helper()
	nodes := make([]*rpctest.Node, 0, count)
	cfg := config.Config{ChainID: 1}
	for i := range count {
		node := rpctest.NewNode(1)
		t.Cleanup(node.Close)
		nodes = append(nodes, node)
		cfg.Upstreams = append(cfg.Upstreams, config.Upstream{
			ID:       fmt.Sprintf("node-%c", 'a'+i),
			Endpoint: node.URL(),
		})
	}
	client, err := ethadapter.New(cfg, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client, nodes
}

// fixtureBlock marshals a self-consistent block at height number: the wire
// hash equals the header's computed hash, so DecodeBlock's integrity check
// passes against honestly-assembled payloads.
func fixtureBlock(t *testing.T, number int64) json.RawMessage {
	t.Helper()
	head := &types.Header{
		ParentHash:  common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001"),
		UncleHash:   types.EmptyUncleHash,
		Root:        common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000002"),
		TxHash:      types.EmptyTxsHash,
		ReceiptHash: common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000003"),
		Difficulty:  new(big.Int), // post-merge zero; must survive the round-trip
		Number:      big.NewInt(number),
		GasLimit:    30_000_000,
		Time:        5,
	}
	block := types.NewBlockWithHeader(head)
	raw, err := head.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	obj["hash"] = json.RawMessage(fmt.Sprintf("%q", block.Hash().Hex()))
	obj["transactions"] = json.RawMessage(`[]`)
	out, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAdapterChainIDIsVerified(t *testing.T) {
	client, _ := fleet(t, 3)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	id, err := client.ChainID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if id.Int64() != 1 {
		t.Fatalf("expected chain id 1, got %v", id)
	}
}

func TestAdapterBalanceAtPinsMovingTag(t *testing.T) {
	client, nodes := fleet(t, 3)
	const pinned = "0xabc"
	for i, node := range nodes {
		node.Return("eth_getBlockByNumber", map[string]any{"number": "0x10", "hash": pinned})
		node.Handle("eth_getBalance", func(c rpctest.Call) (any, error) {
			var tag string
			if len(c.Params) == 2 && json.Unmarshal(c.Params[1], &tag) == nil && tag == pinned {
				return "0x64", nil
			}
			return fmt.Sprintf("0x%x", 100+i), nil // divergence a moving tag would expose
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	balance, err := client.BalanceAt(ctx, common.HexToAddress("0xdad"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if balance.Int64() != 100 {
		t.Fatalf("expected pinned balance 100, got %v", balance)
	}
}

func TestAdapterBlockByNumberNeverFetchesAMovingTag(t *testing.T) {
	client, nodes := fleet(t, 3)
	pinned := fixtureBlock(t, 16)
	for _, node := range nodes {
		// Every reference resolves to the same self-consistent block, so
		// anchor establishment and the pinned fetch agree end to end.
		node.Handle("eth_getBlockByNumber", func(c rpctest.Call) (any, error) {
			return json.RawMessage(pinned), nil
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	block, err := client.BlockByNumber(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if block.NumberU64() != 16 {
		t.Fatalf("expected pinned block 16, got %d", block.NumberU64())
	}

	// The full-transaction fetches must carry explicit references only; any
	// "latest" on the wire belongs to anchor establishment (fullTx=false).
	for i, node := range nodes {
		for _, call := range node.Calls() {
			if call.Method != methods.MethodGetBlockByNumber || len(call.Params) < 2 {
				continue
			}
			var fullTx bool
			json.Unmarshal(call.Params[1], &fullTx)
			var ref string
			json.Unmarshal(call.Params[0], &ref)
			if fullTx && (ref == "latest" || ref == "safe" || ref == "finalized") {
				t.Fatalf("node %d fetched a moving tag with full bodies: %q", i, ref)
			}
		}
	}
}

func TestAdapterBlockNumberMatchesVerifiedReference(t *testing.T) {
	client, nodes := fleet(t, 3, ethguard.WithFinalizedAnchoring())
	fin := fixtureBlock(t, 42)
	pin := blockHashOf(fin)
	for _, node := range nodes {
		node.Handle("eth_getBlockByNumber", func(c rpctest.Call) (any, error) {
			var tag string
			json.Unmarshal(c.Params[0], &tag)
			if tag == "finalized" || strings.EqualFold(tag, pin) {
				return json.RawMessage(fin), nil
			}
			return map[string]any{"number": "0x99", "hash": "0xtip"}, nil // divergent tips
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	height, err := client.BlockNumber(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if height != 42 {
		t.Fatalf("expected verified finalized reference height 42, got %d", height)
	}
}

func TestAdapterSurfacesMalformedClass(t *testing.T) {
	client, nodes := fleet(t, 2)
	// Passes head normalization (number and hash present) but cannot become
	// a block: the failure must surface as the typed-boundary class.
	for _, node := range nodes {
		node.Return("eth_getBlockByNumber", map[string]any{"number": "0x10", "hash": "0xabc"})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := client.BlockByNumber(ctx, big.NewInt(16)); !errors.Is(err, ethguard.ErrMalformedResponse) {
		t.Fatalf("expected ErrMalformedResponse, got %v", err)
	}
}

func TestAdapterSuggestGasPriceTakesMedian(t *testing.T) {
	client, nodes := fleet(t, 3)
	nodes[0].Return("eth_gasPrice", "0x3b9aca00") // 1 gwei
	nodes[1].Return("eth_gasPrice", "0x3b9aca00")
	nodes[2].Return("eth_gasPrice", "0x77359400") // 2 gwei outlier
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	price, err := client.SuggestGasPrice(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if price.Int64() != 1_000_000_000 {
		t.Fatalf("expected median 1 gwei, got %v", price)
	}
}

func TestAdapterLocalObservations(t *testing.T) {
	client, nodes := fleet(t, 3)
	for i, node := range nodes {
		node.Return("net_peerCount", "0xa")
		node.Return("eth_syncing", false)
		_ = i
	}
	nodes[2].Return("eth_syncing", map[string]any{
		"startingBlock": "0x0", "currentBlock": "0x32", "highestBlock": "0x64",
	}) // a later source's state must not leak into the consulted answer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	peers, err := client.PeerCount(ctx)
	if err != nil || peers != 10 {
		t.Fatalf("expected peer count 10, got %d %v", peers, err)
	}
	progress, err := client.SyncProgress(ctx)
	if err != nil || progress != nil {
		t.Fatalf("expected nil progress from the first source, got %+v %v", progress, err)
	}
}

// blockHashOf extracts the hash field of a fixture payload.
func blockHashOf(raw json.RawMessage) string {
	var obj struct {
		Hash string `json:"hash"`
	}
	json.Unmarshal(raw, &obj)
	return obj.Hash
}
