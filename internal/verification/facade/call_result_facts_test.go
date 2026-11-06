package ethguard_test

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
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/codermaybe/ethguard/rpctest"
)

// The migrated scalar/state group's result-contract tests: CallResult.Err
// equals the trailing error, aggregation failures clear the public value but
// keep the formed facts, the variable reads stay Selected, the single-source
// mode stays Forwarded, local rejections carry no decision, and configured
// evidence reaches the result even when every response was empty.

func TestCallResultErrMatchesTrailingErrorOnSuccess(t *testing.T) {
	client, _ := fleet(t, 3)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	id, result, err := client.ChainID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Err != nil || result.Err != err {
		t.Fatalf("result.Err = %v, trailing = %v", result.Err, err)
	}
	if !result.IsCorroborated() || id == nil {
		t.Fatalf("corroborated read = %+v", result)
	}
	if result.Decision == nil || result.WriteDecision != nil {
		t.Fatalf("read result decision split wrong: %+v", result)
	}
}

func TestCallResultAggregationFailureClearsValueKeepsFacts(t *testing.T) {
	client, nodes := fleet(t, 2)
	nodes[0].Return(methods.MethodChainID, "0x1")
	nodes[1].Return(methods.MethodChainID, "0x2") // no cluster reaches the floor

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	id, result, err := client.ChainID(ctx)
	if err == nil || id != nil {
		t.Fatalf("conflicting fleet = %v, %v", id, err)
	}
	if result.Err != err {
		t.Fatalf("result.Err = %v, trailing = %v", result.Err, err)
	}
	if result.IsCorroborated() {
		t.Fatal("a failed aggregation must not claim corroboration")
	}
	if result.Decision == nil {
		t.Fatal("the formed facts vanished: decision is nil")
	}
	if result.Decision.Value != nil {
		t.Fatalf("public value = %s, want cleared on aggregation failure", result.Decision.Value)
	}
	if len(result.Decision.Support) == 0 {
		t.Fatal("support facts were erased with the value")
	}
}

