package ethguard_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/ethapi"
	"github.com/codermaybe/ethguard/internal/methods"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpctest"
)

var receiptWait = 3 * time.Second

type syncWriteEntrypoint struct {
	name  string
	typed bool
	view  bool
}

var syncWriteEntrypoints = []syncWriteEntrypoint{
	{name: "root raw"},
	{name: "root typed", typed: true},
	{name: "view raw", view: true},
	{name: "view typed", typed: true, view: true},
}

type countingSyncTransaction struct {
	raw   []byte
	calls int
}

func (tx *countingSyncTransaction) MarshalBinary() ([]byte, error) {
	tx.calls++
	return tx.raw, nil
}

type syncWriteObservation struct {
	receipt  *ethapi.Receipt
	direct   ethguard.CallResult
	captured ethguard.CallResult
	encodes  int
	err      error
}

func invokeSyncWrite(t *testing.T, entry syncWriteEntrypoint, parent context.Context, client *ethguard.Client, raw []byte, timeout *time.Duration) syncWriteObservation {
	t.Helper()
	ctx, capture := ethguard.WithResult(parent)
	tx := &countingSyncTransaction{raw: raw}
	var observation syncWriteObservation
	switch {
	case entry.view && entry.typed:
		observation.receipt, observation.err = client.EthClient().SendTransactionSync(ctx, tx, timeout)
	case entry.view:
		observation.receipt, observation.err = client.EthClient().SendRawTransactionSync(ctx, raw, timeout)
	case entry.typed:
		observation.receipt, observation.direct, observation.err = client.SendTransactionSync(ctx, tx, timeout)
	default:
		observation.receipt, observation.direct, observation.err = client.SendRawTransactionSync(ctx, raw, timeout)
	}
	observation.encodes = tx.calls
	wantEncodes := 0
	if entry.typed {
		wantEncodes = 1
	}
	if observation.encodes != wantEncodes {
		t.Fatalf("MarshalBinary calls = %d, want %d", observation.encodes, wantEncodes)
	}
	if observation.err != nil && observation.receipt != nil {
		t.Fatalf("failed sync write returned a receipt: %+v", observation.receipt)
	}
	published, ok, captureErr := capture.Result()
	if !ok || captureErr != nil {
		t.Fatalf("capture delivery = %v, %v", ok, captureErr)
	}
	observation.captured = published
	if observation.captured.Err != observation.err { //nolint:errorlint // The result must retain the exact error returned to the caller.
		t.Fatalf("capture error = %v, caller error = %v", observation.captured.Err, observation.err)
	}
	if !entry.view {
		if observation.direct.Err != observation.err { //nolint:errorlint // The result must retain the exact error returned to the caller.
			t.Fatalf("direct result error = %v, caller error = %v", observation.direct.Err, observation.err)
		}
		if !reflect.DeepEqual(observation.direct, observation.captured) {
			t.Fatalf("capture differs: direct=%+v captured=%+v", observation.direct, observation.captured)
		}
	}
	if published.ReceiptWait != nil {
		if published.WriteDecision == nil || published.WriteDecision.Status != rpc.WriteAccepted {
			t.Fatalf("waiting without accepted broadcast: %+v", published)
		}
		wait := published.ReceiptWait
		if wait.LastObservation != nil && (wait.LastAttempt == nil || wait.LastObservation.Sequence > wait.LastAttempt.Sequence) {
			t.Fatalf("receipt sequence order: %+v", wait)
		}
		if wait.LastAttempt != nil && wait.LastAttempt.Sequence == 0 {
			t.Fatal("actual query has zero sequence")
		}
	}
	if published.Decision != nil {
		t.Fatalf("synchronous write gained a receipt read Decision: %+v", published.Decision)
	}
	return observation
}

func assertSyncWriteAccepted(t *testing.T, result ethguard.CallResult, hash common.Hash, nodes []*rpctest.Node) {
	t.Helper()
	decision := writeDecision(t, result)
	if decision.Status != rpc.WriteAccepted || decision.Hash != hash.Hex() {
		t.Fatalf("write decision = %+v, want accepted hash %s", decision, hash)
	}
	if result.Decision != nil {
		t.Fatalf("synchronous write gained a receipt read Decision: %+v", result.Decision)
	}
	// All accepted scenarios broadcast to two agreeing sources and one
	// dissenting source. Receipt observations must not rewrite this evidence.
	if decision.Adjudication.Basis != 3 || decision.Adjudication.Required != 2 || len(writeRelations(decision.Sources, rpc.VerdictSupported)) != 2 || len(writeRelations(decision.Sources, rpc.VerdictDissented)) != 1 ||
		len(writeRejections(decision.Sources)) != 0 || len(writeUnavailable(decision.Sources)) != 0 || len(decision.Faults) != 0 {
		t.Fatalf("original broadcast evidence was lost: %+v", decision)
	}
	seen := make(map[string]bool)
	for _, answer := range writeRelations(decision.Sources, rpc.VerdictSupported) {
		if answer.Hash != hash.Hex() || answer.Rejection != nil || answer.Source.ID == "" ||
			(answer.Source.URL != nodes[0].URL() && answer.Source.URL != nodes[1].URL()) || seen[answer.Source.URL] {
			t.Fatalf("unexpected accepting source: %+v", answer)
		}
		seen[answer.Source.URL] = true
	}
	dissent := writeRelations(decision.Sources, rpc.VerdictDissented)[0]
	if dissent.Hash != syncForeignHash().Hex() || dissent.Rejection != nil ||
		dissent.Source.ID == "" || dissent.Source.URL != nodes[2].URL() {
		t.Fatalf("original dissent was lost: %+v", dissent)
	}
}

func assertSyncReceiptCallsBound(t *testing.T, nodes []*rpctest.Node, hash common.Hash) {
	t.Helper()
	for i, node := range nodes {
		receiptCalls := 0
		for _, call := range node.Calls() {
			if call.Method != methods.MethodGetTransactionReceipt {
				continue
			}
			receiptCalls++
			if len(call.Params) != 1 {
				t.Fatalf("node %d receipt params = %v", i, call.Params)
			}
			var requested string
			if err := json.Unmarshal(call.Params[0], &requested); err != nil {
				t.Fatalf("node %d receipt hash: %v", i, err)
			}
			if requested != hash.Hex() {
				t.Fatalf("node %d receipt hash = %s, want %s", i, requested, hash)
			}
		}
		if receiptCalls == 0 {
			t.Fatalf("node %d received no receipt query", i)
		}
	}
}

