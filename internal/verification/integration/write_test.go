package ethguard_test

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/codermaybe/ethguard/rpctest"
)

func writeTxFixture(t *testing.T) ([]byte, common.Hash) {
	t.Helper()
	key, err := crypto.HexToECDSA("deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	if err != nil {
		t.Fatal(err)
	}
	tx := types.NewTransaction(
		7,
		common.HexToAddress("0x"+strings.Repeat("bb", 20)),
		big.NewInt(1),
		21000,
		big.NewInt(1),
		nil,
	)
	signed, err := types.SignTx(tx, types.HomesteadSigner{}, key)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := signed.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return raw, signed.Hash()
}

func TestSendRawTransactionAcceptedWithMinorityDissent(t *testing.T) {
	client, nodes := fleet(t, 1, 0, 3)
	raw, hash := writeTxFixture(t)
	for _, node := range nodes[:2] {
		node.Return(methods.MethodSendRawTransaction, hash.Hex())
	}
	nodes[2].Return(methods.MethodSendRawTransaction, common.HexToHash("0x"+strings.Repeat("11", 32)).Hex())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := client.SendRawTransaction(ctx, raw)
	if err != nil {
		t.Fatalf("accepted broadcast returned error: %v", err)
	}
	if writeDecision(t, result).Status != rpc.WriteAccepted || writeDecision(t, result).Hash != hash.Hex() || !result.IsWriteAccepted() {
		t.Fatalf("unexpected accepted result: %+v", writeDecision(t, result))
	}
	if writeDecision(t, result).Adjudication.Basis != 3 || writeDecision(t, result).Adjudication.Required != 2 || len(writeRelations(writeDecision(t, result).Sources, rpc.VerdictSupported)) != 2 {
		t.Fatalf("unexpected fixed-basis decision: %+v", writeDecision(t, result))
	}
	if len(writeRelations(writeDecision(t, result).Sources, rpc.VerdictSupported)) != 2 || len(writeRelations(writeDecision(t, result).Sources, rpc.VerdictDissented)) != 1 {
		t.Fatalf("winning and dissent evidence not preserved: %+v", writeDecision(t, result))
	}
	for i, node := range nodes {
		calls := node.Calls()
		if countMethod(calls, methods.MethodSendRawTransaction) != 1 {
			t.Fatalf("source %d send calls = %d, want 1", i, countMethod(calls, methods.MethodSendRawTransaction))
		}
	}
}

func TestSendRawTransactionRejectsDisputedHashes(t *testing.T) {
	client, nodes := fleet(t, 1, 0, 3)
	raw, _ := writeTxFixture(t)
	for i, node := range nodes {
		node.Return(methods.MethodSendRawTransaction, common.HexToHash("0x"+strings.Repeat(string(rune('1'+i)), 32)).Hex())
	}

	result, err := client.SendRawTransaction(context.Background(), raw)
	if !errors.Is(err, ethguard.ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict", err)
	}
	if writeDecision(t, result).Status != rpc.WriteUnconfirmed || len(writeRelations(writeDecision(t, result).Sources, rpc.VerdictSupported)) != 1 || len(writeRelations(writeDecision(t, result).Sources, rpc.VerdictDissented)) != 2 {
		t.Fatalf("unexpected disputed result: %+v", writeDecision(t, result))
	}
	if result.IsWriteAccepted() {
		t.Fatal("disputed write must not report Accepted")
	}
}

func TestSendRawTransactionRejectsWhenAllSourcesReturnProtocolErrors(t *testing.T) {
	client, nodes := fleet(t, 1, 0, 3)
	raw, _ := writeTxFixture(t)
	for _, node := range nodes {
		node.Fail(methods.MethodSendRawTransaction, -32010, "nonce too low")
	}

	result, err := client.SendRawTransaction(context.Background(), raw)
	if !errors.Is(err, ethguard.ErrNoValidResponse) {
		t.Fatalf("error = %v, want ErrNoValidResponse", err)
	}
	if writeDecision(t, result).Status != rpc.WriteUnconfirmed || len(writeRejections(writeDecision(t, result).Sources)) != 3 {
		t.Fatalf("unexpected rejected result: %+v", writeDecision(t, result))
	}
}

