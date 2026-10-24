package ethguard_test

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/codermaybe/ethguard/rpctest"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
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
	if result.Decision.Status != rpc.WriteAccepted || result.Hash() != hash || !result.Accepted() {
		t.Fatalf("unexpected accepted result: %+v", result.Decision)
	}
	if result.Decision.Basis != 3 || result.Decision.Required != 2 || result.Decision.Agreement != 2 {
		t.Fatalf("unexpected fixed-basis decision: %+v", result.Decision)
	}
	if len(result.Decision.Accepted) != 2 || len(result.Decision.Dissent) != 1 {
		t.Fatalf("winning and dissent evidence not preserved: %+v", result.Decision)
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
	if result.Decision.Status != rpc.WriteDisputed || result.Decision.Agreement != 1 || len(result.Decision.Dissent) != 2 {
		t.Fatalf("unexpected disputed result: %+v", result.Decision)
	}
	if result.Accepted() {
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
	if result.Decision.Status != rpc.WriteRejected || len(result.Decision.Rejected) != 3 {
		t.Fatalf("unexpected rejected result: %+v", result.Decision)
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
	if result.Decision.Status != rpc.WriteUnreachable || result.Decision.Basis != 2 ||
		len(result.Decision.Silent) != 2 || len(result.Decision.Faults) != 2 {
		t.Fatalf("unexpected unreachable result: %+v", result.Decision)
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
	if result.Decision.Status != rpc.WriteAccepted || result.Decision.Basis != 1 || result.Decision.Required != 1 {
		t.Fatalf("unexpected forwarded result: %+v", result.Decision)
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
	if result.Decision.Status != rpc.WriteRejected || result.Decision.Basis != 1 || len(result.Decision.Rejected) != 1 {
		t.Fatalf("unexpected forwarded rejection: %+v", result.Decision)
	}
}

func TestSendRawTransactionForwardedFailsClosedOnForeignHash(t *testing.T) {
	client, nodes := fleet(t, 1, 0, 1)
	raw, localHash := writeTxFixture(t)
	foreign := common.HexToHash("0x" + strings.Repeat("44", 32))
	nodes[0].Return(methods.MethodSendRawTransaction, foreign.Hex())

	result, err := client.SendRawTransaction(context.Background(), raw)
	if !errors.Is(err, ethguard.ErrConflict) {
		t.Fatalf("forwarded foreign hash error = %v, want ErrConflict", err)
	}
	if result.Decision.Status != rpc.WriteDisputed || result.Decision.Hash != foreign.Hex() || result.Decision.Hash == localHash.Hex() {
		t.Fatalf("forwarded foreign hash was not failed closed: %+v", result.Decision)
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
	if !errors.Is(err, ethguard.ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict", err)
	}
	if result.Decision.Status != rpc.WriteDisputed || result.Decision.Hash != foreign.Hex() || result.Decision.Hash == localHash.Hex() {
		t.Fatalf("foreign winning hash was not failed closed: %+v", result.Decision)
	}
}

func TestWriteResultZeroValueIsNotAccepted(t *testing.T) {
	if (ethguard.WriteResult{}).Accepted() {
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
	if result.Accepted() || result.Decision.Status != rpc.WriteUnknown {
		t.Fatalf("invalid request returned accepted decision: %+v", result.Decision)
	}
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
