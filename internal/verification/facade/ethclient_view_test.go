package ethguard_test

import (
	"context"
	"errors"
	"math/big"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	gethethclient "github.com/ethereum/go-ethereum/ethclient"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/codermaybe/ethguard/rpctest"
)

// The interface commitments of the ethclient-shaped view after the complete
// generated-model migration: every scalar entry takes the generated
// ethapi.Address/ethapi.Hash, the object entries return the generated
// models, and the call-family surface stays over the project's generated
// ethapi.CallMsg. The migration intentionally does NOT satisfy bind/v2's
// DeployBackend or ContractCaller: DeployBackend wants
// CodeAt(common.Address)/TransactionReceipt(common.Hash) and ContractCaller
// wants CallContract(ethereum.CallMsg); dropping those commitments was the
// announced compat cost of adopting the generated scalar types, and a
// bind/v2 caller needs an explicit adapter design item, not a silent
// signature drift. The
// assertions below pin the full migrated surface so a further change cannot
// slip through unnoticed.
var (
	_ interface {
		// scalar reads: generated Address/Hash in, unchanged scalar out
		BalanceAt(context.Context, ethapi.Address, *big.Int) (*big.Int, error)
		BalanceAtHash(context.Context, ethapi.Address, ethapi.Hash) (*big.Int, error)
		NonceAt(context.Context, ethapi.Address, *big.Int) (uint64, error)
		NonceAtHash(context.Context, ethapi.Address, ethapi.Hash) (uint64, error)
		StorageAt(context.Context, ethapi.Address, ethapi.Hash, *big.Int) ([]byte, error)
		StorageAtHash(context.Context, ethapi.Address, ethapi.Hash, ethapi.Hash) ([]byte, error)
		CodeAt(context.Context, ethapi.Address, *big.Int) ([]byte, error)
		CodeAtHash(context.Context, ethapi.Address, ethapi.Hash) ([]byte, error)
		PendingBalanceAt(context.Context, ethapi.Address) (*big.Int, error)
		PendingStorageAt(context.Context, ethapi.Address, ethapi.Hash) ([]byte, error)
		PendingCodeAt(context.Context, ethapi.Address) ([]byte, error)
		PendingNonceAt(context.Context, ethapi.Address) (uint64, error)
		TransactionCount(context.Context, ethapi.Hash) (uint, error)
		// objects: generated models out
		BlockByHash(context.Context, ethapi.Hash) (*ethapi.Block, error)
		BlockByNumber(context.Context, *big.Int) (*ethapi.Block, error)
		HeaderByHash(context.Context, ethapi.Hash) (*ethapi.Header, error)
		HeaderByNumber(context.Context, *big.Int) (*ethapi.Header, error)
		BlockReceipts(context.Context, ethapi.BlockReference) ([]ethapi.Receipt, error)
		TransactionReceipt(context.Context, ethapi.Hash) (*ethapi.Receipt, error)
		TransactionByHash(context.Context, ethapi.Hash) (*ethapi.Transaction, bool, error)
		TransactionInBlock(context.Context, ethapi.Hash, uint) (*ethapi.Transaction, error)
		TransactionSender(context.Context, *ethapi.Transaction, ethapi.Hash, uint) (ethapi.Address, error)
		// call family and variable results
		CallContract(context.Context, ethapi.CallMsg, *big.Int) ([]byte, error)
		CallContractAtHash(context.Context, ethapi.CallMsg, ethapi.Hash) ([]byte, error)
		PendingCallContract(context.Context, ethapi.CallMsg) ([]byte, error)
		EstimateGas(context.Context, ethapi.CallMsg) (uint64, error)
		EstimateGasAtBlock(context.Context, ethapi.CallMsg, *big.Int) (uint64, error)
		EstimateGasAtBlockHash(context.Context, ethapi.CallMsg, ethapi.Hash) (uint64, error)
		SyncProgress(context.Context) (*ethapi.SyncProgress, error)
		FeeHistory(context.Context, uint64, *big.Int, []float64) (*ethapi.FeeHistoryResult, error)
		FilterLogs(context.Context, ethapi.FilterQuery) ([]ethapi.Log, error)
		// subscriptions: generated event payloads
		SubscribeNewHead(context.Context, chan<- *ethapi.Header) (ethereum.Subscription, error)
		SubscribeFilterLogs(context.Context, ethapi.FilterQuery, chan<- ethapi.Log) (ethereum.Subscription, error)
		SubscribeTransactionReceipts(context.Context, *ethapi.TransactionReceiptsQuery, chan<- []ethapi.Receipt) (ethereum.Subscription, error)
		// writes: BinaryTransaction in, generated Receipt out
		SendTransaction(context.Context, ethguard.BinaryTransaction) error
		SendTransactionSync(context.Context, ethguard.BinaryTransaction, *time.Duration) (*ethapi.Receipt, error)
		SendRawTransactionSync(context.Context, []byte, *time.Duration) (*ethapi.Receipt, error)
	} = (*ethguard.EthClient)(nil)
)