func TestSendRawTransactionReportsUnreachableFleet(t *testing.T) {
	client, nodes := fleet(t, 1, 0, 2)
	raw, _ := writeTxFixture(t)
	for _, node := range nodes {
		node.FailHTTP(methods.MethodSendRawTransaction, 503)
	}

	result, err := client.SendRawTransaction(context.Background(), raw)
	if !errors.Is(err, ethguard.ErrAllSourcesDown) {
		t.Fatalf("error = %v, want ErrAllSourcesDown", err)
	}
	if writeDecision(t, result).Status != rpc.WriteUnconfirmed || writeDecision(t, result).Adjudication.Basis != 2 ||
		len(writeUnavailable(writeDecision(t, result).Sources)) != 2 || len(writeDecision(t, result).Faults) != 1 {
		t.Fatalf("unexpected unreachable result: %+v", writeDecision(t, result))
	}
}

func TestSendRawTransactionForwardedUsesOneSourceBasis(t *testing.T) {
	client, nodes := fleet(t, 1, 0, 1)
	raw, hash := writeTxFixture(t)
	nodes[0].Return(methods.MethodSendRawTransaction, hash.Hex())

	result, err := client.SendRawTransaction(context.Background(), raw)
	if err != nil {
		t.Fatalf("forwarded broadcast returned error: %v", err)
	}
	if writeDecision(t, result).Status != rpc.WriteAccepted || writeDecision(t, result).Adjudication.Basis != 1 || writeDecision(t, result).Adjudication.Required != 1 {
		t.Fatalf("unexpected forwarded result: %+v", writeDecision(t, result))
	}
	if got := countMethod(nodes[0].Calls(), methods.MethodSendRawTransaction); got != 1 {
		t.Fatalf("first source send calls = %d, want 1", got)
	}
}

func TestSingleSourceClientConstructsForwardedMode(t *testing.T) {
	client, err := ethguard.New(config.Config{Upstreams: []config.Upstream{{ID: "single", Endpoint: "http://127.0.0.1:1"}}})
	if err != nil {
		t.Fatalf("single-source client construction failed: %v", err)
	}
	client.Close()
	if client.Mode() != rpc.CorroborationForwarded {
		t.Fatalf("mode = %s, want forwarded", client.Mode())
	}
}

func TestSendRawTransactionForwardedPreservesProtocolRejection(t *testing.T) {
	client, nodes := fleet(t, 1, 0, 1)
	raw, _ := writeTxFixture(t)
	nodes[0].Fail(methods.MethodSendRawTransaction, -32010, "nonce too low")

	result, err := client.SendRawTransaction(context.Background(), raw)
	if !errors.Is(err, ethguard.ErrNoValidResponse) {
		t.Fatalf("forwarded rejection error = %v, want ErrNoValidResponse", err)
	}
	if writeDecision(t, result).Status != rpc.WriteUnconfirmed || writeDecision(t, result).Adjudication.Basis != 1 || len(writeRejections(writeDecision(t, result).Sources)) != 1 {
		t.Fatalf("unexpected forwarded rejection: %+v", writeDecision(t, result))
	}
}

func TestSendRawTransactionForwardedFailsClosedOnForeignHash(t *testing.T) {
	client, nodes := fleet(t, 1, 0, 1)
	raw, localHash := writeTxFixture(t)
	foreign := common.HexToHash("0x" + strings.Repeat("44", 32))
	nodes[0].Return(methods.MethodSendRawTransaction, foreign.Hex())

	result, err := client.SendRawTransaction(context.Background(), raw)
	if !errors.Is(err, ethguard.ErrHashMismatch) {
		t.Fatalf("forwarded foreign hash error = %v, want ErrHashMismatch", err)
	}
	if writeDecision(t, result).Status != rpc.WriteUnconfirmed || writeDecision(t, result).Hash != foreign.Hex() || writeDecision(t, result).Hash == localHash.Hex() {
		t.Fatalf("forwarded foreign hash was not failed closed: %+v", writeDecision(t, result))
	}
}