func TestCallResultSelectionAndForwardedSemanticsPreserved(t *testing.T) {
	// Median read with an outlier: Selected, never Corroborated, no error.
	client, nodes := fleet(t, 3)
	nodes[0].Return("eth_gasPrice", "0x3b9aca00")
	nodes[1].Return("eth_gasPrice", "0x3b9aca00")
	nodes[2].Return("eth_gasPrice", "0x77359400")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	price, result, err := client.SuggestGasPrice(ctx)
	if err != nil || price.Int64() != 1_000_000_000 {
		t.Fatalf("median price = %v, %v", price, err)
	}
	if result.IsCorroborated() || result.Decision.Status.String() != "selected" {
		t.Fatalf("variable read status = %+v, want Selected", result.Decision)
	}

	// Explicit single-source mode: Forwarded, value delivered, no
	// corroboration claim.
	single, err := ethguard.DialUncorroborated(nodes[0].URL())
	if err != nil {
		t.Fatal(err)
	}
	defer single.Close()
	fwd, fwdResult, err := single.SuggestGasPrice(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fwd.Int64() != 1_000_000_000 {
		t.Fatalf("forwarded price = %v", fwd)
	}
	if fwdResult.IsCorroborated() || fwdResult.Decision.Status.String() != "forwarded" {
		t.Fatalf("single-source status = %+v, want Forwarded", fwdResult.Decision)
	}
}

func TestCallResultLocalRejectionCarriesNoDecision(t *testing.T) {
	client, _ := fleet(t, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	balance, result, err := client.BalanceAt(ctx, [20]byte{}, big.NewInt(-99))
	if err == nil || balance != nil {
		t.Fatalf("invalid block arg = %v, %v", balance, err)
	}
	if !errors.Is(err, ethguard.ErrInvalidRequest) {
		t.Fatalf("error = %v, want ErrInvalidRequest", err)
	}
	if result.Err != err {
		t.Fatalf("result.Err = %v, trailing = %v", result.Err, err)
	}
	if result.Decision != nil {
		t.Fatalf("pre-adjudication rejection carries a decision: %+v", result.Decision)
	}
	if result.IsCorroborated() || result.IsWriteAccepted() {
		t.Fatal("zero claims on a local rejection")
	}
}

func TestCallResultDeliversMaterialEvenWhenEmpty(t *testing.T) {
	client, _ := fleet(t, 2)
	base, err := ethguard.WithSourceEvidence(context.Background(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	ctx, capture := ethguard.WithResult(base)

	// A forwarded single-source read records its raw response; a corroborated
	// one does too. The zero-response case is checked by the collector's own
	// suite; here the delivered snapshot must be non-nil once enabled.
	if _, _, err := client.ChainID(ctx); err != nil {
		t.Fatal(err)
	}
	result, published, err := capture.Result()
	if !published || err != nil {
		t.Fatalf("delivery = %v, %v", published, err)
	}
	if result.Uncertain == nil {
		t.Fatal("configured evidence was not delivered")
	}
	if result.Uncertain.Limit != 1<<20 {
		t.Fatalf("budget = %d", result.Uncertain.Limit)
	}
	if len(result.Uncertain.Raw) == 0 {
		t.Fatal("raw responses were not retained")
	}
}

// --- the block / receipt / anchor group's result-contract facts ---

func TestCallResultCorroboratedReceiptNullKeepsValueAndSupport(t *testing.T) {
	client, nodes := fleet(t, 3)
	for _, node := range nodes {
		node.Return(methods.MethodGetTransactionReceipt, nil) // corroborated JSON null
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	receipt, result, err := client.TransactionReceipt(ctx, common.HexToHash("0x"+strings.Repeat("42", 32)))
	if !errors.Is(err, ethereum.NotFound) || receipt != nil {
		t.Fatalf("null receipt = %+v, %v", receipt, err)
	}
	// The two-layer split: the corroborated null is a formed observation.
	if !result.IsCorroborated() {
		t.Fatalf("corroborated null status = %+v", result.Decision)
	}
	if result.Err != err || result.Err == nil {
		t.Fatalf("result.Err = %v, trailing = %v", result.Err, err)
	}
	if result.Decision.Value == nil || string(result.Decision.Value) != "null" {
		t.Fatalf("null payload = %s, want the corroborated JSON null preserved", result.Decision.Value)
	}
	if len(result.Decision.Support) == 0 {
		t.Fatal("support facts were erased with the null receipt")
	}
}

func TestCallResultAnchorCarriesEstablishmentEvidence(t *testing.T) {
	client, nodes := anchoredFleet(t, 3)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	hash, result, err := client.AnchorBlockHash(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsCorroborated() || result.Decision.Reference == nil || result.Decision.Reference.Hash != hash {
		t.Fatalf("anchor result = %+v, hash %s", result.Decision, hash)
	}
	if result.Decision.Value != nil {
		t.Fatalf("the anchor decision claims a value: %s", result.Decision.Value)
	}
	if len(result.Decision.Reference.Support) == 0 || len(nodes) != 3 {
		t.Fatalf("reference support = %+v", result.Decision.Reference.Support)
	}
}

func TestCallResultAnchorFailureKeepsEstablishmentEvidence(t *testing.T) {
	client, nodes := fleet(t, 2)
	for _, node := range nodes {
		node.Fail(methods.MethodGetBlockByNumber, -32602, "no head here")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	hash, result, err := client.AnchorBlockHash(ctx)
	if err == nil || hash != "" {
		t.Fatalf("failed anchor = %q, %v", hash, err)
	}
	if result.IsCorroborated() {
		t.Fatal("a failed establishment must not claim corroboration")
	}
	if result.Decision == nil || result.Decision.Reference == nil {
		t.Fatal("the establishment evidence vanished")
	}
	if result.Decision.Reference.Status.String() != "unknown" || len(result.Decision.Reference.Faults) == 0 {
		t.Fatalf("reference evidence = %+v, want unknown status with faults", result.Decision.Reference)
	}
	if result.Err != err {
		t.Fatalf("result.Err = %v, trailing = %v", result.Err, err)
	}
}

// callDecision adapts a Call outcome to the decision-only shape the older
// generic-JSON assertions read. A locally rejected call carries no decision;
// the error still travels.
func callDecision(result ethguard.CallResult, err error) (rpc.Decision, error) {
	if result.Decision == nil {
		return rpc.Decision{}, err
	}
	return *result.Decision, err
}

// --- the write group's result-contract facts ---

func TestCallResultWriteDecisionReachesTheCapture(t *testing.T) {
	client, nodes := fleet(t, 3)
	raw, hashHex := unifiedSignedTransaction(t)
	for _, node := range nodes {
		node.Return(methods.MethodSendRawTransaction, hashHex)
	}
	base, err := ethguard.WithSourceEvidence(context.Background(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	ctx, capture := ethguard.WithResult(base)

	tx := new(types.Transaction)
	if err := tx.UnmarshalBinary(raw); err != nil {
		t.Fatal(err)
	}
	result, err := client.SendTransaction(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsWriteAccepted() || result.WriteDecision.Hash != strings.ToLower(hashHex) {
		t.Fatalf("write result = %+v", result.WriteDecision)
	}
	if result.WriteDecision.Basis != 3 {
		t.Fatalf("basis = %d, want the fixed 3-member basis", result.WriteDecision.Basis)
	}
	if result.Decision != nil {
		t.Fatal("a write result carries no read decision")
	}
	// The capture received the same decision plus the broadcast material.
	published, ok, err := capture.Result()
	if !ok || err != nil {
		t.Fatalf("delivery = %v, %v", ok, err)
	}
	if !published.IsWriteAccepted() || published.WriteDecision.Hash != result.WriteDecision.Hash {
		t.Fatalf("published decision = %+v", published.WriteDecision)
	}
	if published.Uncertain == nil || len(published.Uncertain.Raw) == 0 {
		t.Fatalf("broadcast material = %+v", published.Uncertain)
	}
}

func TestCallResultNilTransactionRejectedLocally(t *testing.T) {
	client, nodes := fleet(t, 2)
	base, err := ethguard.WithSourceEvidence(context.Background(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	ctx, capture := ethguard.WithResult(base)

	result, err := client.SendTransaction(ctx, nil)
	if !errors.Is(err, ethguard.ErrInvalidRequest) {
		t.Fatalf("nil tx = %v, want ErrInvalidRequest", err)
	}
	if result.WriteDecision != nil || result.Err != err {
		t.Fatalf("local rejection result = %+v", result)
	}
	published, ok, _ := capture.Result()
	if !ok || !errors.Is(published.Err, ethguard.ErrInvalidRequest) {
		t.Fatalf("published = %+v, %v", published, ok)
	}
	for _, node := range nodes {
		for _, call := range node.Calls() {
			if call.Method != "eth_chainId" {
				t.Errorf("rejected write contacted %s", call.Method)
			}
		}
	}
}

// --- composite-read reference retention (A05 regression) ---

// The anchor a composite read established stays on the decision when the
// business round later fails, and the top-level decision keeps stating the
// business phase: the reference's supporters never join the business tables.
func TestCallResultCompositeReadKeepsEstablishedReferenceOnBusinessFailure(t *testing.T) {
	client, nodes := anchoredFleet(t, 2)
	for _, node := range nodes {
		node.Fail(methods.MethodGetBlockByHash, -32603, "business unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	block, result, err := client.BlockByNumber(ctx, nil)
	if err == nil || block != nil {
		t.Fatalf("business failure = %+v, %v", block, err)
	}
	if result.Decision == nil || result.Decision.Reference == nil {
		t.Fatal("the successfully established reference disappeared on business failure")
	}
	if result.Decision.Reference.Status.String() != "corroborated" || len(result.Decision.Reference.Support) != 2 {
		t.Fatalf("reference evidence = %+v, want the corroborated establishment", result.Decision.Reference)
	}
	if result.Decision.Status != rpc.Unknown {
		t.Fatalf("top-level status = %s, want the business phase's unknown", result.Decision.Status)
	}
	if len(result.Decision.Support) != 0 {
		t.Fatalf("reference supporters leaked into the business tables: %+v", result.Decision.Support)
	}

	// The negative-tag composite path retains the same evidence.
	if _, tagResult, tagErr := client.BlockByNumber(ctx, big.NewInt(-2)); tagErr != nil &&
		(tagResult.Decision == nil || tagResult.Decision.Reference == nil) {
		t.Fatalf("negative-tag composite lost the reference: %+v", tagResult.Decision)
	}

	// BlockNumber rides the same composition.
	if _, heightResult, heightErr := client.BlockNumber(ctx); heightErr != nil &&
		(heightResult.Decision == nil || heightResult.Decision.Reference == nil) {
		t.Fatalf("BlockNumber composite lost the reference: %+v", heightResult.Decision)
	}
}

// The success path carries the established reference too: the read's
// prerequisite stays visible alongside the business answer.
func TestCallResultCompositeReadCarriesReferenceOnSuccess(t *testing.T) {
	client, nodes := fleet(t, 3)
	// One coherent head: the establishment answer and the hash-addressed
	// business fetch agree on the same self-consistent block.
	head := fixtureBlock(t, 16)
	for _, node := range nodes {
		node.Handle(methods.MethodGetBlockByNumber, func(c rpctest.Call) (any, error) {
			var ref string
			if len(c.Params) > 0 {
				_ = json.Unmarshal(c.Params[0], &ref)
			}
			var full bool
			if len(c.Params) > 1 {
				_ = json.Unmarshal(c.Params[1], &full)
			}
			if full || ref != "latest" {
				return nil, rpctest.RPCError(-32602, "unexpected probe "+ref)
			}
			return map[string]any{"number": "0x10", "hash": blockHashOf(head)}, nil
		})
		node.Handle(methods.MethodGetBlockByHash, func(c rpctest.Call) (any, error) {
			var hash string
			if len(c.Params) > 0 {
				_ = json.Unmarshal(c.Params[0], &hash)
			}
			if hash == blockHashOf(head) {
				return json.RawMessage(head), nil
			}
			return nil, rpctest.RPCError(-32602, "unknown hash "+hash)
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	block, result, err := client.BlockByNumber(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if block == nil || result.Decision == nil || result.Decision.Reference == nil {
		t.Fatalf("success result = %+v", result.Decision)
	}
	if result.Decision.Reference.Status.String() != "corroborated" {
		t.Fatalf("reference = %+v", result.Decision.Reference)
	}
	if !result.IsCorroborated() || len(result.Decision.Support) == 0 {
		t.Fatalf("business facts = %+v", result.Decision)
	}
}