func assertSyncBroadcastOnce(t *testing.T, nodes []*rpctest.Node) {
	t.Helper()
	for i, node := range nodes {
		if calls := countMethod(node.Calls(), methods.MethodSendRawTransaction); calls != 1 {
			t.Fatalf("node %d broadcast calls = %d, want one", i, calls)
		}
	}
}

// syncReceiptFixture marshals one mined receipt for a transaction hash into
// the wire shape the receipt admission layer accepts.
func syncReceiptFixture(t *testing.T, txHash common.Hash, height int64) json.RawMessage {
	t.Helper()
	receipt := &types.Receipt{
		Type:              types.LegacyTxType,
		Status:            types.ReceiptStatusSuccessful,
		CumulativeGasUsed: 21000,
		Logs: []*types.Log{{
			Address:     common.HexToAddress("0x" + strings.Repeat("cc", 20)),
			Topics:      []common.Hash{common.HexToHash("0x" + strings.Repeat("dd", 32))},
			Data:        []byte{0x01},
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
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	object["from"] = json.RawMessage(`"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`)
	delete(object, "root")
	raw, err = json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestSendRawTransactionSyncLocalValidationPins pins the local-only
// rejections of the synchronous write entry: a negative receipt timeout and
// an undecodable payload fail before any wire contact, with the classified
// sentinel the write contract promises.
func TestSendRawTransactionSyncLocalValidationPins(t *testing.T) {
	client, nodes := fleet(t, 1, 0, 2)
	raw, _ := writeTxFixture(t)
	negative := -1 * time.Second
	if _, _, err := client.SendRawTransactionSync(context.Background(), raw, &negative); !errors.Is(err, rpc.ErrInvalidRequest) {
		t.Fatalf("negative timeout error = %v, want ErrInvalidRequest", err)
	}
	if _, _, err := client.SendRawTransactionSync(context.Background(), []byte("not-a-tx"), nil); !errors.Is(err, rpc.ErrInvalidRequest) {
		t.Fatalf("undecodable payload error = %v, want ErrInvalidRequest", err)
	}
	if _, _, err := client.SendTransactionSync(context.Background(), nil, nil); !errors.Is(err, rpc.ErrInvalidRequest) {
		t.Fatalf("nil transaction error = %v, want ErrInvalidRequest", err)
	}
	for i, node := range nodes {
		if calls := countMethod(node.Calls(), methods.MethodSendRawTransaction); calls != 0 {
			t.Fatalf("source %d contacted on a local rejection: %d calls", i, calls)
		}
	}
}

func syncForeignHash() common.Hash {
	return common.HexToHash("0x" + strings.Repeat("33", 32))
}

func scriptSyncAccepted(nodes []*rpctest.Node, hash common.Hash) {
	for _, node := range nodes[:2] {
		node.Return(methods.MethodSendRawTransaction, hash.Hex())
	}
	nodes[2].Return(methods.MethodSendRawTransaction, syncForeignHash().Hex())
}

func assertSyncMethodCalls(t *testing.T, nodes []*rpctest.Node, method string, want int) {
	t.Helper()
	for i, node := range nodes {
		if got := countMethod(node.Calls(), method); got != want {
			t.Fatalf("node %d %s calls = %d, want %d", i, method, got, want)
		}
	}
}

func TestSyncWriteReceiptSuccess(t *testing.T) {
	for _, entry := range syncWriteEntrypoints {
		t.Run(entry.name, func(t *testing.T) {
			client, nodes := fleet(t, 1, 0, 3)
			raw, hash := writeTxFixture(t)
			scriptSyncAccepted(nodes, hash)
			for _, node := range nodes {
				node.Return(methods.MethodGetTransactionReceipt, syncReceiptFixture(t, hash, 17))
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			got := invokeSyncWrite(t, entry, ctx, client, raw, &receiptWait)
			if got.err != nil || got.receipt == nil || got.receipt.TransactionHash != ethapi.Hash(hash.Hex()) {
				t.Fatalf("receipt = %+v, error = %v", got.receipt, got.err)
			}
			wait := got.captured.ReceiptWait
			if wait == nil || wait.LastAttempt == nil || wait.LastObservation == nil || wait.LastAttempt.Sequence != 1 || wait.LastObservation.Sequence != 1 || wait.LastAttempt.Err != nil || wait.LastObservation.Err != nil || wait.LastObservation.Decision.Adjudication.Reason != rpc.ReasonAccepted {
				t.Fatalf("receipt success missing: %+v", wait)
			}
			assertSyncWriteAccepted(t, got.captured, hash, nodes)
			if got.captured.Uncertain != nil {
				t.Fatal("capture without source-evidence opt-in retained material")
			}
			assertSyncBroadcastOnce(t, nodes)
			assertSyncReceiptCallsBound(t, nodes, hash)
			assertSyncMethodCalls(t, nodes, methods.MethodGetTransactionReceipt, 1)
			for i, node := range nodes {
				for _, call := range node.Calls() {
					if call.Method == methods.MethodSendRawTransaction {
						var sent string
						if len(call.Params) != 1 || json.Unmarshal(call.Params[0], &sent) != nil || sent != "0x"+common.Bytes2Hex(raw) {
							t.Fatalf("node %d broadcast changed signed bytes: %s", i, call.Params)
						}
					}
				}
			}
		})
	}
}

func TestSyncWriteUnconfirmedBroadcastNeverPolls(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		status rpc.WriteStatus
		err    error
	}{
		{"rejected", rpc.WriteUnconfirmed, rpc.ErrNoValidResponse},
		{"disputed", rpc.WriteUnconfirmed, rpc.ErrConflict},
		{"unreachable", rpc.WriteUnconfirmed, ethguard.ErrAllSourcesDown},
		{"foreign hash quorum", rpc.WriteUnconfirmed, rpc.ErrHashMismatch},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			for _, entry := range syncWriteEntrypoints {
				t.Run(entry.name, func(t *testing.T) {
					client, nodes := fleet(t, 1, 0, 3)
					raw, hash := writeTxFixture(t)
					for i, node := range nodes {
						switch scenario.name {
						case "rejected":
							node.Fail(methods.MethodSendRawTransaction, -32010, "insufficient funds")
						case "disputed":
							node.Return(methods.MethodSendRawTransaction, common.HexToHash("0x"+strings.Repeat(string(rune('1'+i)), 32)).Hex())
						case "unreachable":
							node.FailHTTP(methods.MethodSendRawTransaction, http.StatusServiceUnavailable)
						case "foreign hash quorum":
							node.Return(methods.MethodSendRawTransaction, syncForeignHash().Hex())
						}
						// If the gate regresses, a valid receipt is available. Zero
						// queries is asserted independently of the final error.
						node.Return(methods.MethodGetTransactionReceipt, syncReceiptFixture(t, hash, 17))
					}
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					got := invokeSyncWrite(t, entry, ctx, client, raw, &receiptWait)
					if !errors.Is(got.err, scenario.err) {
						t.Fatalf("error = %v, want %v", got.err, scenario.err)
					}
					if got.captured.ReceiptWait != nil {
						t.Fatal("unconfirmed broadcast entered receipt waiting")
					}
					d := writeDecision(t, got.captured)
					if d.Status != scenario.status || d.Adjudication.Basis != 3 || d.Adjudication.Required != 2 {
						t.Fatalf("broadcast decision = %+v", d)
					}
					switch scenario.name {
					case "rejected":
						if len(writeRejections(d.Sources)) != 3 {
							t.Fatalf("rejection evidence lost: %+v", d)
						}
					case "disputed":
						if len(writeRelations(d.Sources, rpc.VerdictSupported)) != 1 || len(writeRelations(d.Sources, rpc.VerdictDissented)) != 2 {
							t.Fatalf("disagreement evidence lost: %+v", d)
						}
					case "unreachable":
						if len(writeUnavailable(d.Sources)) != 3 || len(d.Faults) != 1 {
							t.Fatalf("transport evidence lost: %+v", d)
						}
					case "foreign hash quorum":
						if d.Hash != syncForeignHash().Hex() || len(writeRelations(d.Sources, rpc.VerdictSupported)) != 3 || d.SubmittedHash != hash.Hex() || d.Adjudication.Reason != rpc.ReasonHashMismatch {
							t.Fatalf("foreign winning hash evidence lost: %+v", d)
						}
					}
					assertSyncBroadcastOnce(t, nodes)
					assertSyncMethodCalls(t, nodes, methods.MethodGetTransactionReceipt, 0)
				})
			}
		})
	}
}

func TestSyncWritePollsNotFoundThenConflictThenReceipt(t *testing.T) {
	for _, entry := range syncWriteEntrypoints {
		t.Run(entry.name, func(t *testing.T) {
			client, nodes := fleet(t, 1, 0, 3)
			raw, hash := writeTxFixture(t)
			scriptSyncAccepted(nodes, hash)
			mined := syncReceiptFixture(t, hash, 42)
			for i, node := range nodes {
				conflicting := syncReceiptFixture(t, hash, int64(17+i))
				var attempt atomic.Int32
				node.Handle(methods.MethodGetTransactionReceipt, func(rpctest.Call) (any, error) {
					switch attempt.Add(1) {
					case 1:
						return nil, nil
					case 2:
						return conflicting, nil
					default:
						return mined, nil
					}
				})
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			got := invokeSyncWrite(t, entry, ctx, client, raw, &receiptWait)
			if got.err != nil || got.receipt == nil || got.receipt.TransactionHash != ethapi.Hash(hash.Hex()) || got.receipt.BlockNumber != "0x2a" {
				t.Fatalf("final receipt = %+v, error = %v", got.receipt, got.err)
			}
			assertSyncWriteAccepted(t, got.captured, hash, nodes)
			assertSyncBroadcastOnce(t, nodes)
			assertSyncReceiptCallsBound(t, nodes, hash)
			assertSyncMethodCalls(t, nodes, methods.MethodGetTransactionReceipt, 3)
		})
	}
}

// A short positive timeout permits repeated observations but bounds evidence
// well below the shared raw+fields record cap. It says only that waiting ended.
func TestSyncWritePersistentReceiptObservationsTimeOut(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		name := "not found"
		if conflict {
			name = "conflict"
		}
		t.Run(name, func(t *testing.T) {
			for _, entry := range syncWriteEntrypoints {
				t.Run(entry.name, func(t *testing.T) {
					checkSyncReceiptTimeoutEvidence(t, entry, conflict, 1<<20)
				})
			}
			// Budget exhaustion must not change polling, the write decision,
			// the final error or broadcast multiplicity. One facade is enough
			// for this control of the shared operation collector.
			t.Run("tiny evidence budget", func(t *testing.T) {
				checkSyncReceiptTimeoutEvidence(t, syncWriteEntrypoints[0], conflict, 1)
			})
		})
	}
}

func checkSyncReceiptTimeoutEvidence(t *testing.T, entry syncWriteEntrypoint, conflict bool, budget int) {
	t.Helper()
	client, nodes := fleet(t, 1, 0, 3)
	raw, hash := writeTxFixture(t)
	scriptSyncAccepted(nodes, hash)
	wantRaw := make(map[string]json.RawMessage)
	wantSources := make(map[string]rpc.SourceRef)
	for i, node := range nodes {
		value := json.RawMessage("null")
		if conflict {
			value = syncReceiptFixture(t, hash, int64(17+i))
		}
		wantRaw[node.URL()] = value
		wantSources[node.URL()] = rpc.SourceRef{Name: fmt.Sprintf("node-%c", 'a'+i), URL: node.URL(), ID: node.URL()}
		node.ReturnRaw(methods.MethodGetTransactionReceipt, value)
	}
	parent, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx, err := ethguard.WithSourceEvidence(parent, budget)
	if err != nil {
		t.Fatal(err)
	}
	timeout := 250 * time.Millisecond
	got := invokeSyncWrite(t, entry, ctx, client, raw, &timeout)
	if !errors.Is(got.err, context.DeadlineExceeded) || parent.Err() != nil {
		t.Fatalf("receipt timeout = %v, parent error = %v", got.err, parent.Err())
	}
	if errors.Is(got.err, rpc.ErrConflict) || errors.Is(got.err, ethereum.NotFound) {
		t.Fatalf("timeout acquired a receipt outcome in its error chain: %v", got.err)
	}
	wait := got.captured.ReceiptWait
	if wait == nil || wait.LastAttempt == nil || wait.LastObservation == nil {
		t.Fatalf("lost receipt facts at timeout: %+v", wait)
	}
	observation := wait.LastObservation
	decision := observation.Decision
	if decision == nil || decision.Adjudication.Basis != 3 || decision.Adjudication.Required != 2 || len(decision.Sources) != 3 {
		t.Fatalf("receipt observation lost its fixed voting basis: %+v", observation)
	}
	// The final poll may complete for only part of the fleet. Count only
	// complete, validated answers; every other source must record the deadline.
	validated := make(map[string]bool)
	seen := make(map[string]bool)
	for _, source := range decision.Sources {
		want, ok := wantSources[source.Source.URL]
		if !ok || source.Source != want || seen[source.Source.URL] {
			t.Fatalf("unexpected or repeated observation source: %+v", source)
		}
		seen[source.Source.URL] = true
		if source.Exchange.Cause != nil {
			if !errors.Is(source.Exchange.Cause, context.DeadlineExceeded) || source.Relation == rpc.VerdictSupported || source.Relation == rpc.VerdictDissented {
				t.Fatalf("interrupted observation source acquired a vote: %+v", source)
			}
			continue
		}
		if source.Exchange.Progress != rpc.ExchangeCompleted || source.Answer.Kind != rpc.AnswerResult || source.Answer.Cause != nil || source.Answer.Interpretation.State != rpc.InterpretationValidated || source.Answer.Interpretation.Cause != nil {
			t.Fatalf("receipt source was not fully validated: %+v", source)
		}
		validated[source.Source.URL] = true
	}
	n := len(validated)
	if n == 0 {
		t.Fatalf("an entirely interrupted poll replaced the last observation: %+v", observation)
	}
	wantErr, wantReason, wantStatus := rpc.ErrInsufficientSupport, rpc.ReasonInsufficientSupport, rpc.Unknown
	wantValue := ""
	if n >= 2 {
		if conflict {
			wantErr, wantReason = rpc.ErrConflict, rpc.ReasonConflictingCandidates
		} else {
			wantErr, wantReason, wantStatus = ethereum.NotFound, rpc.ReasonAccepted, rpc.Corroborated
			wantValue = "null"
		}
	}
	if !errors.Is(observation.Err, wantErr) || decision.Adjudication.Reason != wantReason || decision.Status != wantStatus || string(decision.Value) != wantValue {
		t.Fatalf("receipt observation with %d validated sources = %+v, want %v", n, observation, wantErr)
	}
	for _, other := range []error{context.DeadlineExceeded, rpc.ErrInsufficientSupport, rpc.ErrConflict, ethereum.NotFound} {
		if !errors.Is(wantErr, other) && errors.Is(observation.Err, other) {
			t.Fatalf("receipt observation mixed conclusions: %v", observation.Err)
		}
	}
	wantGroups, wantGroupSize := 1, n
	if conflict {
		wantGroups, wantGroupSize = n, 1
	}
	if len(decision.Adjudication.Candidates) != wantGroups {
		t.Fatalf("receipt candidate groups = %+v, want %d", decision.Adjudication.Candidates, wantGroups)
	}
	grouped := make(map[string]bool)
	for _, group := range decision.Adjudication.Candidates {
		if len(group.Sources) != wantGroupSize {
			t.Fatalf("receipt candidate = %+v, want %d sources", group, wantGroupSize)
		}
		for _, source := range group.Sources {
			if source != wantSources[source.URL] || !validated[source.URL] || grouped[source.URL] {
				t.Fatalf("candidate borrowed an invalid or repeated source: %+v", source)
			}
			grouped[source.URL] = true
		}
	}
	assertSyncWriteAccepted(t, got.captured, hash, nodes)
	assertSyncBroadcastOnce(t, nodes)
	assertSyncReceiptCallsBound(t, nodes, hash)
	for i, node := range nodes {
		if calls := countMethod(node.Calls(), methods.MethodGetTransactionReceipt); calls < 2 {
			t.Fatalf("node %d did not retry its receipt observation: %d", i, calls)
		}
	}
	material := got.captured.Uncertain
	if material == nil || material.Limit != budget || material.Bytes > budget {
		t.Fatalf("source-evidence budget = %+v", material)
	}
	if budget == 1 {
		if !material.Truncated || material.Dropped == 0 {
			t.Fatalf("tiny budget did not truncate: %+v", material)
		}
		return
	}
	if material.Truncated || material.Dropped != 0 {
		t.Fatalf("bounded receipt observations unexpectedly truncated: %+v", material)
	}
	// The deadline can fall between polls or interrupt the final poll. Keep
	// its transport errors separate from the completed receipt observations.
	successful, _, _ := assertSyncTimeoutRawEvidence(t, material, nodes, wait.LastAttempt, wantRaw)
	assertSyncRawEvidence(t, successful, nodes, hash, wantRaw, 2)
}

func assertSyncTimeoutRawEvidence(t *testing.T, material *ethguard.UncertainResult, nodes []*rpctest.Node, last *ethguard.ReceiptAttempt, wantRaw map[string]json.RawMessage) (*ethguard.UncertainResult, int, int) {
	t.Helper()
	successful := *material
	successful.Raw = nil
	sources := make(map[string]rpc.SourceRef, len(nodes))
	for i, node := range nodes {
		sources[node.URL()] = rpc.SourceRef{Name: fmt.Sprintf("node-%c", 'a'+i), URL: node.URL(), ID: node.URL()}
	}
	timedOut := make(map[string]bool)
	collectTimeouts := 0
	collectionDeadline := false
	if last.Decision != nil {
		for _, fault := range last.Decision.CollectionFaults {
			collectionDeadline = collectionDeadline || errors.Is(fault, context.DeadlineExceeded)
		}
	}
	for _, item := range material.Raw {
		if item.Error == "" {
			successful.Raw = append(successful.Raw, item)
			continue
		}
		if last.Decision == nil || (!errors.Is(last.Err, context.DeadlineExceeded) && !collectionDeadline) || item.Truncated || item.Method != methods.MethodGetTransactionReceipt || item.Error != context.DeadlineExceeded.Error() {
			t.Fatalf("unexpected timeout evidence: %+v, last attempt: %+v", item, last)
		}
		switch item.Stage {
		case "response":
			want, ok := sources[item.Source.URL]
			if !ok || item.Source != want || timedOut[item.Source.URL] {
				t.Fatalf("unexpected or repeated source timeout: %+v", item)
			}
			sourceDeadline := false
			for _, source := range last.Decision.Sources {
				if source.Source == item.Source && errors.Is(source.Exchange.Cause, context.DeadlineExceeded) {
					sourceDeadline = true
				}
			}
			if !sourceDeadline {
				t.Fatalf("raw timeout lacks the last attempt's source fault: %+v", item)
			}
			// A complete body may arrive just before the transport notices the
			// deadline. It remains error evidence, never a successful response.
			if len(item.Value) != 0 && string(item.Value) != string(wantRaw[item.Source.URL]) {
				t.Fatalf("unexpected timed-out response body: %+v", item)
			}
			timedOut[item.Source.URL] = true
		case "collect":
			if !collectionDeadline || item.Source != (rpc.SourceRef{}) || len(item.Value) != 0 || collectTimeouts != 0 {
				t.Fatalf("unexpected or repeated collection timeout: %+v", item)
			}
			collectTimeouts++
		default:
			t.Fatalf("unexpected timeout stage: %+v", item)
		}
	}
	return &successful, len(timedOut), collectTimeouts
}

func assertSyncRawEvidence(t *testing.T, material *ethguard.UncertainResult, nodes []*rpctest.Node, hash common.Hash, wantRaw map[string]json.RawMessage, minReceipts int) {
	t.Helper()
	seen := make(map[string]int)
	broadcasts := make(map[string]int)
	for _, item := range material.Raw {
		if item.Source.ID == "" || item.Truncated || item.Error != "" {
			t.Fatalf("unexpected raw evidence metadata: %+v", item)
		}
		switch item.Method {
		case methods.MethodGetTransactionReceipt:
			want, ok := wantRaw[item.Source.URL]
			if !ok || item.Stage != "response" || string(item.Value) != string(want) {
				t.Fatalf("receipt raw evidence = %+v, want %s", item, want)
			}
			seen[item.Source.URL]++
		case methods.MethodSendRawTransaction:
			wantHash := hash.Hex()
			if item.Source.URL == nodes[2].URL() {
				wantHash = syncForeignHash().Hex()
			}
			if _, ok := wantRaw[item.Source.URL]; !ok || item.Stage != "broadcast" || string(item.Value) != `"`+wantHash+`"` {
				t.Fatalf("broadcast raw evidence = %+v", item)
			}
			broadcasts[item.Source.URL]++
		default:
			t.Fatalf("unexpected evidence method: %+v", item)
		}
	}
	for i, node := range nodes {
		if seen[node.URL()] < minReceipts || broadcasts[node.URL()] != 1 {
			t.Fatalf("node %d retained receipt responses = %d, broadcasts = %d", i, seen[node.URL()], broadcasts[node.URL()])
		}
	}
}

func TestSyncWriteUnexpectedReceiptErrorStopsPolling(t *testing.T) {
	for _, scenario := range []struct {
		name string
		err  error
	}{
		{"foreign receipt", rpc.ErrMalformedResponse},
		{"missing receipt field", rpc.ErrMalformedResponse},
		{"provider protocol error", rpc.ErrNoValidResponse},
		{"provider transport error", ethguard.ErrAllSourcesDown},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			for _, entry := range syncWriteEntrypoints {
				t.Run(entry.name, func(t *testing.T) {
					client, nodes := fleet(t, 1, 0, 3)
					raw, hash := writeTxFixture(t)
					scriptSyncAccepted(nodes, hash)
					foreign := syncReceiptFixture(t, syncForeignHash(), 17)
					mined := syncReceiptFixture(t, hash, 17)
					var incomplete map[string]any
					if err := json.Unmarshal(mined, &incomplete); err != nil {
						t.Fatal(err)
					}
					delete(incomplete, "transactionHash")

					for _, node := range nodes {
						if scenario.name == "provider transport error" {
							node.FailHTTP(methods.MethodGetTransactionReceipt, http.StatusServiceUnavailable)
							continue
						}
						var attempt atomic.Int32
						node.Handle(methods.MethodGetTransactionReceipt, func(rpctest.Call) (any, error) {
							if attempt.Add(1) > 1 {
								return mined, nil // Retrying a terminal error would wrongly succeed.
							}
							if scenario.name == "missing receipt field" {
								return incomplete, nil
							}
							if scenario.name == "foreign receipt" {
								return foreign, nil
							}
							return nil, rpctest.RPCError(-32042, "receipt service unavailable")
						})
					}
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					got := invokeSyncWrite(t, entry, ctx, client, raw, &receiptWait)
					if !errors.Is(got.err, scenario.err) || ctx.Err() != nil {
						t.Fatalf("receipt error = %v, want %v before parent expires", got.err, scenario.err)
					}
					wait := got.captured.ReceiptWait
					if wait == nil || wait.LastAttempt == nil || wait.LastAttempt.Sequence != 1 || !errors.Is(wait.LastAttempt.Err, scenario.err) {
						t.Fatalf("terminal attempt missing: %+v", wait)
					}
					if scenario.name == "foreign receipt" || scenario.name == "missing receipt field" {
						if wait.LastObservation == nil || !errors.Is(wait.LastObservation.Err, rpc.ErrMalformedResponse) {
							t.Fatalf("method rejection not retained: %+v", wait)
						}
						for _, source := range wait.LastObservation.Decision.Sources {
							if source.Exchange.Progress != rpc.ExchangeCompleted || source.Answer.Kind != rpc.AnswerResult || source.Answer.Interpretation.State != rpc.InterpretationRejected {
								t.Fatalf("method failure overwrote answer: %+v", source)
							}
						}
					} else if wait.LastObservation != nil {
						t.Fatalf("provider failure fabricated receipt meaning: %+v", wait.LastObservation)
					}
					assertSyncWriteAccepted(t, got.captured, hash, nodes)
					assertSyncBroadcastOnce(t, nodes)
					assertSyncReceiptCallsBound(t, nodes, hash)
					assertSyncMethodCalls(t, nodes, methods.MethodGetTransactionReceipt, 1)
				})
			}
		})
	}
}