func TestSendRawTransactionFailsClosedOnForeignWinningHash(t *testing.T) {
	client, nodes := fleet(t, 1, 0, 3)
	raw, localHash := writeTxFixture(t)
	foreign := common.HexToHash("0x" + strings.Repeat("33", 32))
	for _, node := range nodes {
		node.Return(methods.MethodSendRawTransaction, foreign.Hex())
	}

	result, err := client.SendRawTransaction(context.Background(), raw)
	if !errors.Is(err, ethguard.ErrHashMismatch) {
		t.Fatalf("error = %v, want ErrHashMismatch", err)
	}
	if writeDecision(t, result).Status != rpc.WriteUnconfirmed || writeDecision(t, result).Hash != foreign.Hex() || writeDecision(t, result).Hash == localHash.Hex() {
		t.Fatalf("foreign winning hash was not failed closed: %+v", writeDecision(t, result))
	}
}

func TestWriteResultZeroValueIsNotAccepted(t *testing.T) {
	var zero ethguard.CallResult
	if zero.IsWriteAccepted() {
		t.Fatal("zero write result must not report acceptance")
	}
}

func TestSendRawTransactionRejectsInvalidBytesBeforeContact(t *testing.T) {
	client, err := ethguard.New(config.Config{Upstreams: []config.Upstream{{ID: "unused", Endpoint: "http://127.0.0.1:1"}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.SendRawTransaction(context.Background(), []byte{0x01})
	if !errors.Is(err, ethguard.ErrInvalidRequest) {
		t.Fatalf("error = %v, want ErrInvalidRequest", err)
	}
	if result.IsWriteAccepted() || (result.WriteDecision != nil && result.WriteDecision.Status != rpc.WriteUnknown) {
		t.Fatalf("invalid request returned accepted decision: %+v", writeDecision(t, result))
	}
}

// signedWriteTransaction builds the signed transaction the typed write
// entries broadcast. The typed entry marshals the transaction itself, so the
// fixture keeps the transaction object beside its raw bytes.
func signedWriteTransaction(t *testing.T) (*types.Transaction, []byte, common.Hash) {
	t.Helper()
	key, err := crypto.HexToECDSA("deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	if err != nil {
		t.Fatal(err)
	}
	tx := types.NewTransaction(
		7,
		common.HexToAddress("0x"+strings.Repeat("bb", 20)),
		big.NewInt(1),
		21000,
		big.NewInt(1),
		nil,
	)
	signed, err := types.SignTx(tx, types.HomesteadSigner{}, key)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := signed.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return signed, raw, signed.Hash()
}

// TestSendTransactionBroadcastsTheSignedBytes pins the typed write entry:
// it broadcasts the caller's signed transaction bytes unchanged, keeps the
// write quorum fixed over the frozen basis, and preserves the accepting
// sources on the returned decision.
func TestSendTransactionBroadcastsTheSignedBytes(t *testing.T) {
	client, nodes := fleet(t, 1, 0, 3)
	tx, raw, hash := signedWriteTransaction(t)
	for _, node := range nodes[:2] {
		node.Return(methods.MethodSendRawTransaction, hash.Hex())
	}
	nodes[2].Return(methods.MethodSendRawTransaction, common.HexToHash("0x"+strings.Repeat("11", 32)).Hex())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := client.SendTransaction(ctx, tx)
	if err != nil {
		t.Fatalf("typed broadcast returned error: %v", err)
	}
	decision := writeDecision(t, result)
	if decision.Status != rpc.WriteAccepted || decision.Hash != hash.Hex() || !result.IsWriteAccepted() {
		t.Fatalf("unexpected accepted result: %+v", decision)
	}
	if decision.Adjudication.Basis != 3 || decision.Adjudication.Required != 2 || len(writeRelations(decision.Sources, rpc.VerdictSupported)) != 2 {
		t.Fatalf("unexpected fixed-basis decision: %+v", decision)
	}
	if len(writeRelations(decision.Sources, rpc.VerdictSupported)) != 2 || len(writeRelations(decision.Sources, rpc.VerdictDissented)) != 1 {
		t.Fatalf("winning and dissent evidence not preserved: %+v", decision)
	}
	want := `"0x` + common.Bytes2Hex(raw) + `"`
	for i, node := range nodes {
		calls := node.Calls()
		if countMethod(calls, methods.MethodSendRawTransaction) != 1 {
			t.Fatalf("source %d send calls = %d, want 1", i, countMethod(calls, methods.MethodSendRawTransaction))
		}
		var sent string
		for _, call := range calls {
			if call.Method == methods.MethodSendRawTransaction {
				if len(call.Params) != 1 {
					t.Fatalf("source %d send parameters = %s, want exactly one raw transaction", i, call.Params)
				}
				sent = string(call.Params[0])
			}
		}
		if sent != want {
			t.Fatalf("source %d broadcast changed signed bytes: got %s, want %s", i, sent, want)
		}
	}
}

// TestSendTransactionFailsClosedOnDisputedHashes pins the typed write entry's
// failure path: sources that answer with different hashes never produce an
// accepted write, and the disputed hash stays visible as evidence.
func TestSendTransactionFailsClosedOnDisputedHashes(t *testing.T) {
	client, nodes := fleet(t, 1, 0, 3)
	tx, _, _ := signedWriteTransaction(t)
	for i, node := range nodes {
		node.Return(methods.MethodSendRawTransaction, common.HexToHash("0x"+strings.Repeat(string(rune('1'+i)), 32)).Hex())
	}

	result, err := client.SendTransaction(context.Background(), tx)
	if !errors.Is(err, ethguard.ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict", err)
	}
	decision := writeDecision(t, result)
	if decision.Status != rpc.WriteUnconfirmed || len(writeRelations(decision.Sources, rpc.VerdictSupported)) != 1 || len(writeRelations(decision.Sources, rpc.VerdictDissented)) != 2 {
		t.Fatalf("unexpected disputed result: %+v", decision)
	}
	if result.IsWriteAccepted() {
		t.Fatal("disputed write must not report Accepted")
	}
}

// TestSendTransactionReportsUnreachableFleet pins the typed write entry's
// transport failure: no source answered, so the write is unreachable rather
// than rejected, and every silent source stays on the frozen basis.
func TestSendTransactionReportsUnreachableFleet(t *testing.T) {
	client, nodes := fleet(t, 1, 0, 2)
	tx, _, _ := signedWriteTransaction(t)
	for _, node := range nodes {
		node.FailHTTP(methods.MethodSendRawTransaction, 503)
	}

	result, err := client.SendTransaction(context.Background(), tx)
	if !errors.Is(err, ethguard.ErrAllSourcesDown) {
		t.Fatalf("error = %v, want ErrAllSourcesDown", err)
	}
	decision := writeDecision(t, result)
	if decision.Status != rpc.WriteUnconfirmed || decision.Adjudication.Basis != 2 || len(writeUnavailable(decision.Sources)) != 2 {
		t.Fatalf("unexpected unreachable result: %+v", decision)
	}
}

func writeDecision(t *testing.T, result ethguard.CallResult) *rpc.WriteDecision {
	t.Helper()
	if result.WriteDecision == nil {
		t.Fatalf("result has no WriteDecision: %+v", result)
	}
	return result.WriteDecision
}

func countMethod(calls []rpctest.Call, method string) int {
	count := 0
	for _, call := range calls {
		if call.Method == method {
			count++
		}
	}
	return count
}

// These assertion helpers query the authoritative source evidence without
// storing a second classification in the decision.
func writeRelations(sources []rpc.SourceAnswer, relation rpc.VerdictKind) []rpc.SourceAnswer {
	var matches []rpc.SourceAnswer
	for _, source := range sources {
		if source.Relation == relation {
			matches = append(matches, source)
		}
	}
	return matches
}
func writeRejections(sources []rpc.SourceAnswer) []rpc.SourceAnswer {
	var matches []rpc.SourceAnswer
	for _, source := range sources {
		if source.Rejection != nil {
			matches = append(matches, source)
		}
	}
	return matches
}
func writeUnavailable(sources []rpc.SourceAnswer) []rpc.SourceAnswer {
	var matches []rpc.SourceAnswer
	for _, source := range sources {
		if source.Answer.Kind == rpc.AnswerUnassessed {
			matches = append(matches, source)
		}
	}
	return matches
}
