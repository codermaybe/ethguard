package ethguard_test

import (
	"bytes"
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

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/testkit/ethapitest"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/codermaybe/ethguard/rpctest"
)

// fleet spins up count scripted nodes behind one adapter client.
func fleet(t *testing.T, count int, opts ...ethguard.Option) (*ethguard.Client, []*rpctest.Node) {
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
	client, err := ethguard.New(cfg, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client, nodes
}

// mustQuantity builds the generated quantity for an explicit height.
func mustQuantity(t *testing.T, n uint64) ethapi.Quantity {
	t.Helper()
	q, err := ethapi.NewQuantity(json.RawMessage(fmt.Sprintf(`"0x%x"`, n)))
	if err != nil {
		t.Fatal(err)
	}
	return q
}

// numberRef is the explicit-height BlockReference form (the migrated shape of
// gethrpc.BlockNumberOrHashWithNumber).
func numberRef(t *testing.T, n uint64) ethapi.BlockReference {
	q := mustQuantity(t, n)
	return ethapi.BlockReference{BlockNumber: &q}
}

// hashRef is the hash-addressed BlockReference form (the migrated shape of
// gethrpc.BlockNumberOrHashWithHash(h, false)). It uses the EIP-1898 hash
// object: the generated parser round-trips a bare hash string through its
// quantity branch first, so only the object carries the hash binding.
func hashRef(h ethapi.Hash) ethapi.BlockReference {
	return ethapi.BlockReference{Eip1898BlockHashObject: &ethapi.BlockHashReference{BlockHash: h}}
}

// tagRef is the moving-tag BlockReference form.
func tagRef(tag string) ethapi.BlockReference {
	tg := ethapi.Text(tag)
	return ethapi.BlockReference{BlockTag: &tg}
}

// txHashOf returns the declared hash of the selected generated transaction
// branch (the migrated shape of types.Transaction.Hash()).
func txHashOf(tx *ethapi.Transaction) string {
	switch {
	case tx == nil:
		return ""
	case tx.Signed7702Transaction != nil:
		return string(tx.Signed7702Transaction.Hash)
	case tx.Signed4844Transaction != nil:
		return string(tx.Signed4844Transaction.Hash)
	case tx.Signed1559Transaction != nil:
		return string(tx.Signed1559Transaction.Hash)
	case tx.Signed2930Transaction != nil:
		return string(tx.Signed2930Transaction.Hash)
	case tx.SignedLegacyTransaction != nil:
		return string(tx.SignedLegacyTransaction.Hash)
	default:
		return ""
	}
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
	obj["size"] = json.RawMessage(fmt.Sprintf("%q", fmt.Sprintf("0x%x", block.Size())))
	obj["transactions"] = json.RawMessage(`[]`)
	obj["uncles"] = json.RawMessage(`[]`)
	out, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestTypedChainIDIsCorroborated(t *testing.T) {
	client, _ := fleet(t, 3)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	id, _, err := client.ChainID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if id.Int64() != 1 {
		t.Fatalf("expected chain id 1, got %v", id)
	}
}

func TestTypedBalanceAtPinsMovingTag(t *testing.T) {
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

	balance, _, err := client.BalanceAt(ctx, ethapi.Address("0x0000000000000000000000000000000000000dad"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if balance.Int64() != 100 {
		t.Fatalf("expected pinned balance 100, got %v", balance)
	}
}

func TestTypedBlockByNumberNeverFetchesAMovingTag(t *testing.T) {
	client, nodes := fleet(t, 3)
	pinned := fixtureBlock(t, 16)
	for _, node := range nodes {
		// Every reference resolves to the same self-consistent block, so
		// anchor establishment and the pinned fetch agree end to end. The
		// hash-shaped fetch rides eth_getBlockByHash after blockAt's
		// portability dispatch.
		node.Handle("eth_getBlockByNumber", func(c rpctest.Call) (any, error) {
			return pinned, nil
		})
		node.Return("eth_getBlockByHash", pinned)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	block, _, err := client.BlockByNumber(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	height, heightErr := rpc.ParseHexQuantity(string(block.Number))
	if heightErr != nil {
		t.Fatal(heightErr)
	}
	if height != 16 {
		t.Fatalf("expected pinned block 16, got %d", height)
	}

	// The full-transaction fetches must carry explicit references only; any
	// "latest" on the wire belongs to anchor establishment (fullTx=false).
	for i, node := range nodes {
		for _, call := range node.Calls() {
			if call.Method != methods.MethodGetBlockByNumber || len(call.Params) < 2 {
				continue
			}
			var fullTx bool
			if err := json.Unmarshal(call.Params[1], &fullTx); err != nil {
				t.Fatalf("decode recorded request: %v", err)
			}
			var ref string
			if err := json.Unmarshal(call.Params[0], &ref); err != nil {
				t.Fatalf("decode recorded request: %v", err)
			}
			if fullTx && (ref == "latest" || ref == "safe" || ref == "finalized") {
				t.Fatalf("node %d fetched a moving tag with full bodies: %q", i, ref)
			}
		}
	}
}

func TestTypedBlockByHashUsesDedicatedMethod(t *testing.T) {
	client, nodes := fleet(t, 3)
	pinned := fixtureBlock(t, 16)
	for _, node := range nodes {
		// A hash must reach the wire as eth_getBlockByHash: several public
		// providers reject a 32-byte hash in eth_getBlockByNumber's number
		// position, so the shortcut this test forbids is not portable.
		node.Handle("eth_getBlockByNumber", func(c rpctest.Call) (any, error) {
			return nil, fmt.Errorf("eth_getBlockByNumber must not be called for a hash lookup")
		})
		node.Return("eth_getBlockByHash", pinned)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	block, _, err := client.BlockByHash(ctx, ethapi.Hash(blockHashOf(t, pinned)))
	if err != nil {
		t.Fatal(err)
	}
	height, heightErr := rpc.ParseHexQuantity(string(block.Number))
	if heightErr != nil {
		t.Fatal(heightErr)
	}
	if height != 16 {
		t.Fatalf("expected block 16, got %d", height)
	}
}

func TestTypedBlockByHashRejectsAnotherSelfConsistentBlock(t *testing.T) {
	client, nodes := fleet(t, 3)
	pinned := fixtureBlock(t, 16)
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockByHash, pinned)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	requested := ethapi.Hash("0x" + strings.Repeat("42", 32))
	if _, _, err := client.BlockByHash(ctx, requested); !errors.Is(err, ethguard.ErrMalformedResponse) {
		t.Fatalf("expected ErrMalformedResponse for wrong requested block, got %v", err)
	}
}

func TestTypedBlockNumberMatchesCorroboratedReference(t *testing.T) {
	client, nodes := fleet(t, 3, ethguard.WithFinalizedAnchoring())
	fin := fixtureBlock(t, 42)
	pin := blockHashOf(t, fin)
	for _, node := range nodes {
		node.Handle("eth_getBlockByNumber", func(c rpctest.Call) (any, error) {
			var tag string
			if err := json.Unmarshal(c.Params[0], &tag); err != nil {
				t.Errorf("decode fixture request: %v", err)
				return nil, err
			}
			if tag == "finalized" || strings.EqualFold(tag, pin) {
				return fin, nil
			}
			return map[string]any{"number": "0x99", "hash": "0xtip"}, nil // divergent tips
		})
		// The derived height fetch rides the hash-addressed method.
		node.Return("eth_getBlockByHash", fin)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	height, _, err := client.BlockNumber(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if height != 42 {
		t.Fatalf("expected corroborated finalized reference height 42, got %d", height)
	}
}

func TestTypedSurfacesMalformedClass(t *testing.T) {
	client, nodes := fleet(t, 2)
	// Passes head normalization (number and hash present) but cannot become
	// a block: the failure must surface as the typed-boundary class.
	for _, node := range nodes {
		node.Return("eth_getBlockByNumber", map[string]any{"number": "0x10", "hash": "0xabc"})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, _, err := client.BlockByNumber(ctx, big.NewInt(16)); !errors.Is(err, ethguard.ErrMalformedResponse) {
		t.Fatalf("expected ErrMalformedResponse, got %v", err)
	}
}

func TestTypedSuggestGasPriceTakesMedian(t *testing.T) {
	client, nodes := fleet(t, 3)
	nodes[0].Return("eth_gasPrice", "0x3b9aca00") // 1 gwei
	nodes[1].Return("eth_gasPrice", "0x3b9aca00")
	nodes[2].Return("eth_gasPrice", "0x77359400") // 2 gwei outlier
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	price, _, err := client.SuggestGasPrice(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if price.Int64() != 1_000_000_000 {
		t.Fatalf("expected median 1 gwei, got %v", price)
	}
}

func TestTypedLocalObservations(t *testing.T) {
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

	peers, _, err := client.PeerCount(ctx)
	if err != nil || peers != 10 {
		t.Fatalf("expected peer count 10, got %d %v", peers, err)
	}
	progress, _, err := client.SyncProgress(ctx)
	if err != nil || progress != nil {
		t.Fatalf("expected nil progress from the first source, got %+v %v", progress, err)
	}
}

// TestTypedPeerCountOverflowIsClassifiedMalformed pins the projection's error
// class: a folded quantity the uint64 boundary cannot hold is a typed-boundary
// shaping failure, the same ErrMalformedResponse class the sibling
// TransactionCount projection and every hexutil decode already carry.
func TestTypedPeerCountOverflowIsClassifiedMalformed(t *testing.T) {
	client, nodes := fleet(t, 3)
	for _, node := range nodes {
		node.Return("net_peerCount", "0x10000000000000000") // 2^64: canonical, unshappable
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _, err := client.PeerCount(ctx)
	if !errors.Is(err, ethguard.ErrMalformedResponse) {
		t.Fatalf("expected ErrMalformedResponse for an out-of-range peer count, got %v", err)
	}
}

// blockHashOf extracts the hash field of a fixture payload.
func blockHashOf(t testing.TB, raw json.RawMessage) string {
	t.Helper()
	var obj struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Errorf("decode block fixture: %v", err)
		return ""
	}
	return obj.Hash
}

// fixtureReceipt marshals a schema-valid receipt through go-ethereum's own
// codec, so the wire shape honest sources emit is exercised end to end. The
// bloom is derived from the receipt's logs the way consensus nodes derive
// it, keeping DecodeReceipt's integrity check green by construction. The
// receipt claims the given height while its BlockHash stays the fleet's
// canonical pin, so hash-form and height-form bindings can be tested
// independently.
func fixtureReceipt(t *testing.T, txHash ethapi.Hash, height int64) json.RawMessage {
	t.Helper()
	gethTxHash := common.HexToHash(string(txHash))
	receipt := &types.Receipt{
		Type:              types.LegacyTxType,
		Status:            types.ReceiptStatusSuccessful,
		CumulativeGasUsed: 21000,
		Logs: []*types.Log{{
			Address:     common.HexToAddress("0x" + strings.Repeat("cc", 20)),
			Topics:      []common.Hash{common.HexToHash("0x" + strings.Repeat("dd", 32))},
			Data:        []byte{0x01, 0x02},
			BlockNumber: uint64(height),
			TxHash:      gethTxHash,
			TxIndex:     1,
			BlockHash:   common.HexToHash("0x" + strings.Repeat("11", 32)),
			Index:       0,
		}},
		TxHash:            gethTxHash,
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
	// types.Receipt is a typed projection, not the complete RPC information
	// object: the RPC sender is required separately and protected by comparison.
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	object["from"] = json.RawMessage(`"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`)
	// Geth's typed marshaler emits root:"0x" for a post-Byzantium receipt;
	// the RPC information object omits that inapplicable state root.
	delete(object, "root")
	raw, err = json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestTypedTransactionReceiptReturnsCorroboratedReceipt(t *testing.T) {
	client, nodes := fleet(t, 3)
	txHash := ethapi.Hash("0x" + strings.Repeat("42", 32))
	raw := fixtureReceipt(t, txHash, 16)
	for _, node := range nodes {
		node.Return(methods.MethodGetTransactionReceipt, raw)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	receipt, _, err := client.TransactionReceipt(ctx, txHash)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.TransactionHash != txHash || receipt.Status == nil || *receipt.Status != "0x1" {
		t.Fatalf("unexpected corroborated receipt: %+v", receipt)
	}
	if len(receipt.Logs) != 1 || receipt.Logs[0].TransactionHash != txHash {
		t.Fatalf("expected the receipt's logs to survive decoding, got %+v", receipt.Logs)
	}
}

func TestTypedTransactionReceiptMapsCorroboratedAbsenceToNotFound(t *testing.T) {
	client, nodes := fleet(t, 3)
	for _, node := range nodes {
		node.Return(methods.MethodGetTransactionReceipt, nil)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, _, err := client.TransactionReceipt(ctx, ethapi.Hash("0x"+strings.Repeat("42", 32))); !errors.Is(err, ethereum.NotFound) {
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
	// Geth's union encoder emits null fee fields from other transaction types;
	// those fields are not part of the official legacy information branch.
	for _, field := range []string{"maxFeePerGas", "maxPriorityFeePerGas"} {
		delete(obj, field)
	}
	// RPC transaction information includes explicit inclusion context, unlike
	// types.Transaction.MarshalJSON's signed-payload-only representation.
	for _, field := range []string{"blockHash", "blockNumber", "blockTimestamp", "transactionIndex"} {
		obj[field] = json.RawMessage(`null`)
	}
	if mined {
		obj["blockHash"] = json.RawMessage(`"0x3333333333333333333333333333333333333333333333333333333333333333"`)
		obj["blockNumber"] = json.RawMessage(`"0x10"`)
		obj["blockTimestamp"] = json.RawMessage(`"0x5"`)
		obj["transactionIndex"] = json.RawMessage(`"0x0"`)
	}
	out, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestTypedTransactionByHashReportsPendingFlag(t *testing.T) {
	client, nodes := fleet(t, 3)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Mined: the fleet holds the same transaction with its block context.
	minedRaw := signedTxFixture(t, true)
	want, wantErr := ethapi.NewTransaction(minedRaw)
	if wantErr != nil {
		t.Fatal(wantErr)
	}
	txHash := ethapi.Hash(txHashOf(&want))
	for _, node := range nodes {
		node.Return(methods.MethodGetTransactionByHash, minedRaw)
	}
	tx, pending, _, err := client.TransactionByHash(ctx, txHash)
	if err != nil || pending {
		t.Fatalf("expected mined transaction without pending flag, got %+v pending=%v err=%v", tx, pending, err)
	}
	if tx.SignedLegacyTransaction == nil {
		t.Fatalf("unexpected transaction: %+v", tx)
	}
	nonce, nonceErr := rpc.ParseHexQuantity(string(tx.SignedLegacyTransaction.Nonce))
	if nonceErr != nil {
		t.Fatal(nonceErr)
	}
	if nonce != 7 {
		t.Fatalf("unexpected transaction: %+v", tx)
	}

	// Pending: the same object with explicit null block context — pending is an
	// ordinary corroborated state while the object stays stable.
	pendingRaw := signedTxFixture(t, false)
	for _, node := range nodes {
		node.Return(methods.MethodGetTransactionByHash, pendingRaw)
	}
	if _, pending, _, err := client.TransactionByHash(ctx, txHash); err != nil || !pending {
		t.Fatalf("expected pending flag from blockless payload, got pending=%v err=%v", pending, err)
	}
}

func TestTypedTransactionByHashRejectsAnotherTransaction(t *testing.T) {
	client, nodes := fleet(t, 3)
	raw := signedTxFixture(t, true)
	for _, node := range nodes {
		node.Return(methods.MethodGetTransactionByHash, raw)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	requested := ethapi.Hash("0x" + strings.Repeat("42", 32))
	if _, _, _, err := client.TransactionByHash(ctx, requested); !errors.Is(err, ethguard.ErrMalformedResponse) {
		t.Fatalf("expected ErrMalformedResponse for wrong requested transaction, got %v", err)
	}
}

func TestTypedAnchoredStateReads(t *testing.T) {
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

	nonce, _, err := client.NonceAt(ctx, ethapi.Address("0x0000000000000000000000000000000000000dad"), nil)
	if err != nil || nonce != 7 {
		t.Fatalf("expected pinned nonce 7, got %d %v", nonce, err)
	}
	storage, _, err := client.StorageAt(ctx, ethapi.Address("0x0000000000000000000000000000000000000dad"), ethapi.Hash("0x"+strings.Repeat("0", 64)), nil)
	if err != nil || !bytes.Equal(storage, bytes.Repeat([]byte{0xab}, 32)) {
		t.Fatalf("expected pinned storage word, got %x %v", storage, err)
	}
	code, _, err := client.CodeAt(ctx, ethapi.Address("0x0000000000000000000000000000000000000dad"), nil)
	if err != nil || len(code) != 0 {
		t.Fatalf("expected empty corroborated code for the EOA answer, got %x %v", code, err)
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

func TestTypedSuggestGasTipCapTakesMedian(t *testing.T) {
	client, nodes := fleet(t, 3)
	nodes[0].Return(methods.MethodMaxPriorityFeePerGas, "0x3b9aca00") // 1 gwei
	nodes[1].Return(methods.MethodMaxPriorityFeePerGas, "0x3b9aca00")
	nodes[2].Return(methods.MethodMaxPriorityFeePerGas, "0x77359400") // 2 gwei outlier
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tip, _, err := client.SuggestGasTipCap(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if tip.Int64() != 1_000_000_000 {
		t.Fatalf("expected median 1 gwei, got %v", tip)
	}
}

func TestTypedBlockReceiptsAcceptsNumberTagAndHash(t *testing.T) {
	client, nodes := fleet(t, 3)
	pin := "0x" + strings.Repeat("11", 32)
	txHash := ethapi.Hash("0x" + strings.Repeat("42", 32))
	raw := fixtureReceipt(t, txHash, 16)
	for _, node := range nodes {
		node.Return("eth_getBlockByNumber", map[string]any{"number": "0x10", "hash": pin})
		node.Handle("eth_getBlockReceipts", func(c rpctest.Call) (any, error) {
			var ref string
			if err := json.Unmarshal(c.Params[0], &ref); err != nil {
				var object struct {
					BlockHash string `json:"blockHash"`
				}
				if err := json.Unmarshal(c.Params[0], &object); err != nil || object.BlockHash == "" {
					return nil, fmt.Errorf("unexpected receipts reference %s", c.Params[0])
				}
				ref = object.BlockHash
			}
			if ref == pin || ref == "0x10" { // hash object and explicit number both land here
				return []json.RawMessage{raw}, nil
			}
			return nil, fmt.Errorf("unexpected receipts reference %q", ref)
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	byHash, _, err := client.BlockReceipts(ctx, hashRef(ethapi.Hash(pin)))
	if err != nil || len(byHash) != 1 || byHash[0].TransactionHash != txHash {
		t.Fatalf("expected hash-addressed receipts, got %v", err)
	}
	byNumber, _, err := client.BlockReceipts(ctx, numberRef(t, 16))
	if err != nil || len(byNumber) != 1 {
		t.Fatalf("expected explicit-number receipts to pass through, got %v", err)
	}
	byTag, _, err := client.BlockReceipts(ctx, tagRef("latest"))
	if err != nil || len(byTag) != 1 {
		t.Fatalf("expected moving tag to be pinned before fan-out, got %v", err)
	}
}

// TestTypedBlockReceiptsBindsEarliestAsExplicitHeightZero pins the earliest
// sentinel's binding class: geth encodes EarliestBlockNumber as the explicit
// height "0x0", so the receipts it returns must bind by that height exactly
// like any other explicit number — never by the moving-tag pin path, which
// has no reference to bind against and would fail a successful read.
func TestTypedBlockReceiptsBindsEarliestAsExplicitHeightZero(t *testing.T) {
	client, nodes := fleet(t, 3)
	txHash := ethapi.Hash("0x" + strings.Repeat("42", 32))
	raw := fixtureReceipt(t, txHash, 0)
	for _, node := range nodes {
		node.Handle("eth_getBlockReceipts", func(c rpctest.Call) (any, error) {
			var ref string
			if err := json.Unmarshal(c.Params[0], &ref); err != nil {
				t.Errorf("decode fixture request: %v", err)
				return nil, err
			}
			if ref == "0x0" {
				return []json.RawMessage{raw}, nil
			}
			return nil, fmt.Errorf("unexpected receipts reference %q", ref)
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	receipts, _, err := client.BlockReceipts(ctx, numberRef(t, 0))
	if err != nil {
		t.Fatalf("expected earliest to bind as explicit height zero, got %v", err)
	}
	if len(receipts) != 1 || receipts[0].TransactionHash != txHash {
		t.Fatalf("expected the height-zero receipt, got %+v", receipts)
	}
	heightZero, heightErr := rpc.ParseHexQuantity(string(receipts[0].BlockNumber))
	if heightErr != nil || heightZero != 0 {
		t.Fatalf("expected the height-zero receipt, got %+v", receipts)
	}
}

func TestTypedBlockReceiptsRejectsAnotherBlockForHashRequest(t *testing.T) {
	client, nodes := fleet(t, 3)
	txHash := ethapi.Hash("0x" + strings.Repeat("42", 32))
	raw := fixtureReceipt(t, txHash, 16)
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockReceipts, []json.RawMessage{raw})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	requested := ethapi.Hash("0x" + strings.Repeat("22", 32))
	_, _, err := client.BlockReceipts(ctx, hashRef(requested))
	if !errors.Is(err, ethguard.ErrMalformedResponse) {
		t.Fatalf("expected ErrMalformedResponse for receipts from another block, got %v", err)
	}
}

// TestAdapterBlockReceiptsNumberFormBindsRequestedHeight pins the number-form
// half of the request-answer binding: a unanimous, self-consistent fleet
// returning another block's legitimate receipts still fails, because
// agreement cannot substitute for the binding.
func TestTypedBlockReceiptsNumberFormBindsRequestedHeight(t *testing.T) {
	client, nodes := fleet(t, 3)
	txHash := ethapi.Hash("0x" + strings.Repeat("42", 32))
	foreign := fixtureReceipt(t, txHash, 17) // legitimate receipts, wrong block
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockReceipts, []json.RawMessage{foreign})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _, err := client.BlockReceipts(ctx, numberRef(t, 16))
	if !errors.Is(err, ethguard.ErrMalformedResponse) {
		t.Fatalf("expected ErrMalformedResponse for coherent receipts of another height, got %v", err)
	}
}

func TestTypedBlockReceiptsNumberFormRejectsTruncatedHeight(t *testing.T) {
	client, nodes := fleet(t, 3)
	var receipt map[string]json.RawMessage
	if err := json.Unmarshal(fixtureReceipt(t, ethapi.Hash("0x"+strings.Repeat("0", 62)+"42"), 16), &receipt); err != nil {
		t.Fatal(err)
	}
	receipt["blockNumber"] = json.RawMessage(`"0x10000000000000010"`)
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockReceipts, []any{receipt})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := client.BlockReceipts(ctx, numberRef(t, 16)); !errors.Is(err, ethguard.ErrMalformedResponse) {
		t.Fatalf("expected full-height mismatch to fail, got %v", err)
	}
}

func TestTypedBlockReceiptsNumberFormBindsEveryReceipt(t *testing.T) {
	client, nodes := fleet(t, 3)
	txHash := ethapi.Hash("0x" + strings.Repeat("42", 32))
	good := fixtureReceipt(t, txHash, 16)
	foreign := fixtureReceipt(t, txHash, 17)
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockReceipts, []json.RawMessage{good, foreign})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _, err := client.BlockReceipts(ctx, numberRef(t, 16))
	if !errors.Is(err, ethguard.ErrMalformedResponse) {
		t.Fatalf("expected one mismatched element to fail the whole array, got %v", err)
	}
}

func TestTypedBlockReceiptsNumberFormRejectsMissingHeight(t *testing.T) {
	client, nodes := fleet(t, 3)
	txHash := ethapi.Hash("0x" + strings.Repeat("42", 32))
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

	_, _, err = client.BlockReceipts(ctx, numberRef(t, 16))
	if !errors.Is(err, ethguard.ErrMalformedResponse) {
		t.Fatalf("expected receipts without a height to fail the number-form binding, got %v", err)
	}
}

// TestAdapterBlockReceiptsTagFormBindsToThePinnedReference proves the
// tag-form binding rides decision.BlockRef: receipts whose block hash
// differs from the corroborated pin fail without a second anchor call.
func TestTypedBlockReceiptsTagFormBindsToThePinnedReference(t *testing.T) {
	client, nodes := fleet(t, 3)
	txHash := ethapi.Hash("0x" + strings.Repeat("42", 32))
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

	_, _, err := client.BlockReceipts(ctx, tagRef("latest"))
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

func TestTypedBlockReceiptsNumberFormAcceptsEmptyBlock(t *testing.T) {
	client, nodes := fleet(t, 3)
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockReceipts, []json.RawMessage{})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	receipts, _, err := client.BlockReceipts(ctx, numberRef(t, 16))
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

func TestTypedCallContractCorroboratesResultsAndMapsReverts(t *testing.T) {
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
	data, _, err := adapter.CallContract(ctx, ethapitest.Call(msg), nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string([]byte{0xde, 0xad, 0xbe, 0xef}) {
		t.Fatalf("unexpected call result %x", data)
	}

	// A unanimous revert is a corroborated observation surfaced as RevertError,
	// with the pipeline sentinels left for pipeline failures.
	for _, node := range nodes {
		node.Fail("eth_call", -32000, "execution reverted: no funds")
	}
	_, _, err = adapter.CallContract(ctx, ethapitest.Call(msg), nil)
	var revert *ethguard.RevertError
	if !errors.As(err, &revert) {
		t.Fatalf("expected RevertError, got %v", err)
	}
	if !strings.Contains(err.Error(), "execution reverted: no funds") {
		t.Fatalf("unexpected revert error %q", err)
	}
	if !strings.Contains(err.Error(), "corroborated observation") {
		t.Fatalf("revert error should state its standing: %v", err)
	}
}

func TestTypedEstimateGasPinsTagAndTakesMedian(t *testing.T) {
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
	gas, _, err := adapter.EstimateGas(ctx, ethapitest.Call(ethereum.CallMsg{To: &to}))
	if err != nil {
		t.Fatal(err)
	}
	if gas != 0x66 {
		t.Fatalf("expected lower median 0x66, got %d", gas)
	}
}

func TestTypedFilterLogsPinsTheFilterRange(t *testing.T) {
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
			return nil, rpctest.RPCError(-32602, "range was not pinned to the corroborated height")
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	query, queryErr := ethapi.NewFilterQuery(json.RawMessage(`{"fromBlock":"0x0"}`))
	if queryErr != nil {
		t.Fatal(queryErr)
	}
	got, _, err := adapter.FilterLogs(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].BlockNumber == nil {
		t.Fatalf("unexpected logs: %+v", got)
	}
	height, heightErr := rpc.ParseHexQuantity(string(*got[0].BlockNumber))
	if heightErr != nil || height != 16 {
		t.Fatalf("unexpected logs: %+v", got)
	}
}

// TestAdapterBlockAtBindsAnswersToTheRequestedReference pins the adapter
// half of the request-answer binding: a coherent foreign block (self-
// consistent payload, unanimous fleet) is rejected at the typed boundary
// for both the hash-addressed and the height-addressed forms.
func TestTypedBlockAtBindsAnswersToTheRequestedReference(t *testing.T) {
	t.Run("foreign block by hash", func(t *testing.T) {
		adapter, nodes := fleet(t, 3)
		for _, node := range nodes {
			node.Return("eth_getBlockByHash", fixtureBlock(t, 5))
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		requested := ethapi.Hash("0x1111111111111111111111111111111111111111111111111111111111111111")
		_, _, err := adapter.BlockByHash(ctx, requested)
		if !errors.Is(err, ethguard.ErrMalformedResponse) {
			t.Fatalf("expected malformed-response binding failure, got %v", err)
		}
	})

	t.Run("foreign block by height", func(t *testing.T) {
		adapter, nodes := fleet(t, 3)
		for _, node := range nodes {
			node.Return("eth_getBlockByNumber", fixtureBlock(t, 7))
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, _, err := adapter.BlockByNumber(ctx, big.NewInt(5))
		if !errors.Is(err, ethguard.ErrMalformedResponse) {
			t.Fatalf("expected malformed-response binding failure, got %v", err)
		}
	})
}

func TestReceiptNullLogReturnsMalformed(t *testing.T) {
	for _, batch := range []bool{false, true} {
		t.Run(fmt.Sprint(batch), func(t *testing.T) {
			client, nodes := fleet(t, 2)
			hash := ethapi.Hash("0x" + strings.Repeat("0", 62) + "42")
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
				_, _, err = client.BlockReceipts(context.Background(), numberRef(t, 16))
			} else {
				_, _, err = client.TransactionReceipt(context.Background(), hash)
			}
			if !errors.Is(err, ethguard.ErrMalformedResponse) {
				t.Fatalf("expected malformed null log, got %v", err)
			}
		})
	}
}

func TestTypedBlockByNumberRejectsOverflowBeforeDispatch(t *testing.T) {
	client, nodes := fleet(t, 2)
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockByNumber, fixtureBlock(t, 16))
	}
	number := new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 64), big.NewInt(16))
	if _, _, err := client.BlockByNumber(context.Background(), number); !errors.Is(err, ethguard.ErrInvalidRequest) {
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

func TestTypedRequiresCorroborationWithoutChangingMethodSignatures(t *testing.T) {
	client, nodes := fleet(t, 2)
	for _, node := range nodes {
		node.Return(methods.MethodGetBlockByNumber, map[string]any{"number": "0x10", "hash": "0xabc"})
	}
	nodes[0].Return(methods.MethodGetBalance, "0x1")
	nodes[1].Fail(methods.MethodGetBalance, -32603, "unavailable")
	if value, _, err := client.BalanceAt(context.Background(), ethapi.Address("0x0000000000000000000000000000000000000000"), nil); !errors.Is(err, ethguard.ErrInsufficientSupport) || value != nil {
		t.Fatalf("typed read hid unmet corroboration: %v %v", value, err)
	}
	// The advanced API reports the unmet majority as a conflict with the
	// surviving cluster in evidence (decision 0029): a 1-of-2 answer is no
	// longer surfaced as Selected under the default support floor.
	d, err := callDecision(client.Call(context.Background(), methods.MethodGetBalance, ethapi.Address("0x0000000000000000000000000000000000000000"), "0x10"))
	if !errors.Is(err, ethguard.ErrInsufficientSupport) {
		t.Fatalf("advanced call hid the unmet majority: %v", err)
	}
	// The public value is cleared on the aggregation failure (decision 0066);
	// the surviving cluster stays visible through Support and the source
	// verdict tables.
	if len(d.Support) != 1 || d.Value != nil {
		t.Fatalf("expected the surviving cluster in sources with a cleared public value, got %+v", d)
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
		// The geth payload projection spells absent members as null; the
		// official information object requires the inclusion context and a
		// sender, so complete the official shape before decoding.
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			t.Fatal(err)
		}
		if value, ok := obj["gasPrice"]; ok && string(value) == "null" {
			obj["gasPrice"] = json.RawMessage(`"0x1"`)
		}
		obj["from"] = json.RawMessage(`"` + crypto.PubkeyToAddress(key.PublicKey).Hex() + `"`)
		obj["blockHash"] = json.RawMessage(`"0x` + strings.Repeat("33", 32) + `"`)
		obj["blockNumber"] = json.RawMessage(`"0x10"`)
		obj["blockTimestamp"] = json.RawMessage(`"0x5"`)
		obj["transactionIndex"] = json.RawMessage(`"0x0"`)
		official, err := json.Marshal(obj)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := ethguard.DecodeTransaction(official); err != nil {
			t.Fatalf("valid parity-zero transaction rejected: %v", err)
		}
		return
	}
	t.Fatal("fixture did not produce parity zero")
}