func TestSyncWriteCancellationDuringBroadcastAndReceipt(t *testing.T) {
	for _, phase := range []string{methods.MethodSendRawTransaction, methods.MethodGetTransactionReceipt} {
		t.Run(phase, func(t *testing.T) {
			for _, entry := range syncWriteEntrypoints {
				t.Run(entry.name, func(t *testing.T) {
					client, nodes := fleet(t, 1, 0, 3)
					raw, hash := writeTxFixture(t)
					scriptSyncAccepted(nodes, hash)
					guard, stop := context.WithTimeout(context.Background(), 5*time.Second)
					defer stop()
					ctx, cancel := context.WithCancel(guard)
					defer cancel()
					var arrived atomic.Int32
					for _, node := range nodes {
						node.Handle(phase, func(call rpctest.Call) (any, error) {
							// Cancel only after every source has received this phase.
							// Request contexts release every blocked handler on exit.
							if arrived.Add(1) == int32(len(nodes)) {
								cancel()
							}
							<-call.Context.Done()
							return nil, call.Context.Err()
						})
					}
					got := invokeSyncWrite(t, entry, ctx, client, raw, &receiptWait)
					if !errors.Is(got.err, context.Canceled) || arrived.Load() != int32(len(nodes)) {
						t.Fatalf("cancellation = %v, phase arrivals = %d", got.err, arrived.Load())
					}
					assertSyncBroadcastOnce(t, nodes)
					if phase == methods.MethodGetTransactionReceipt {
						assertSyncWriteAccepted(t, got.captured, hash, nodes)
						assertSyncReceiptCallsBound(t, nodes, hash)
						assertSyncMethodCalls(t, nodes, phase, 1)
					} else {
						d := writeDecision(t, got.captured)
						if d.Status != rpc.WriteUnconfirmed || d.Adjudication.Basis != 3 || d.Adjudication.Required != 2 || len(writeUnavailable(d.Sources)) != 3 || len(d.Faults) != 1 {
							t.Fatalf("canceled broadcast evidence = %+v", d)
						}
						assertSyncMethodCalls(t, nodes, methods.MethodGetTransactionReceipt, 0)
					}
				})
			}
		})
	}
}

