package ethguard_test

import (
	"context"
	"errors"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/codermaybe/ethguard/rpctest"
	"github.com/ethereum/go-ethereum"
	bind "github.com/ethereum/go-ethereum/accounts/abi/bind/v2"
	"github.com/ethereum/go-ethereum/common"
	gethethclient "github.com/ethereum/go-ethereum/ethclient"
	gethrpc "github.com/ethereum/go-ethereum/rpc"
)

// The interface commitments of the ethclient-shaped view include bind/v2's
// ContractCaller and DeployBackend. The complete method-set check below is
// generated from the locked go-ethereum type, so new upstream methods cannot
// silently disappear from the compatibility target.
var (
	_ bind.ContractCaller = (*ethguard.EthClient)(nil)
	_ bind.DeployBackend  = (*ethguard.EthClient)(nil)
)

// TestEthClientMethodSetMatchesLockedTarget compares the view with the locked
// go-ethereum ethclient.Client method set, excluding Client(), which is a
// concrete accessor to the underlying *rpc.Client and is outside EthGuard's
// business facade. Missing methods are logged while the implementation is in
// progress; release_check can set ETHGUARD_REQUIRE_COMPLETE=1 to turn those
// gaps into a hard acceptance failure. Methods outside the target always fail.
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
