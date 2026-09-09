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

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	gethrpc "github.com/ethereum/go-ethereum/rpc"

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
	obj["uncles"] = json.RawMessage(`[]`)
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
		// anchor establishment and the pinned fetch agree end to end. The
		// hash-shaped fetch rides eth_getBlockByHash after blockAt's
		// portability dispatch.
		node.Handle("eth_getBlockByNumber", func(c rpctest.Call) (any, error) {
			return json.RawMessage(pinned), nil
		})
		node.Return("eth_getBlockByHash", json.RawMessage(pinned))
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

func TestAdapterBlockByHashUsesDedicatedMethod(t *testing.T) {
	client, nodes := fleet(t, 3)
	pinned := fixtureBlock(t, 16)
	for _, node := range nodes {
		// A hash must reach the wire as eth_getBlockByHash: several public
		// providers reject a 32-byte hash in eth_getBlockByNumber's number
		// position, so the shortcut this test forbids is not portable.
		node.Handle("eth_getBlockByNumber", func(c rpctest.Call) (any, error) {
			return nil, fmt.Errorf("eth_getBlockByNumber must not be called for a hash lookup")
		})
		node.Return("eth_getBlockByHash", json.RawMessage(pinned))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	block, err := client.BlockByHash(ctx, common.HexToHash(blockHashOf(pinned)))
	if err != nil {
		t.Fatal(err)
	}
	if block.NumberU64() != 16 {
		t.Fatalf("expected block 16, got %d", block.NumberU64())
	}
}

func TestAdapterBlockByHashRejectsAnotherSelfConsistentBlock(t *testing.T) {
	client, nodes := fleet(t, 3)
	pinned := fixtureBlock(t, 16)
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockByHash, pinned)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	requested := common.HexToHash("0x" + strings.Repeat("42", 32))
	if _, err := client.BlockByHash(ctx, requested); !errors.Is(err, ethguard.ErrMalformedResponse) {
		t.Fatalf("expected ErrMalformedResponse for wrong requested block, got %v", err)
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
		// The derived height fetch rides the hash-addressed method.
		node.Return("eth_getBlockByHash", json.RawMessage(fin))
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

// fixtureReceipt marshals a schema-valid receipt through go-ethereum's own
// codec, so the wire shape honest sources emit is exercised end to end. The
// bloom is derived from the receipt's logs the way consensus nodes derive
// it, keeping DecodeReceipt's integrity check green by construction. The
// receipt claims the given height while its BlockHash stays the fleet's
// canonical pin, so hash-form and height-form bindings can be tested
// independently.
func fixtureReceipt(t *testing.T, txHash common.Hash, height int64) json.RawMessage {
	t.Helper()
	receipt := &types.Receipt{
		Type:              types.LegacyTxType,
		Status:            types.ReceiptStatusSuccessful,
		CumulativeGasUsed: 21000,
		Logs: []*types.Log{{
			Address:     common.HexToAddress("0x" + strings.Repeat("cc", 20)),
			Topics:      []common.Hash{common.HexToHash("0x" + strings.Repeat("dd", 32))},
			Data:        []byte{0x01, 0x02},
			BlockNumber: uint64(height),
			TxHash:      txHash,
			TxIndex:     1,
			BlockHash:   common.HexToHash("0x" + strings.Repeat("11", 32)),
			Index:       0,
		}},
		TxHash:            txHash,
		GasUsed:           21000,
		BlockHash:         common.HexToHash("0x" + strings.Repeat("11", 32)),
		BlockNumber:       big.NewInt(height),
		TransactionIndex:  1,
		EffectiveGasPrice: big.NewInt(1_000_000_000),
	}
	receipt.Bloom = types.CreateBloom(receipt)
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestAdapterTransactionReceiptReturnsVerifiedReceipt(t *testing.T) {
	client, nodes := fleet(t, 3)
	txHash := common.HexToHash("0x" + strings.Repeat("42", 32))
	raw := fixtureReceipt(t, txHash, 16)
	for _, node := range nodes {
		node.Return(methods.MethodGetTransactionReceipt, json.RawMessage(raw))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	receipt, err := client.TransactionReceipt(ctx, txHash)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.TxHash != txHash || receipt.Status != types.ReceiptStatusSuccessful {
		t.Fatalf("unexpected verified receipt: %+v", receipt)
	}
	if len(receipt.Logs) != 1 || receipt.Logs[0].TxHash != txHash {
		t.Fatalf("expected the receipt's logs to survive decoding, got %+v", receipt.Logs)
	}
}

func TestAdapterTransactionReceiptMapsVerifiedAbsenceToNotFound(t *testing.T) {
	client, nodes := fleet(t, 3)
	for _, node := range nodes {
		node.Return(methods.MethodGetTransactionReceipt, nil)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := client.TransactionReceipt(ctx, common.HexToHash("0x"+strings.Repeat("42", 32))); !errors.Is(err, ethereum.NotFound) {
		t.Fatalf("expected ethereum.NotFound, got %v", err)
	}
}

// signedTxFixture marshals a signed legacy transaction with the context
// fields a node reports: hash, from, and blockNumber when mined.
func signedTxFixture(t *testing.T, mined bool) json.RawMessage {
	t.Helper()
	key, err := crypto.HexToECDSA("deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	if err != nil {
		t.Fatal(err)
	}
	to := common.HexToAddress("0x" + strings.Repeat("bb", 20))
	tx := types.NewTransaction(7, to, big.NewInt(1_000_000_000_000_000_000), 21000, big.NewInt(1_000_000_000), nil)
	signed, err := types.SignTx(tx, types.HomesteadSigner{}, key)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := signed.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	obj["hash"] = json.RawMessage(fmt.Sprintf("%q", signed.Hash().Hex()))
	obj["from"] = json.RawMessage(fmt.Sprintf("%q", crypto.PubkeyToAddress(key.PublicKey).Hex()))
	if mined {
		obj["blockNumber"] = json.RawMessage(`"0x10"`)
	}
	out, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAdapterTransactionByHashReportsPendingFlag(t *testing.T) {
	client, nodes := fleet(t, 3)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Mined: the fleet holds the same transaction with its block context.
	minedRaw := signedTxFixture(t, true)
	var want types.Transaction
	if err := json.Unmarshal(minedRaw, &want); err != nil {
		t.Fatal(err)
	}
	txHash := want.Hash()
	for _, node := range nodes {
		node.Return(methods.MethodGetTransactionByHash, minedRaw)
	}
	tx, pending, err := client.TransactionByHash(ctx, txHash)
	if err != nil || pending {
		t.Fatalf("expected mined transaction without pending flag, got %+v pending=%v err=%v", tx, pending, err)
	}
	if tx.Nonce() != 7 {
		t.Fatalf("unexpected transaction: %+v", tx)
	}

	// Pending: the same object without block context — pending is an
	// ordinary verified state while the object stays stable.
	pendingRaw := signedTxFixture(t, false)
	for _, node := range nodes {
		node.Return(methods.MethodGetTransactionByHash, pendingRaw)
	}
	if _, pending, err := client.TransactionByHash(ctx, txHash); err != nil || !pending {
		t.Fatalf("expected pending flag from blockless payload, got pending=%v err=%v", pending, err)
	}
}

func TestAdapterTransactionByHashRejectsAnotherTransaction(t *testing.T) {
	client, nodes := fleet(t, 3)
	raw := signedTxFixture(t, true)
	for _, node := range nodes {
		node.Return(methods.MethodGetTransactionByHash, raw)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	requested := common.HexToHash("0x" + strings.Repeat("42", 32))
	if _, _, err := client.TransactionByHash(ctx, requested); !errors.Is(err, ethguard.ErrMalformedResponse) {
		t.Fatalf("expected ErrMalformedResponse for wrong requested transaction, got %v", err)
	}
}

func TestAdapterAnchoredStateReads(t *testing.T) {
	client, nodes := fleet(t, 3)
	pinned := "0x" + strings.Repeat("11", 32)
	for i, node := range nodes {
		node.Return("eth_getBlockByNumber", map[string]any{"number": "0x10", "hash": pinned})
		node.Handle("eth_getTransactionCount", func(c rpctest.Call) (any, error) {
			var tag string
			if len(c.Params) == 2 && json.Unmarshal(c.Params[1], &tag) == nil && tag == pinned {
				return "0x7", nil
			}
			return fmt.Sprintf("0x%x", 10+i), nil // divergence a moving tag would expose
		})
		node.Handle("eth_getStorageAt", func(c rpctest.Call) (any, error) {
			var tag string
			if len(c.Params) == 3 && json.Unmarshal(c.Params[2], &tag) == nil && tag == pinned {
				return "0x" + strings.Repeat("ab", 32), nil
			}
			return "0x" + strings.Repeat("cd", 32), nil
		})
		node.Handle("eth_getCode", func(c rpctest.Call) (any, error) {
			var tag string
			if len(c.Params) == 2 && json.Unmarshal(c.Params[1], &tag) == nil && tag == pinned {
				return "0x", nil // externally owned account: empty code is valid data
			}
			return "0xdead", nil
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	nonce, err := client.NonceAt(ctx, common.HexToAddress("0xdad"), nil)
	if err != nil || nonce != 7 {
		t.Fatalf("expected pinned nonce 7, got %d %v", nonce, err)
	}
	storage, err := client.StorageAt(ctx, common.HexToAddress("0xdad"), common.HexToHash("0x0"), nil)
	if err != nil || string(storage) != string(common.Hex2Bytes(strings.Repeat("ab", 32))) {
		t.Fatalf("expected pinned storage word, got %x %v", storage, err)
	}
	code, err := client.CodeAt(ctx, common.HexToAddress("0xdad"), nil)
	if err != nil || len(code) != 0 {
		t.Fatalf("expected empty verified code for the EOA answer, got %x %v", code, err)
	}

	// None of the anchored state reads may reach a source with a moving tag.
	for i, node := range nodes {
		for _, call := range node.Calls() {
			switch call.Method {
			case "eth_getTransactionCount", "eth_getStorageAt", "eth_getCode":
				var ref string
				if err := json.Unmarshal(call.Params[len(call.Params)-1], &ref); err != nil {
					t.Fatalf("node %d received malformed reference %s", i, call.Params[len(call.Params)-1])
				}
				if ref == "latest" || ref == "safe" || ref == "finalized" {
					t.Fatalf("node %d received an unrewritten moving tag %q for %s", i, ref, call.Method)
				}
			}
		}
	}
}

func TestAdapterSuggestGasTipCapTakesMedian(t *testing.T) {
	client, nodes := fleet(t, 3)
	nodes[0].Return(methods.MethodMaxPriorityFeePerGas, "0x3b9aca00") // 1 gwei
	nodes[1].Return(methods.MethodMaxPriorityFeePerGas, "0x3b9aca00")
	nodes[2].Return(methods.MethodMaxPriorityFeePerGas, "0x77359400") // 2 gwei outlier
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tip, err := client.SuggestGasTipCap(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if tip.Int64() != 1_000_000_000 {
		t.Fatalf("expected median 1 gwei, got %v", tip)
	}
}

func TestAdapterBlockReceiptsAcceptsNumberTagAndHash(t *testing.T) {
	client, nodes := fleet(t, 3)
	pin := "0x" + strings.Repeat("11", 32)
	txHash := common.HexToHash("0x" + strings.Repeat("42", 32))
	raw := fixtureReceipt(t, txHash, 16)
	for _, node := range nodes {
		node.Return("eth_getBlockByNumber", map[string]any{"number": "0x10", "hash": pin})
		node.Handle("eth_getBlockReceipts", func(c rpctest.Call) (any, error) {
			var ref string
			json.Unmarshal(c.Params[0], &ref)
			if ref == pin || ref == "0x10" { // rewritten tag and explicit number both land here
				return []json.RawMessage{json.RawMessage(raw)}, nil
			}
			return nil, fmt.Errorf("unexpected receipts reference %q", ref)
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	byHash, err := client.BlockReceipts(ctx, gethrpc.BlockNumberOrHashWithHash(common.HexToHash(pin), false))
	if err != nil || len(byHash) != 1 || byHash[0].TxHash != txHash {
		t.Fatalf("expected hash-addressed receipts, got %v", err)
	}
	byNumber, err := client.BlockReceipts(ctx, gethrpc.BlockNumberOrHashWithNumber(16))
	if err != nil || len(byNumber) != 1 {
		t.Fatalf("expected explicit-number receipts to pass through, got %v", err)
	}
	byTag, err := client.BlockReceipts(ctx, gethrpc.BlockNumberOrHashWithNumber(gethrpc.LatestBlockNumber))
	if err != nil || len(byTag) != 1 {
		t.Fatalf("expected moving tag to be pinned before fan-out, got %v", err)
	}
}

func TestAdapterBlockReceiptsRejectsAnotherBlockForHashRequest(t *testing.T) {
	client, nodes := fleet(t, 3)
	txHash := common.HexToHash("0x" + strings.Repeat("42", 32))
	raw := fixtureReceipt(t, txHash, 16)
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockReceipts, []json.RawMessage{raw})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	requested := common.HexToHash("0x" + strings.Repeat("22", 32))
	_, err := client.BlockReceipts(ctx, gethrpc.BlockNumberOrHashWithHash(requested, false))
	if !errors.Is(err, ethguard.ErrMalformedResponse) {
		t.Fatalf("expected ErrMalformedResponse for receipts from another block, got %v", err)
	}
}

// TestAdapterBlockReceiptsNumberFormBindsRequestedHeight pins the number-form
// half of the request-answer binding: a unanimous, self-consistent fleet
// returning another block's legitimate receipts still fails, because
// agreement cannot substitute for the binding.
func TestAdapterBlockReceiptsNumberFormBindsRequestedHeight(t *testing.T) {
	client, nodes := fleet(t, 3)
	txHash := common.HexToHash("0x" + strings.Repeat("42", 32))
	foreign := fixtureReceipt(t, txHash, 17) // legitimate receipts, wrong block
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockReceipts, []json.RawMessage{foreign})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.BlockReceipts(ctx, gethrpc.BlockNumberOrHashWithNumber(16))
	if !errors.Is(err, ethguard.ErrMalformedResponse) {
		t.Fatalf("expected ErrMalformedResponse for coherent receipts of another height, got %v", err)
	}
}

func TestAdapterBlockReceiptsNumberFormRejectsTruncatedHeight(t *testing.T) {
	client, nodes := fleet(t, 3)
	var receipt map[string]json.RawMessage
	if err := json.Unmarshal(fixtureReceipt(t, common.HexToHash("0x42"), 16), &receipt); err != nil {
		t.Fatal(err)
	}
	receipt["blockNumber"] = json.RawMessage(`"0x10000000000000010"`)
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockReceipts, []any{receipt})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := client.BlockReceipts(ctx, gethrpc.BlockNumberOrHashWithNumber(16)); !errors.Is(err, ethguard.ErrMalformedResponse) {
		t.Fatalf("expected full-height mismatch to fail, got %v", err)
	}
}

func TestAdapterBlockReceiptsNumberFormBindsEveryReceipt(t *testing.T) {
	client, nodes := fleet(t, 3)
	txHash := common.HexToHash("0x" + strings.Repeat("42", 32))
	good := fixtureReceipt(t, txHash, 16)
	foreign := fixtureReceipt(t, txHash, 17)
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockReceipts, []json.RawMessage{good, foreign})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.BlockReceipts(ctx, gethrpc.BlockNumberOrHashWithNumber(16))
	if !errors.Is(err, ethguard.ErrMalformedResponse) {
		t.Fatalf("expected one mismatched element to fail the whole array, got %v", err)
	}
}

func TestAdapterBlockReceiptsNumberFormRejectsMissingHeight(t *testing.T) {
	client, nodes := fleet(t, 3)
	txHash := common.HexToHash("0x" + strings.Repeat("42", 32))
	var receipt map[string]json.RawMessage
	if err := json.Unmarshal(fixtureReceipt(t, txHash, 16), &receipt); err != nil {
		t.Fatal(err)
	}
	delete(receipt, "blockNumber") // blockless receipts are legal wire; the binding must catch them
	stripped, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockReceipts, []json.RawMessage{stripped})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = client.BlockReceipts(ctx, gethrpc.BlockNumberOrHashWithNumber(16))
	if !errors.Is(err, ethguard.ErrMalformedResponse) {
		t.Fatalf("expected receipts without a height to fail the number-form binding, got %v", err)
	}
}

// TestAdapterBlockReceiptsTagFormBindsToThePinnedReference proves the
// tag-form binding rides decision.BlockRef: receipts whose block hash
// differs from the verified pin fail without a second anchor call.
func TestAdapterBlockReceiptsTagFormBindsToThePinnedReference(t *testing.T) {
	client, nodes := fleet(t, 3)
	txHash := common.HexToHash("0x" + strings.Repeat("42", 32))
	// fixtureReceipt's BlockHash is the pin (0x11…), so reroute the pin:
	// anchor reports 0x22…, receipts claim 0x11….
	pin := "0x" + strings.Repeat("22", 32)
	raw := fixtureReceipt(t, txHash, 16)
	for _, node := range nodes {
		node.Return("eth_getBlockByNumber", map[string]any{"number": "0x10", "hash": pin})
		node.Return(methods.MethodGetBlockReceipts, []json.RawMessage{raw})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.BlockReceipts(ctx, gethrpc.BlockNumberOrHashWithNumber(gethrpc.LatestBlockNumber))
	if !errors.Is(err, ethguard.ErrMalformedResponse) {
		t.Fatalf("expected receipts from another block than the pin to fail, got %v", err)
	}
	// No second establishment: one anchor fan-out per read is the budget.
	for i, node := range nodes {
		establishments := 0
		for _, call := range node.Calls() {
			if call.Method == "eth_getBlockByNumber" {
				establishments++
			}
		}
		if establishments != 1 {
			t.Fatalf("node %d: expected exactly one anchor establishment, got %d", i, establishments)
		}
	}
}

func TestAdapterBlockReceiptsNumberFormAcceptsEmptyBlock(t *testing.T) {
	client, nodes := fleet(t, 3)
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockReceipts, []json.RawMessage{})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	receipts, err := client.BlockReceipts(ctx, gethrpc.BlockNumberOrHashWithNumber(16))
	if err != nil || len(receipts) != 0 {
		t.Fatalf("an empty block stays a legal answer, got %+v err=%v", receipts, err)
	}
}

// headFixture pins one common latest head (number 0x10, hash 0xabc) so
// anchored reads rewrite moving tags deterministically.
func headFixture(nodes []*rpctest.Node) {
	for _, node := range nodes {
		node.Return("eth_getBlockByNumber", map[string]any{"number": "0x10", "hash": "0xabc"})
	}
}

func TestAdapterCallContractVerifiesResultsAndMapsReverts(t *testing.T) {
	adapter, nodes := fleet(t, 3)
	headFixture(nodes)
	for _, node := range nodes {
		node.Handle("eth_call", func(c rpctest.Call) (any, error) {
			var tag string
			if len(c.Params) == 2 && json.Unmarshal(c.Params[1], &tag) == nil && tag == "0xabc" {
				return "0xdeadbeef", nil
			}
			return nil, rpctest.RPCError(-32000, "execution reverted")
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	to := common.HexToAddress("0x1")
	msg := ethereum.CallMsg{To: &to, Data: []byte{0x00}}
	data, err := adapter.CallContract(ctx, msg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string([]byte{0xde, 0xad, 0xbe, 0xef}) {
		t.Fatalf("unexpected call result %x", data)
	}

	// A unanimous revert is a verified observation surfaced as RevertError,
	// with the pipeline sentinels left for pipeline failures.
	for _, node := range nodes {
		node.Fail("eth_call", -32000, "execution reverted: no funds")
	}
	_, err = adapter.CallContract(ctx, msg, nil)
	var revert *ethadapter.RevertError
	if !errors.As(err, &revert) {
		t.Fatalf("expected RevertError, got %v", err)
	}
	if revert.Message != "execution reverted: no funds" {
		t.Fatalf("unexpected revert message %q", revert.Message)
	}
	if !strings.Contains(err.Error(), "verified observation") {
		t.Fatalf("revert error should state its standing: %v", err)
	}
}

func TestAdapterEstimateGasPinsTagAndTakesMedian(t *testing.T) {
	adapter, nodes := fleet(t, 3)
	headFixture(nodes)
	estimates := []string{"0x64", "0x66", "0x68"}
	for i, node := range nodes {
		want := estimates[i]
		node.Handle("eth_estimateGas", func(c rpctest.Call) (any, error) {
			var tag string
			if len(c.Params) == 2 && json.Unmarshal(c.Params[1], &tag) == nil && tag == "0xabc" {
				return want, nil
			}
			return nil, rpctest.RPCError(-32602, "tag was not pinned")
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	to := common.HexToAddress("0x1")
	gas, err := adapter.EstimateGas(ctx, ethereum.CallMsg{To: &to})
	if err != nil {
		t.Fatal(err)
	}
	if gas != 0x66 {
		t.Fatalf("expected lower median 0x66, got %d", gas)
	}
}

func TestAdapterFilterLogsPinsTheFilterRange(t *testing.T) {
	adapter, nodes := fleet(t, 3)
	headFixture(nodes)
	logs := []map[string]any{{
		"address":          "0x0000000000000000000000000000000000000002",
		"blockNumber":      "0x10",
		"blockHash":        "0x00000000000000000000000000000000000000000000000000000000000000ab",
		"transactionHash":  "0x00000000000000000000000000000000000000000000000000000000000000cd",
		"transactionIndex": "0x0",
		"logIndex":         "0x0",
		"topics":           []any{},
		"data":             "0x",
		"removed":          false,
	}}
	for _, node := range nodes {
		node.Handle("eth_getLogs", func(c rpctest.Call) (any, error) {
			var filter map[string]any
			if len(c.Params) != 1 || json.Unmarshal(c.Params[0], &filter) != nil {
				return nil, rpctest.RPCError(-32602, "expected filter object")
			}
			if filter["fromBlock"] == "0x0" && filter["toBlock"] == "0x10" {
				return logs, nil
			}
			return nil, rpctest.RPCError(-32602, "range was not pinned to the verified height")
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, err := adapter.FilterLogs(ctx, ethereum.FilterQuery{FromBlock: big.NewInt(0)})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].BlockNumber != 16 {
		t.Fatalf("unexpected logs: %+v", got)
	}
}

// TestAdapterBlockAtBindsAnswersToTheRequestedReference pins the adapter
// half of the request-answer binding: a coherent foreign block (self-
// consistent payload, unanimous fleet) is rejected at the typed boundary
// for both the hash-addressed and the height-addressed forms.
func TestAdapterBlockAtBindsAnswersToTheRequestedReference(t *testing.T) {
	t.Run("foreign block by hash", func(t *testing.T) {
		adapter, nodes := fleet(t, 3)
		for _, node := range nodes {
			node.Return("eth_getBlockByHash", json.RawMessage(fixtureBlock(t, 5)))
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		requested := common.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111")
		_, err := adapter.BlockByHash(ctx, requested)
		if !errors.Is(err, ethguard.ErrMalformedResponse) {
			t.Fatalf("expected malformed-response binding failure, got %v", err)
		}
	})

	t.Run("foreign block by height", func(t *testing.T) {
		adapter, nodes := fleet(t, 3)
		for _, node := range nodes {
			node.Return("eth_getBlockByNumber", json.RawMessage(fixtureBlock(t, 7)))
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := adapter.BlockByNumber(ctx, big.NewInt(5))
		if !errors.Is(err, ethguard.ErrMalformedResponse) {
			t.Fatalf("expected malformed-response binding failure, got %v", err)
		}
	})
}

func TestReceiptNullLogReturnsMalformed(t *testing.T) {
	for _, batch := range []bool{false, true} {
		t.Run(fmt.Sprint(batch), func(t *testing.T) {
			client, nodes := fleet(t, 2)
			hash := common.HexToHash("0x42")
			var receipt map[string]json.RawMessage
			if err := json.Unmarshal(fixtureReceipt(t, hash, 16), &receipt); err != nil {
				t.Fatal(err)
			}
			receipt["logs"] = json.RawMessage(`[null]`)
			for _, node := range nodes {
				node.Return(methods.MethodGetTransactionReceipt, receipt)
				node.Return(methods.MethodGetBlockReceipts, []any{receipt})
			}
			var err error
			if batch {
				_, err = client.BlockReceipts(context.Background(), gethrpc.BlockNumberOrHashWithNumber(16))
			} else {
				_, err = client.TransactionReceipt(context.Background(), hash)
			}
			if !errors.Is(err, ethguard.ErrMalformedResponse) {
				t.Fatalf("expected malformed null log, got %v", err)
			}
		})
	}
}

func TestAdapterBlockByNumberRejectsOverflowBeforeDispatch(t *testing.T) {
	client, nodes := fleet(t, 2)
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockByNumber, fixtureBlock(t, 16))
	}
	number := new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 64), big.NewInt(16))
	if _, err := client.BlockByNumber(context.Background(), number); !errors.Is(err, ethguard.ErrInvalidRequest) {
		t.Fatalf("expected unsupported request height, got %v", err)
	}
	for _, node := range nodes {
		if len(node.Calls()) != 0 {
			t.Fatal("unsupported height reached the wire")
		}
	}
}

func TestDecodeTransactionRejectsUnsigned(t *testing.T) {
	tx := types.NewTx(&types.LegacyTx{Gas: 21000, GasPrice: big.NewInt(1), Value: big.NewInt(0)})
	raw, err := json.Marshal(tx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ethguard.DecodeTransaction(raw); !errors.Is(err, ethguard.ErrMalformedResponse) {
		t.Fatalf("expected unsigned rejection, got %v", err)
	}
}

func TestAdapterRequiresCorroborationWithoutChangingMethodSignatures(t *testing.T) {
	client, nodes := fleet(t, 2)
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockByNumber, map[string]any{"number": "0x10", "hash": "0xabc"})
	}
	nodes[0].Return(methods.MethodGetBalance, "0x1")
	nodes[1].Fail(methods.MethodGetBalance, -32603, "unavailable")
	if value, err := client.BalanceAt(context.Background(), common.Address{}, nil); !errors.Is(err, ethguard.ErrConflict) || value != nil {
		t.Fatalf("typed read hid unmet corroboration: %v %v", value, err)
	}
	// The advanced API reports the unmet majority as a conflict with the
	// surviving cluster in evidence (decision 0029): a 1-of-2 answer is no
	// longer surfaced as Selected under the default support floor.
	d, err := client.Unwrap().Call(context.Background(), methods.MethodGetBalance, common.Address{}, "0x10")
	if !errors.Is(err, ethguard.ErrConflict) {
		t.Fatalf("advanced call hid the unmet majority: %v", err)
	}
	if len(d.Support) != 1 || string(d.Value) != `"0x1"` {
		t.Fatalf("expected the surviving cluster in evidence, got %+v", d)
	}
	wrapped := ethadapter.Wrap(client.Unwrap())
	if _, err := wrapped.BalanceAt(context.Background(), common.Address{}, big.NewInt(16)); !errors.Is(err, ethguard.ErrConflict) {
		t.Fatalf("Wrap bypassed verification: %v", err)
	}
}

func TestDecodeTypedTransactionAllowsZeroParity(t *testing.T) {
	key, err := crypto.HexToECDSA(strings.Repeat("11", 32))
	if err != nil {
		t.Fatal(err)
	}
	for nonce := uint64(0); nonce < 100; nonce++ {
		tx, err := types.SignTx(types.NewTx(&types.DynamicFeeTx{ChainID: big.NewInt(1), Nonce: nonce, Gas: 21000, GasFeeCap: big.NewInt(2), GasTipCap: big.NewInt(1), Value: big.NewInt(0)}), types.NewLondonSigner(big.NewInt(1)), key)
		if err != nil {
			t.Fatal(err)
		}
		v, _, _ := tx.RawSignatureValues()
		if v.Sign() != 0 {
			continue
		}
		raw, err := json.Marshal(tx)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := ethguard.DecodeTransaction(raw); err != nil {
			t.Fatalf("valid parity-zero transaction rejected: %v", err)
		}
		return
	}
	t.Fatal("fixture did not produce parity zero")
}