func TestSyncWriteParentDeadlinePrecedesReceiptTimeout(t *testing.T) {
	zero := time.Duration(0)
	for _, scenario := range []struct {
		name    string
		timeout *time.Duration
	}{
		{"nil default", nil},
		{"zero default", &zero},
		{"explicit longer timeout", &receiptWait},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			for _, entry := range syncWriteEntrypoints {
				t.Run(entry.name, func(t *testing.T) {
					client, nodes := fleet(t, 1, 0, 3)
					raw, hash := writeTxFixture(t)
					scriptSyncAccepted(nodes, hash)
					for _, node := range nodes {
						node.Handle(methods.MethodGetTransactionReceipt, func(call rpctest.Call) (any, error) {
							<-call.Context.Done()
							return nil, call.Context.Err()
						})
					}
					ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
					defer cancel()
					got := invokeSyncWrite(t, entry, ctx, client, raw, scenario.timeout)
					if !errors.Is(got.err, context.DeadlineExceeded) || !errors.Is(ctx.Err(), context.DeadlineExceeded) {
						t.Fatalf("receipt error = %v, parent error = %v", got.err, ctx.Err())
					}
					if errors.Is(got.err, rpc.ErrConflict) || errors.Is(got.err, ethereum.NotFound) {
						t.Fatalf("parent timeout acquired a receipt outcome in its error chain: %v", got.err)
					}
					assertSyncWriteAccepted(t, got.captured, hash, nodes)
					assertSyncBroadcastOnce(t, nodes)
					assertSyncReceiptCallsBound(t, nodes, hash)
					assertSyncMethodCalls(t, nodes, methods.MethodGetTransactionReceipt, 1)
				})
			}
		})
	}
}

