package ethguard_test

import (
	"context"
	"errors"
	"math/big"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/codermaybe/ethguard/rpctest"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	gethethclient "github.com/ethereum/go-ethereum/ethclient"
	gethrpc "github.com/ethereum/go-ethereum/rpc"
)

// A migrated caller uses generated call inputs. The independent signature
// matrix below checks every other parameter and result during migration.
var _ interface {
	CallContract(context.Context, ethapi.CallMsg, *big.Int) ([]byte, error)
	CodeAt(context.Context, common.Address, *big.Int) ([]byte, error)
} = (*ethguard.EthClient)(nil)

// knownMethodSetGap is the ratchet over the locked target: the default build
// tolerates exactly this set of not-yet-implemented business methods, and any
// additional gap fails. The gap must close through a real implementation; the
// tolerance below never becomes a release pass — release_check sets
// ETHGUARD_REQUIRE_COMPLETE=1, which requires a zero-gap method set.
var knownMethodSetGap = map[string]bool{
	"SimulateV1": true, // opt-in adapter only; independent design brief pending
}

// TestEthClientMethodSetIsExact compares the view with the locked
// go-ethereum ethclient.Client method set, excluding Client(), which is a
// concrete accessor to the underlying *rpc.Client and is outside EthGuard's
// business facade. The default build allows only the known gap ratchet
// (knownMethodSetGap); any other missing method fails here. Release checking
// sets ETHGUARD_REQUIRE_COMPLETE=1 to require the complete zero-gap set.
// Methods outside the target always fail.
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
	// The ratchet: the known gap stays visible, and anything beyond it fails
	// the default build. The release gate still requires zero gaps.
	businessImplemented := len(got) + len(missing)
	if businessImplemented != len(want) {
		t.Fatalf("method accounting = %d implemented + %d missing, want %d business methods",
			len(got), len(missing), len(want))
	}
	var unexpected []string
	for _, name := range missing {
		if !knownMethodSetGap[name] {
			unexpected = append(unexpected, name)
		}
	}
	if len(unexpected) > 0 {
		t.Fatalf("missing locked go-ethereum business methods beyond the known gap ratchet: %v", unexpected)
	}
	if len(missing) > 0 {
		t.Logf("locked go-ethereum business methods not yet exposed (%d): %v", len(missing), missing)
		if os.Getenv("ETHGUARD_REQUIRE_COMPLETE") == "1" {
			t.Fatalf("missing locked go-ethereum business methods: %v", missing)
		}
	}
	if len(got) > len(want) {
		t.Fatalf("method count = %d exceeds locked business target %d", len(got), len(want))
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
	if _, err := view.BalanceAt(ctx, common.HexToAddress("0xdad"), nil); err != nil {
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
	hash := common.HexToHash("0x" + strings.Repeat("42", 32))

	rootReceipt, _, rootReceiptErr := client.TransactionReceipt(ctx, hash)
	viewReceipt, viewReceiptErr := client.EthClient().TransactionReceipt(ctx, hash)
	if rootReceipt != nil || viewReceipt != nil || !errors.Is(rootReceiptErr, ethereum.NotFound) || !errors.Is(viewReceiptErr, ethereum.NotFound) {
		t.Fatalf("receipt root/view = %v/%v, errors = %v/%v", rootReceipt, viewReceipt, rootReceiptErr, viewReceiptErr)
	}

	rootReceipts, _, rootBlockErr := client.BlockReceipts(ctx, gethrpc.BlockNumberOrHashWithHash(hash, false))
	viewReceipts, viewBlockErr := client.EthClient().BlockReceipts(ctx, gethrpc.BlockNumberOrHashWithHash(hash, false))
	if rootReceipts != nil || viewReceipts != nil || !errors.Is(rootBlockErr, ethereum.NotFound) || !errors.Is(viewBlockErr, ethereum.NotFound) {
		t.Fatalf("block receipts root/view = %v/%v, errors = %v/%v", rootReceipts, viewReceipts, rootBlockErr, viewBlockErr)
	}
}

// TestEthClientBusinessSignatures checks complete parameter and result types.
// Migrated methods use explicit project-generated signatures; the locked geth
// signatures remain the temporary contract only for unmigrated methods. The
// independent full method-set ratchet continues to expose SimulateV1.
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
		"CallContract":           reflect.TypeOf((func(context.Context, ethapi.CallMsg, *big.Int) ([]byte, error))(nil)),
		"CallContractAtHash":     reflect.TypeOf((func(context.Context, ethapi.CallMsg, common.Hash) ([]byte, error))(nil)),
		"PendingCallContract":    reflect.TypeOf((func(context.Context, ethapi.CallMsg) ([]byte, error))(nil)),
		"EstimateGas":            reflect.TypeOf((func(context.Context, ethapi.CallMsg) (uint64, error))(nil)),
		"EstimateGasAtBlock":     reflect.TypeOf((func(context.Context, ethapi.CallMsg, *big.Int) (uint64, error))(nil)),
		"EstimateGasAtBlockHash": reflect.TypeOf((func(context.Context, ethapi.CallMsg, common.Hash) (uint64, error))(nil)),
		"SyncProgress":           reflect.TypeOf((func(context.Context) (*ethapi.SyncProgress, error))(nil)),
		"FeeHistory":             reflect.TypeOf((func(context.Context, uint64, *big.Int, []float64) (*ethapi.FeeHistoryResult, error))(nil)),
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
