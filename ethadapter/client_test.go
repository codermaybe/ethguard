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
// it, keeping DecodeReceipt's integrity check green by construction.
func fixtureReceipt(t *testing.T, txHash common.Hash) json.RawMessage {
	t.Helper()
	receipt := &types.Receipt{
		Type:              types.LegacyTxType,
		Status:            types.ReceiptStatusSuccessful,
		CumulativeGasUsed: 21000,
		Logs: []*types.Log{{
			Address:     common.HexToAddress("0x" + strings.Repeat("cc", 20)),
			Topics:      []common.Hash{common.HexToHash("0x" + strings.Repeat("dd", 32))},
			Data:        []byte{0x01, 0x02},
			BlockNumber: 16,
			TxHash:      txHash,
			TxIndex:     1,
			BlockHash:   common.HexToHash("0x" + strings.Repeat("11", 32)),
			Index:       0,
		}},
		TxHash:            txHash,
		GasUsed:           21000,
		BlockHash:         common.HexToHash("0x" + strings.Repeat("11", 32)),
		BlockNumber:       big.NewInt(16),
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
	raw := fixtureReceipt(t, txHash)
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
	raw := fixtureReceipt(t, txHash)
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
	raw := fixtureReceipt(t, txHash)
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