func TestSyncWriteReceiptTimeoutStartsAfterBroadcast(t *testing.T) {
	client, nodes := fleet(t, 1, 0, 3)
	raw, hash := writeTxFixture(t)
	// Deliberately hold the broadcast beyond the receipt timeout. The timer
	// measures the contract under test; the barrier synchronizes requests.
	timeout := 100 * time.Millisecond
	broadcastsArrived := make(chan struct{})
	releaseBroadcast := make(chan struct{})
	var arrived atomic.Int32
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i, node := range nodes {
		answer := hash.Hex()
		if i == 2 {
			answer = syncForeignHash().Hex()
		}
		node.Handle(methods.MethodSendRawTransaction, func(call rpctest.Call) (any, error) {
			if arrived.Add(1) == int32(len(nodes)) {
				close(broadcastsArrived)
			}
			select {
			case <-releaseBroadcast:
				return answer, nil
			case <-call.Context.Done():
				return nil, call.Context.Err()
			}
		})
		node.Return(methods.MethodGetTransactionReceipt, syncReceiptFixture(t, hash, 17))
	}
	released := make(chan struct{})
	go func() {
		defer close(released)
		select {
		case <-broadcastsArrived:
		case <-ctx.Done():
			return
		}
		timer := time.NewTimer(2 * timeout)
		defer timer.Stop()
		select {
		case <-timer.C:
			close(releaseBroadcast)
		case <-ctx.Done():
		}
	}()
	defer func() { cancel(); <-released }()
	got := invokeSyncWrite(t, syncWriteEntrypoints[1], ctx, client, raw, &timeout)
	if got.err != nil || got.receipt == nil || got.receipt.TransactionHash != ethapi.Hash(hash.Hex()) {
		t.Fatalf("receipt timeout consumed by broadcast: receipt=%+v error=%v", got.receipt, got.err)
	}
	assertSyncWriteAccepted(t, got.captured, hash, nodes)
	assertSyncBroadcastOnce(t, nodes)
	assertSyncReceiptCallsBound(t, nodes, hash)
}