// TestEthClientMethodSetIsExact requires the complete locked ethclient business
// method set. Client() exposes the upstream concrete transport and is excluded.
// Signature migration and per-method semantic acceptance are separate gates.
func TestEthClientMethodSetIsExact(t *testing.T) {
	want := make(map[string]bool)
	upstream := reflect.TypeOf((*gethethclient.Client)(nil))
	for i := 0; i < upstream.NumMethod(); i++ {
		name := upstream.Method(i).Name
		if name != "Client" {
			want[name] = true
		}
	}
	typ := reflect.TypeOf((*ethguard.EthClient)(nil))
	structType := typ.Elem()
	got := map[string]bool{}
	for i := 0; i < typ.NumMethod(); i++ {
		got[typ.Method(i).Name] = true
	}
	for name := range got {
		if !want[name] {
			t.Errorf("leaked view method %s", name)
		}
	}
	missing := make([]string, 0)
	for name := range want {
		if !got[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) != 0 {
		t.Fatalf("missing locked go-ethereum business methods: %v", missing)
	}
	if len(got) != len(want) {
		t.Fatalf("method count = %d, want locked business target %d", len(got), len(want))
	}
	// The owner field stays private: the type must not embed or expose it.
	if structType.NumField() != 1 || structType.Field(0).IsExported() || structType.Field(0).Name != "owner" {
		t.Fatalf("EthClient fields = %+v, want one unexported owner", structType.NumField())
	}
}

// TestViewMatchesNativeOnOneFixture is the A03 slice for the view: the same
// fixture produces the same business value and error class through both
// facades, and the view makes no extra provider contact beyond the native
// call's own trace.
func TestViewMatchesNativeOnOneFixture(t *testing.T) {
	client, nodes := anchoredFleet(t, 3)
	for _, node := range nodes {
		node.Return(methods.MethodGetBalance, "0x64")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	view := client.EthClient()
	nativeID, _, nativeErr := client.ChainID(ctx)
	viewID, viewErr := view.ChainID(ctx)
	if nativeErr != nil || viewErr != nil || nativeID.Cmp(viewID) != 0 {
		t.Fatalf("chain id native/view = %v/%v, %v/%v", nativeID, viewID, nativeErr, viewErr)
	}

	before := make([]int, len(nodes))
	for i, node := range nodes {
		before[i] = len(node.Calls())
	}
	if _, err := view.BalanceAt(ctx, ethapi.Address("0x0000000000000000000000000000000000000dad"), nil); err != nil {
		t.Fatal(err)
	}
	after := make([]int, len(nodes))
	for i, node := range nodes {
		after[i] = len(node.Calls())
	}
	// The view's one read = the native read's trace: the anchor round and the
	// business round, one request per member each — nothing more.
	traceDelta := 0
	for i := range nodes {
		traceDelta += after[i] - before[i]
	}
	if traceDelta != 2*len(nodes) {
		t.Fatalf("view read contacted %d endpoints, want exactly anchor+business per member", traceDelta)
	}
}

// TestViewCloseStopsTheOwner pins the shared lifecycle: one Close on either
// surface stops both, and a closed view rejects new work like the native.
func TestViewCloseStopsTheOwner(t *testing.T) {
	client, _ := anchoredFleet(t, 2)
	view := client.EthClient()
	view.Close()
	if _, _, err := client.AnchorBlockHash(context.Background()); err == nil {
		t.Fatal("the owner survived the view's Close")
	}
	if _, err := view.ChainID(context.Background()); err == nil {
		t.Fatal("the closed view served a read")
	}
	view.Close() // idempotent
}

// TestViewReadWithCaptureDeliversMaterial: a WithResult-bound view call
// collects and delivers the material the native result carries, with the
// same decision facts.
func TestViewReadWithCaptureDeliversMaterial(t *testing.T) {
	client, nodes := fleet(t, 3)
	for _, node := range nodes {
		node.Return(methods.MethodChainID, "0x1")
	}
	base, err := ethguard.WithSourceEvidence(context.Background(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	ctx, capture := ethguard.WithResult(base)

	view := client.EthClient()
	viewID, err := view.ChainID(ctx)
	if err != nil || viewID.Int64() != 1 {
		t.Fatalf("view read = %v, %v", viewID, err)
	}
	result, published, err := capture.Result()
	if !published || err != nil {
		t.Fatalf("delivery = %v, %v", published, err)
	}
	if !result.IsCorroborated() || result.Uncertain == nil || len(result.Uncertain.Raw) != 3 {
		t.Fatalf("view capture = %+v, material %+v", result.Decision, result.Uncertain)
	}
}

// TestViewReceiptAbsenceMatchesNative pins the root/view projection boundary
// for both receipt methods: a corroborated JSON null remains ethereum.NotFound
// and is never converted into a zero receipt or an empty receipt list.
func TestViewReceiptAbsenceMatchesNative(t *testing.T) {
	client, nodes := fleet(t, 3)
	for _, node := range nodes {
		node.Handle(methods.MethodGetTransactionReceipt, func(rpctest.Call) (any, error) { return nil, nil })
		node.Handle(methods.MethodGetBlockReceipts, func(rpctest.Call) (any, error) { return nil, nil })
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hash := ethapi.Hash("0x" + strings.Repeat("42", 32))

	rootReceipt, _, rootReceiptErr := client.TransactionReceipt(ctx, hash)
	viewReceipt, viewReceiptErr := client.EthClient().TransactionReceipt(ctx, hash)
	if rootReceipt != nil || viewReceipt != nil || !errors.Is(rootReceiptErr, ethereum.NotFound) || !errors.Is(viewReceiptErr, ethereum.NotFound) {
		t.Fatalf("receipt root/view = %v/%v, errors = %v/%v", rootReceipt, viewReceipt, rootReceiptErr, viewReceiptErr)
	}

	blockRef := ethapi.BlockReference{Eip1898BlockHashObject: &ethapi.BlockHashReference{BlockHash: hash}}
	rootReceipts, _, rootBlockErr := client.BlockReceipts(ctx, blockRef)
	viewReceipts, viewBlockErr := client.EthClient().BlockReceipts(ctx, blockRef)
	if rootReceipts != nil || viewReceipts != nil || !errors.Is(rootBlockErr, ethereum.NotFound) || !errors.Is(viewBlockErr, ethereum.NotFound) {
		t.Fatalf("block receipts root/view = %v/%v, errors = %v/%v", rootReceipts, viewReceipts, rootBlockErr, viewBlockErr)
	}
}

// TestViewFailureDeliversSameConclusionToCallerAndCapture pins the failed
// view read's dual delivery: the error the caller receives is the error the
// capture published. The view's dropped CallResult never carries a different
// conclusion than the one the caller saw.
func TestViewFailureDeliversSameConclusionToCallerAndCapture(t *testing.T) {
	client, nodes := fleet(t, 2)
	nodes[0].Return(methods.MethodChainID, "0x1")
	nodes[1].Return(methods.MethodChainID, "0x2") // no cluster reaches the floor
	captureCtx, capture := ethguard.WithResult(context.Background())
	ctx, cancel := context.WithTimeout(captureCtx, 5*time.Second)
	defer cancel()

	id, err := client.EthClient().ChainID(ctx)
	if err == nil || id != nil || !errors.Is(err, ethguard.ErrInsufficientSupport) {
		t.Fatalf("failed view read = %v, %v", id, err)
	}
	published, ok, capErr := capture.Result()
	if !ok || capErr != nil {
		t.Fatalf("delivery = %v, %v", ok, capErr)
	}
	if published.Err == nil || !errors.Is(published.Err, ethguard.ErrInsufficientSupport) || published.Err != err {
		t.Fatalf("published conclusion = %v, caller conclusion = %v", published.Err, err)
	}
}

// TestEthClientBusinessSignatures checks complete parameter and result types.
// Migrated methods use explicit project-generated signatures; the locked geth
// signatures remain the temporary contract only for unmigrated methods. The
// independent full method-set gate requires every business entry.
func TestEthClientBusinessSignatures(t *testing.T) {
	upstream := reflect.TypeOf((*gethethclient.Client)(nil))
	view := reflect.TypeOf((*ethguard.EthClient)(nil))
	upstreamMethods := make(map[string]reflect.Method)
	for i := 0; i < upstream.NumMethod(); i++ {
		m := upstream.Method(i)
		upstreamMethods[m.Name] = m
	}
	viewMethods := make(map[string]reflect.Method)
	for i := 0; i < view.NumMethod(); i++ {
		m := view.Method(i)
		viewMethods[m.Name] = m
	}
	signatureOf := func(sig reflect.Type, first int) string {
		var parts []string
		for i := first; i < sig.NumIn(); i++ {
			parts = append(parts, sig.In(i).String())
		}
		returns := make([]string, 0, sig.NumOut())
		for i := 0; i < sig.NumOut(); i++ {
			returns = append(returns, sig.Out(i).String())
		}
		return "(" + strings.Join(parts, ", ") + ") -> (" + strings.Join(returns, ", ") + ")"
	}
	sameSignature := func(actual, expected reflect.Type, expectedOffset int) bool {
		if actual.NumIn()-1 != expected.NumIn()-expectedOffset || actual.NumOut() != expected.NumOut() || actual.IsVariadic() != expected.IsVariadic() {
			return false
		}
		for i := 1; i < actual.NumIn(); i++ {
			if actual.In(i) != expected.In(i-1+expectedOffset) {
				return false
			}
		}
		for i := 0; i < actual.NumOut(); i++ {
			if actual.Out(i) != expected.Out(i) {
				return false
			}
		}
		return true
	}
	migrated := map[string]reflect.Type{
		// scalars
		"BalanceAt":        reflect.TypeOf((func(context.Context, ethapi.Address, *big.Int) (*big.Int, error))(nil)),
		"BalanceAtHash":    reflect.TypeOf((func(context.Context, ethapi.Address, ethapi.Hash) (*big.Int, error))(nil)),
		"NonceAt":          reflect.TypeOf((func(context.Context, ethapi.Address, *big.Int) (uint64, error))(nil)),
		"NonceAtHash":      reflect.TypeOf((func(context.Context, ethapi.Address, ethapi.Hash) (uint64, error))(nil)),
		"StorageAt":        reflect.TypeOf((func(context.Context, ethapi.Address, ethapi.Hash, *big.Int) ([]byte, error))(nil)),
		"StorageAtHash":    reflect.TypeOf((func(context.Context, ethapi.Address, ethapi.Hash, ethapi.Hash) ([]byte, error))(nil)),
		"CodeAt":           reflect.TypeOf((func(context.Context, ethapi.Address, *big.Int) ([]byte, error))(nil)),
		"CodeAtHash":       reflect.TypeOf((func(context.Context, ethapi.Address, ethapi.Hash) ([]byte, error))(nil)),
		"PendingBalanceAt": reflect.TypeOf((func(context.Context, ethapi.Address) (*big.Int, error))(nil)),
		"PendingStorageAt": reflect.TypeOf((func(context.Context, ethapi.Address, ethapi.Hash) ([]byte, error))(nil)),
		"PendingCodeAt":    reflect.TypeOf((func(context.Context, ethapi.Address) ([]byte, error))(nil)),
		"PendingNonceAt":   reflect.TypeOf((func(context.Context, ethapi.Address) (uint64, error))(nil)),
		"TransactionCount": reflect.TypeOf((func(context.Context, ethapi.Hash) (uint, error))(nil)),
		// objects
		"BlockByHash":        reflect.TypeOf((func(context.Context, ethapi.Hash) (*ethapi.Block, error))(nil)),
		"BlockByNumber":      reflect.TypeOf((func(context.Context, *big.Int) (*ethapi.Block, error))(nil)),
		"HeaderByHash":       reflect.TypeOf((func(context.Context, ethapi.Hash) (*ethapi.Header, error))(nil)),
		"HeaderByNumber":     reflect.TypeOf((func(context.Context, *big.Int) (*ethapi.Header, error))(nil)),
		"BlockReceipts":      reflect.TypeOf((func(context.Context, ethapi.BlockReference) ([]ethapi.Receipt, error))(nil)),
		"TransactionReceipt": reflect.TypeOf((func(context.Context, ethapi.Hash) (*ethapi.Receipt, error))(nil)),
		"TransactionByHash":  reflect.TypeOf((func(context.Context, ethapi.Hash) (*ethapi.Transaction, bool, error))(nil)),
		"TransactionInBlock": reflect.TypeOf((func(context.Context, ethapi.Hash, uint) (*ethapi.Transaction, error))(nil)),
		"TransactionSender":  reflect.TypeOf((func(context.Context, *ethapi.Transaction, ethapi.Hash, uint) (ethapi.Address, error))(nil)),
		// call family and variable results
		"SimulateV1":             reflect.TypeOf((func(context.Context, ethapi.SimulateV1Request, *ethapi.SimulateBlockReference) ([]ethapi.SimulateBlock, error))(nil)),
		"CallContract":           reflect.TypeOf((func(context.Context, ethapi.CallMsg, *big.Int) ([]byte, error))(nil)),
		"CallContractAtHash":     reflect.TypeOf((func(context.Context, ethapi.CallMsg, ethapi.Hash) ([]byte, error))(nil)),
		"PendingCallContract":    reflect.TypeOf((func(context.Context, ethapi.CallMsg) ([]byte, error))(nil)),
		"EstimateGas":            reflect.TypeOf((func(context.Context, ethapi.CallMsg) (uint64, error))(nil)),
		"EstimateGasAtBlock":     reflect.TypeOf((func(context.Context, ethapi.CallMsg, *big.Int) (uint64, error))(nil)),
		"EstimateGasAtBlockHash": reflect.TypeOf((func(context.Context, ethapi.CallMsg, ethapi.Hash) (uint64, error))(nil)),
		"SyncProgress":           reflect.TypeOf((func(context.Context) (*ethapi.SyncProgress, error))(nil)),
		"FeeHistory":             reflect.TypeOf((func(context.Context, uint64, *big.Int, []float64) (*ethapi.FeeHistoryResult, error))(nil)),
		"FilterLogs":             reflect.TypeOf((func(context.Context, ethapi.FilterQuery) ([]ethapi.Log, error))(nil)),
		// subscriptions
		"SubscribeNewHead":             reflect.TypeOf((func(context.Context, chan<- *ethapi.Header) (ethereum.Subscription, error))(nil)),
		"SubscribeFilterLogs":          reflect.TypeOf((func(context.Context, ethapi.FilterQuery, chan<- ethapi.Log) (ethereum.Subscription, error))(nil)),
		"SubscribeTransactionReceipts": reflect.TypeOf((func(context.Context, *ethapi.TransactionReceiptsQuery, chan<- []ethapi.Receipt) (ethereum.Subscription, error))(nil)),
		// writes
		"SendTransaction":        reflect.TypeOf((func(context.Context, ethguard.BinaryTransaction) error)(nil)),
		"SendTransactionSync":    reflect.TypeOf((func(context.Context, ethguard.BinaryTransaction, *time.Duration) (*ethapi.Receipt, error))(nil)),
		"SendRawTransactionSync": reflect.TypeOf((func(context.Context, []byte, *time.Duration) (*ethapi.Receipt, error))(nil)),
	}
	for name, viewMethod := range viewMethods {
		upstreamMethod, ok := upstreamMethods[name]
		if !ok {
			continue // leaked-method detection owns this case
		}
		expected, offset := upstreamMethod.Type, 1
		if signature, migrated := migrated[name]; migrated {
			expected, offset = signature, 0
		}
		if !sameSignature(viewMethod.Type, expected, offset) {
			t.Errorf("%s signature = %s, want %s", name, signatureOf(viewMethod.Type, 1), signatureOf(expected, offset))
		}
	}
	for name := range migrated {
		if _, exists := viewMethods[name]; !exists {
			t.Errorf("migrated signature %s has no public implementation", name)
		}
	}
	if t.Failed() {
		t.Fatal("business method signatures drifted from their declared contracts")
	}
}