func TestSyncWriteNegativeTimeoutDoesNotBroadcast(t *testing.T) {
	for _, entry := range syncWriteEntrypoints {
		t.Run(entry.name, func(t *testing.T) {
			client, nodes := fleet(t, 1, 0, 3)
			raw, _ := writeTxFixture(t)
			negative := -time.Second
			got := invokeSyncWrite(t, entry, context.Background(), client, raw, &negative)
			if !errors.Is(got.err, rpc.ErrInvalidRequest) || got.captured.WriteDecision != nil {
				t.Fatalf("negative timeout result = %+v, error = %v", got.captured, got.err)
			}
			assertSyncMethodCalls(t, nodes, methods.MethodSendRawTransaction, 0)
			assertSyncMethodCalls(t, nodes, methods.MethodGetTransactionReceipt, 0)
		})
	}
}

// The root result itself consumes optional evidence, even without a capture.
func TestSyncWriteRootEvidenceWithoutCapture(t *testing.T) {
	client, nodes := fleet(t, 1, 0, 3)
	raw, hash := writeTxFixture(t)
	scriptSyncAccepted(nodes, hash)
	wantRaw := make(map[string]json.RawMessage)
	for _, node := range nodes {
		value := syncReceiptFixture(t, hash, 17)
		wantRaw[node.URL()] = value
		node.ReturnRaw(methods.MethodGetTransactionReceipt, value)
	}
	var original *rpc.WriteDecision
	// These are two steps of one comparison, not independently selectable
	// subtests: the second call must observe the same fleet as the first.
	for i, evidence := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if evidence {
			var err error
			ctx, err = ethguard.WithSourceEvidence(ctx, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
		}
		receipt, result, err := client.SendRawTransactionSync(ctx, raw, nil)
		if err != nil || result.Err != nil || receipt == nil || receipt.TransactionHash != ethapi.Hash(hash.Hex()) {
			t.Fatalf("uncaptured call: receipt=%+v result=%+v error=%v", receipt, result, err)
		}
		if result.ReceiptWait == nil || result.ReceiptWait.LastObservation == nil || result.ReceiptWait.LastObservation.Sequence != 1 || result.ReceiptWait.LastObservation.Err != nil {
			t.Fatalf("uncaptured operation lost receipt: %+v", result.ReceiptWait)
		}
		assertSyncWriteAccepted(t, result, hash, nodes)
		if !evidence {
			original = result.WriteDecision
			if result.Uncertain != nil {
				t.Fatalf("evidence off retained material: %+v", result.Uncertain)
			}
		} else {
			if !reflect.DeepEqual(original, result.WriteDecision) {
				t.Fatalf("evidence opt-in changed WriteDecision: before=%+v after=%+v", original, result.WriteDecision)
			}
			if result.Uncertain == nil || result.Uncertain.Truncated || result.Uncertain.Dropped != 0 {
				t.Fatalf("root direct result did not retain complete material: %+v", result.Uncertain)
			}
			assertSyncRawEvidence(t, result.Uncertain, nodes, hash, wantRaw, 1)
		}
		// The same fleet serves two operations; each adds exactly one
		// broadcast and one receipt request per source.
		assertSyncMethodCalls(t, nodes, methods.MethodSendRawTransaction, i+1)
		assertSyncMethodCalls(t, nodes, methods.MethodGetTransactionReceipt, i+1)
		assertSyncReceiptCallsBound(t, nodes, hash)
	}
}

// The last query may be interrupted after an earlier query formed a real
// conflict. The final timeout must retain both positions without joining errors
// or borrowing any votes from an earlier round.
func TestSyncWriteRetainsObservationBeforeInterruptedAttempt(t *testing.T) {
	for _, entry := range syncWriteEntrypoints {
		for _, budget := range []int{0, 1, 1 << 20} {
			t.Run(fmt.Sprintf("%s/budget=%d", entry.name, budget), func(t *testing.T) {
				client, nodes := fleet(t, 1, 0, 3)
				raw, hash := writeTxFixture(t)
				scriptSyncAccepted(nodes, hash)
				wantNull := make(map[string]json.RawMessage)
				wantConflict := make(map[string]json.RawMessage)
				for i, node := range nodes {
					conflicting := syncReceiptFixture(t, hash, int64(17+i))
					wantNull[node.URL()] = json.RawMessage("null")
					wantConflict[node.URL()] = conflicting
					var attempt atomic.Int32
					node.Handle(methods.MethodGetTransactionReceipt, func(call rpctest.Call) (any, error) {
						switch attempt.Add(1) {
						case 1:
							return nil, nil
						case 2:
							return conflicting, nil
						default:
							<-call.Context.Done()
							return nil, call.Context.Err()
						}
					})
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if budget != 0 {
					var err error
					ctx, err = ethguard.WithSourceEvidence(ctx, budget)
					if err != nil {
						t.Fatal(err)
					}
				}
				timeout := 500 * time.Millisecond
				got := invokeSyncWrite(t, entry, ctx, client, raw, &timeout)
				if !errors.Is(got.err, context.DeadlineExceeded) || errors.Is(got.err, ethereum.NotFound) || errors.Is(got.err, rpc.ErrConflict) {
					t.Fatalf("final conclusion acquired historical errors: %v", got.err)
				}
				wait := got.captured.ReceiptWait
				if wait == nil || wait.LastAttempt == nil || wait.LastObservation == nil {
					t.Fatalf("missing bounded receipt evidence: %+v", wait)
				}
				last, observed := wait.LastAttempt, wait.LastObservation
				if last.Sequence != 3 || observed.Sequence != 2 || !errors.Is(last.Err, context.DeadlineExceeded) || !errors.Is(observed.Err, rpc.ErrConflict) {
					t.Fatalf("query conclusions lost: last=%+v observation=%+v", last, observed)
				}
				if observed.Decision.Adjudication.Reason != rpc.ReasonConflictingCandidates || len(observed.Decision.Adjudication.Candidates) != 3 || observed.Decision.Status != rpc.Unknown {
					t.Fatalf("actual disagreement lost: %+v", observed.Decision)
				}
				if last.Decision == nil || last.Decision.Adjudication.Reason != rpc.ReasonUnassessed || len(last.Decision.Adjudication.Candidates) != 0 || len(last.Decision.Support) != 0 || len(last.Decision.Reject) != 0 {
					t.Fatalf("interrupted query inherited votes: %+v", last.Decision)
				}
				for _, source := range last.Decision.Sources {
					if source.Answer.Interpretation.State != rpc.InterpretationUnassessed || source.Relation == rpc.VerdictSupported || source.Relation == rpc.VerdictDissented {
						t.Fatalf("interrupted source inherited judgment: %+v", source)
					}
				}
				material := got.captured.Uncertain
				if budget == 0 && material != nil {
					t.Fatal("default facts required material capture")
				}
				if budget != 0 && (material == nil || material.Limit != budget || material.Bytes > budget) {
					t.Fatalf("source-evidence budget = %+v", material)
				}
				if budget == 1 && (!material.Truncated || material.Dropped == 0) {
					t.Fatal("tiny material budget was not exhausted")
				}
				if budget > 1 {
					if material.Truncated || material.Dropped != 0 {
						t.Fatalf("bounded interrupted observation unexpectedly truncated: %+v", material)
					}
					successful, responseTimeouts, collectTimeouts := assertSyncTimeoutRawEvidence(t, material, nodes, last, nil)
					if responseTimeouts != len(nodes) || collectTimeouts != 1 {
						t.Fatalf("terminal raw timeouts: sources=%d, collection=%d", responseTimeouts, collectTimeouts)
					}
					// Both completed rounds retain their own original bytes. The
					// broadcast belongs to the operation and is checked in each view.
					first, second := *successful, *successful
					first.Raw, second.Raw = nil, nil
					for _, item := range successful.Raw {
						if item.Method == methods.MethodGetTransactionReceipt && string(item.Value) == "null" {
							first.Raw = append(first.Raw, item)
						} else {
							second.Raw = append(second.Raw, item)
						}
						if item.Method == methods.MethodSendRawTransaction {
							first.Raw = append(first.Raw, item)
						}
					}
					assertSyncRawEvidence(t, &first, nodes, hash, wantNull, 1)
					assertSyncRawEvidence(t, &second, nodes, hash, wantConflict, 1)
					if len(first.Raw) != 2*len(nodes) || len(second.Raw) != 2*len(nodes) {
						t.Fatalf("completed raw rounds repeated: first=%d, second=%d", len(first.Raw), len(second.Raw))
					}
				}
				assertSyncWriteAccepted(t, got.captured, hash, nodes)
				assertSyncBroadcastOnce(t, nodes)
				assertSyncReceiptCallsBound(t, nodes, hash)
				assertSyncMethodCalls(t, nodes, methods.MethodGetTransactionReceipt, 3)
			})
		}
	}
}

func TestSyncWriteRetainsInsufficientSupportWithoutDissent(t *testing.T) {
	for _, entry := range syncWriteEntrypoints {
		t.Run(entry.name, func(t *testing.T) {
			client, nodes := fleet(t, 1, 0, 3)
			raw, hash := writeTxFixture(t)
			scriptSyncAccepted(nodes, hash)
			for i, node := range nodes {
				mined := syncReceiptFixture(t, hash, 17)
				var attempt atomic.Int32
				node.Handle(methods.MethodGetTransactionReceipt, func(call rpctest.Call) (any, error) {
					if attempt.Add(1) == 1 {
						if i == 0 {
							return mined, nil
						}
						return nil, rpctest.RPCError(-32601, "receipt method unavailable")
					}
					<-call.Context.Done()
					return nil, call.Context.Err()
				})
			}
			timeout := 300 * time.Millisecond
			got := invokeSyncWrite(t, entry, context.Background(), client, raw, &timeout)
			if !errors.Is(got.err, context.DeadlineExceeded) || errors.Is(got.err, rpc.ErrInsufficientSupport) {
				t.Fatalf("insufficient support was not retried independently: %v", got.err)
			}
			wait := got.captured.ReceiptWait
			if wait == nil || wait.LastObservation == nil || wait.LastAttempt == nil || wait.LastObservation.Sequence != 1 || wait.LastAttempt.Sequence != 2 {
				t.Fatalf("lost insufficient-support observation: %+v", wait)
			}
			observed := wait.LastObservation
			if !errors.Is(observed.Err, rpc.ErrInsufficientSupport) || errors.Is(observed.Err, rpc.ErrConflict) || observed.Decision.Adjudication.Reason != rpc.ReasonInsufficientSupport || len(observed.Decision.Adjudication.Candidates) != 1 || len(observed.Decision.Support) != 1 || len(observed.Decision.Reject) != 0 {
				t.Fatalf("support shortfall became disagreement: %+v", observed)
			}
			assertSyncBroadcastOnce(t, nodes)
			assertSyncMethodCalls(t, nodes, methods.MethodGetTransactionReceipt, 2)
		})
	}
}

func TestSyncWriteOwnerClosePreservesEarlierObservation(t *testing.T) {
	for _, entry := range syncWriteEntrypoints {
		t.Run(entry.name, func(t *testing.T) {
			client, nodes := fleet(t, 1, 0, 3)
			raw, hash := writeTxFixture(t)
			scriptSyncAccepted(nodes, hash)
			inFlight := make(chan struct{})
			var arrived atomic.Int32
			for _, node := range nodes {
				var attempt atomic.Int32
				node.Handle(methods.MethodGetTransactionReceipt, func(call rpctest.Call) (any, error) {
					if attempt.Add(1) == 1 {
						return nil, nil
					}
					if arrived.Add(1) == int32(len(nodes)) {
						close(inFlight)
					}
					<-call.Context.Done()
					return nil, call.Context.Err()
				})
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			closed := make(chan struct{})
			go func() {
				defer close(closed)
				select {
				case <-inFlight:
					client.Close()
				case <-ctx.Done():
				}
			}()
			got := invokeSyncWrite(t, entry, ctx, client, raw, &receiptWait)
			<-closed
			if !errors.Is(got.err, ethguard.ErrPoolClosed) || errors.Is(got.err, ethereum.NotFound) || ctx.Err() != nil {
				t.Fatalf("owner termination lost identity or acquired history: %v", got.err)
			}
			wait := got.captured.ReceiptWait
			if wait == nil || wait.LastAttempt == nil || wait.LastObservation == nil || wait.LastAttempt.Sequence != 2 || wait.LastObservation.Sequence != 1 || wait.LastAttempt.Err == nil || !errors.Is(wait.LastObservation.Err, ethereum.NotFound) {
				t.Fatalf("close lost receipt positions: %+v", wait)
			}
			if wait.LastAttempt.Decision.Adjudication.Reason != rpc.ReasonUnassessed || len(wait.LastAttempt.Decision.Support) != 0 || wait.LastObservation.Decision.Adjudication.Reason != rpc.ReasonAccepted {
				t.Fatalf("close mixed query facts: %+v", wait)
			}
			assertSyncWriteAccepted(t, got.captured, hash, nodes)
			assertSyncBroadcastOnce(t, nodes)
			assertSyncMethodCalls(t, nodes, methods.MethodGetTransactionReceipt, 2)
		})
	}
}
