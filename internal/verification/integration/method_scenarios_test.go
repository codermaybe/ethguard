package ethguard_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"

	ethereum "github.com/ethereum/go-ethereum"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
)

// Every test below owns its scenario rows and invokes each one explicitly:
// status, membership, faults, representative, Value, typed projection,
// dimensions, and request shape are written per row. There is no matrix loop,
// no method dispatch, and no shared scenario list.

func TestVerificationMethodScenarioChainID(t *testing.T) {
	row := t09FieldRowFor(t, methods.MethodChainID)
	a, b := row.variants[0], row.other
	mc := t09MethodCase{Method: row.method, Typed: t09TypedChainID, TypedWant: t09TypedWantBig}
	t09CheckScenario(t, mc, t09Row{Scenario: "all-agree", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3, 4}, Representative: 0, ValueFrom: 0, ValueExact: `"0x1"`, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "minority-dissent", Answers: []json.RawMessage{a, a, a, b, b}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2}, Reject: []int{3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "outlier", Answers: []json.RawMessage{a, a, a, a, b}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3}, Reject: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "single-fault", Answers: t09Repeat(a, t09MatrixN), Faults: []int{4}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3}, Representative: 0, Faults: 1, SourceFaults: []int{4}, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "all-fault", Faults: []int{0, 1, 2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{ethguard.ErrAllSourcesDown}, Participants: 5, Faults: 6, SourceFaults: []int{0, 1, 2, 3, 4}, Representative: -1, ValueFrom: -1, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "conflict", Answers: []json.RawMessage{a, a, b, b, a}, Faults: []int{4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrConflict}, ErrText: "required support 3, winning cluster 2 of 5", Participants: 5, Support: []int{0, 1}, Reject: []int{2, 3}, Faults: 1, SourceFaults: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "notfound", Answers: t09Repeat(json.RawMessage(`null`), t09MatrixN), Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrNoValidResponse}, Participants: 5, Faults: 5, Representative: -1, ValueFrom: -1, TypedFrom: -1, TypedErr: rpc.ErrNoValidResponse}})
	t09CheckScenario(t, mc, t09Row{Scenario: "pending-local", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0, CheckNoPending: true, Note: "eth_chainId has no block parameter, so no pending form exists"}})
	t09CheckScenario(t, mc, t09Row{Scenario: "quorum-boundary", Answers: t09Repeat(a, t09MatrixN), Faults: []int{2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrConflict}, ErrText: "required support 3, winning cluster 2 of 5", Participants: 5, Support: []int{0, 1}, Faults: 3, SourceFaults: []int{2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: -1}})
}

func TestVerificationMethodScenarioGetBlockByHash(t *testing.T) {
	row := t09FieldRowFor(t, methods.MethodGetBlockByHash)
	a, b := row.variants[0], row.other
	null := json.RawMessage(`null`)
	mc := t09MethodCase{Method: row.method, Params: row.params, Typed: t09TypedBlockByHash, TypedWant: t09TypedWantBlock}
	t09CheckScenario(t, mc, t09Row{Scenario: "all-agree", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "minority-dissent", Answers: []json.RawMessage{a, a, a, b, b}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2}, Faults: 2, SourceFaults: []int{3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0, Note: "a different block cannot satisfy the requested hash; the dissent is an admission fault, not a legal Reject"}})
	t09CheckScenario(t, mc, t09Row{Scenario: "outlier", Answers: []json.RawMessage{a, a, a, a, b}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3}, Faults: 1, SourceFaults: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "single-fault", Answers: t09Repeat(a, t09MatrixN), Faults: []int{4}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3}, Faults: 1, SourceFaults: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "all-fault", Faults: []int{0, 1, 2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{ethguard.ErrAllSourcesDown}, Participants: 5, Faults: 6, SourceFaults: []int{0, 1, 2, 3, 4}, Representative: -1, ValueFrom: -1, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "conflict", Answers: []json.RawMessage{a, a, null, null, a}, Faults: []int{4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrConflict}, ErrText: "required support 3, winning cluster 2 of 5", Participants: 5, Support: []int{0, 1}, Reject: []int{2, 3}, Faults: 1, SourceFaults: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: -1, Note: "the requested block and corroborated absence are the two legal answers"}})
	t09CheckScenario(t, mc, t09Row{Scenario: "notfound", Answers: t09Repeat(null, t09MatrixN), Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: -1, TypedErr: ethereum.NotFound}})
	t09CheckScenario(t, mc, t09Row{Scenario: "pending-local", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0, CheckNoPending: true, Note: "hash-addressed read has no pending form"}})
	t09CheckScenario(t, mc, t09Row{Scenario: "quorum-boundary", Answers: t09Repeat(a, t09MatrixN), Faults: []int{2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrConflict}, ErrText: "required support 3, winning cluster 2 of 5", Participants: 5, Support: []int{0, 1}, Faults: 3, SourceFaults: []int{2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: -1}})
}

func TestVerificationMethodScenarioGetBlockByNumber(t *testing.T) {
	row := t09FieldRowFor(t, methods.MethodGetBlockByNumber)
	a, b := row.variants[0], row.other
	null := json.RawMessage(`null`)
	mc := t09MethodCase{Method: row.method, Params: row.params, Typed: t09TypedBlockByNumber, TypedWant: t09TypedWantBlock}
	t09CheckScenario(t, mc, t09Row{Scenario: "all-agree", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "minority-dissent", Answers: []json.RawMessage{a, a, a, b, b}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2}, Reject: []int{3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "outlier", Answers: []json.RawMessage{a, a, a, a, b}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3}, Reject: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "single-fault", Answers: t09Repeat(a, t09MatrixN), Faults: []int{4}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3}, Faults: 1, SourceFaults: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "all-fault", Faults: []int{0, 1, 2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{ethguard.ErrAllSourcesDown}, Participants: 5, Faults: 6, SourceFaults: []int{0, 1, 2, 3, 4}, Representative: -1, ValueFrom: -1, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "conflict", Answers: []json.RawMessage{a, a, b, b, a}, Faults: []int{4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrConflict}, ErrText: "required support 3, winning cluster 2 of 5", Participants: 5, Support: []int{0, 1}, Reject: []int{2, 3}, Faults: 1, SourceFaults: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "notfound", Answers: t09Repeat(null, t09MatrixN), Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: -1, TypedErr: ethereum.NotFound}})
	t09CheckScenario(t, mc, t09Row{Scenario: "pending-local", Params: []any{"pending", true}, Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0}, Representative: -1, ValueFrom: 0, TypedFrom: 0, TypedWantHeader: true, PreparedPending: true, Note: "pending stays node-local; the typed adapter rejects -1 while the root CallInto reads one header"}})
	t09CheckScenario(t, mc, t09Row{Scenario: "quorum-boundary", Answers: t09Repeat(a, t09MatrixN), Faults: []int{2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrConflict}, ErrText: "required support 3, winning cluster 2 of 5", Participants: 5, Support: []int{0, 1}, Faults: 3, SourceFaults: []int{2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: -1}})
}

func TestVerificationMethodScenarioGetBalance(t *testing.T) {
	row := t09FieldRowFor(t, methods.MethodGetBalance)
	a, b := row.variants[0], row.other
	// A conflict value whose canonical spelling sorts after a, so the 2|2
	// tie-break is predictable and the a-cluster is the one retained.
	conflictOther := json.RawMessage(`"0x65"`)
	mc := t09MethodCase{Method: row.method, Params: row.params, Typed: t09TypedBalance, TypedWant: t09TypedWantBig}
	t09CheckScenario(t, mc, t09Row{Scenario: "all-agree", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "minority-dissent", Answers: []json.RawMessage{a, a, a, b, b}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2}, Reject: []int{3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "outlier", Answers: []json.RawMessage{a, a, a, a, b}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3}, Reject: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "single-fault", Answers: t09Repeat(a, t09MatrixN), Faults: []int{4}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3}, Faults: 1, SourceFaults: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "all-fault", Faults: []int{0, 1, 2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{ethguard.ErrAllSourcesDown}, Participants: 5, Faults: 6, SourceFaults: []int{0, 1, 2, 3, 4}, Representative: -1, ValueFrom: -1, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "conflict", Answers: []json.RawMessage{a, a, conflictOther, conflictOther, a}, Faults: []int{4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrConflict}, ErrText: "required support 3, winning cluster 2 of 5", Participants: 5, Support: []int{0, 1}, Reject: []int{2, 3}, Faults: 1, SourceFaults: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "notfound", Answers: t09Repeat(json.RawMessage(`null`), t09MatrixN), Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrNoValidResponse}, Participants: 5, Faults: 5, Representative: -1, ValueFrom: -1, TypedFrom: -1, TypedErr: rpc.ErrNoValidResponse}})
	t09CheckScenario(t, mc, t09Row{Scenario: "pending-local", Params: []any{t09Address, "pending"}, Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0}, Representative: -1, ValueFrom: 0, TypedFrom: 0, PreparedPending: true, Note: "no adapter method expresses pending; the production CallInto path decodes the selected value"}})
	t09CheckScenario(t, mc, t09Row{Scenario: "quorum-boundary", Answers: t09Repeat(a, t09MatrixN), Faults: []int{2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrConflict}, ErrText: "required support 3, winning cluster 2 of 5", Participants: 5, Support: []int{0, 1}, Faults: 3, SourceFaults: []int{2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: -1}})
}

func TestVerificationMethodScenarioGetTransactionReceipt(t *testing.T) {
	row := t09FieldRowFor(t, methods.MethodGetTransactionReceipt)
	a, b := row.variants[0], row.other
	// gasUsed sorts after the canonical answer, so a 2|2 tie retains the
	// a-cluster deterministically.
	b = t09SetField(t, b, "gasUsed", "0x9999")
	mc := t09MethodCase{Method: row.method, Params: row.params, Typed: t09TypedReceipt, TypedWant: t09TypedWantReceipt}
	t09CheckScenario(t, mc, t09Row{Scenario: "all-agree", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "minority-dissent", Answers: []json.RawMessage{a, a, a, b, b}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2}, Reject: []int{3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "outlier", Answers: []json.RawMessage{a, a, a, a, b}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3}, Reject: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "single-fault", Answers: t09Repeat(a, t09MatrixN), Faults: []int{4}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3}, Faults: 1, SourceFaults: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "all-fault", Faults: []int{0, 1, 2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{ethguard.ErrAllSourcesDown}, Participants: 5, Faults: 6, SourceFaults: []int{0, 1, 2, 3, 4}, Representative: -1, ValueFrom: -1, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "conflict", Answers: []json.RawMessage{a, a, b, b, a}, Faults: []int{4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrConflict}, ErrText: "required support 3, winning cluster 2 of 5", Participants: 5, Support: []int{0, 1}, Reject: []int{2, 3}, Faults: 1, SourceFaults: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "notfound", Answers: t09Repeat(json.RawMessage(`null`), t09MatrixN), Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: -1, TypedErr: ethereum.NotFound}})
	t09CheckScenario(t, mc, t09Row{Scenario: "pending-local", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0, CheckNoPending: true, Note: "receipt is addressed by transaction hash; no pending block parameter exists"}})
	t09CheckScenario(t, mc, t09Row{Scenario: "quorum-boundary", Answers: t09Repeat(a, t09MatrixN), Faults: []int{2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrConflict}, ErrText: "required support 3, winning cluster 2 of 5", Participants: 5, Support: []int{0, 1}, Faults: 3, SourceFaults: []int{2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: -1}})
}

func TestVerificationMethodScenarioGetTransactionByHash(t *testing.T) {
	chain := t09NewChain(t)
	row := t09FieldRowFor(t, methods.MethodGetTransactionByHash)
	a, b := row.variants[0], row.other
	pending := chain.txObject(false, "", 0, 0)
	mc := t09MethodCase{Method: row.method, Params: row.params, Typed: t09TypedTransaction, TypedWant: t09TypedWantTransaction}
	t09CheckScenario(t, mc, t09Row{Scenario: "all-agree", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "minority-dissent", Answers: []json.RawMessage{a, a, a, b, b}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2}, Reject: []int{3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "outlier", Answers: []json.RawMessage{a, a, a, a, b}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3}, Reject: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "single-fault", Answers: t09Repeat(a, t09MatrixN), Faults: []int{4}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3}, Faults: 1, SourceFaults: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "all-fault", Faults: []int{0, 1, 2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{ethguard.ErrAllSourcesDown}, Participants: 5, Faults: 6, SourceFaults: []int{0, 1, 2, 3, 4}, Representative: -1, ValueFrom: -1, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "conflict", Answers: []json.RawMessage{a, a, b, b, a}, Faults: []int{4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrConflict}, ErrText: "required support 3, winning cluster 2 of 5", Participants: 5, Support: []int{0, 1}, Reject: []int{2, 3}, Faults: 1, SourceFaults: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "notfound", Answers: t09Repeat(json.RawMessage(`null`), t09MatrixN), Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: -1, TypedErr: ethereum.NotFound}})
	t09CheckScenario(t, mc, t09Row{Scenario: "pending-local", Params: row.params, Answers: t09Repeat(pending, t09MatrixN), Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0, Note: "a pending transaction is still content-addressed and corroborated; the typed adapter reports pending=true"}})
	t09CheckScenario(t, mc, t09Row{Scenario: "quorum-boundary", Answers: t09Repeat(a, t09MatrixN), Faults: []int{2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrConflict}, ErrText: "required support 3, winning cluster 2 of 5", Participants: 5, Support: []int{0, 1}, Faults: 3, SourceFaults: []int{2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: -1}})
}

func TestVerificationMethodScenarioGetBlockReceipts(t *testing.T) {
	row := t09FieldRowFor(t, methods.MethodGetBlockReceipts)
	a, b := row.variants[0], row.other
	// gasUsed sorts after the canonical answer, so a 2|2 tie retains the
	// a-cluster deterministically.
	b = t09SetNestedField(t, b, 0, "gasUsed", "0x9999")
	mc := t09MethodCase{Method: row.method, Params: row.params, Typed: t09TypedBlockReceipts, TypedWant: t09TypedWantReceipts}
	t09CheckScenario(t, mc, t09Row{Scenario: "all-agree", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "minority-dissent", Answers: []json.RawMessage{a, a, a, b, b}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2}, Reject: []int{3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "outlier", Answers: []json.RawMessage{a, a, a, a, b}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3}, Reject: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "single-fault", Answers: t09Repeat(a, t09MatrixN), Faults: []int{4}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3}, Faults: 1, SourceFaults: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "all-fault", Faults: []int{0, 1, 2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{ethguard.ErrAllSourcesDown}, Participants: 5, Faults: 6, SourceFaults: []int{0, 1, 2, 3, 4}, Representative: -1, ValueFrom: -1, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "conflict", Answers: []json.RawMessage{a, a, b, b, a}, Faults: []int{4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrConflict}, ErrText: "required support 3, winning cluster 2 of 5", Participants: 5, Support: []int{0, 1}, Reject: []int{2, 3}, Faults: 1, SourceFaults: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "notfound", Answers: t09Repeat(json.RawMessage(`null`), t09MatrixN), Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: -1, TypedErr: ethereum.NotFound}})
	t09CheckScenario(t, mc, t09Row{Scenario: "pending-local", Params: []any{"pending"}, Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrInvalidRequest}, Participants: 0, Faults: 0, Representative: -1, ValueFrom: -1, TypedFrom: -1, NoProvider: true, Note: "rejected locally before any provider or anchor call"}})
	t09CheckScenario(t, mc, t09Row{Scenario: "quorum-boundary", Answers: t09Repeat(a, t09MatrixN), Faults: []int{2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrConflict}, ErrText: "required support 3, winning cluster 2 of 5", Participants: 5, Support: []int{0, 1}, Faults: 3, SourceFaults: []int{2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: -1}})
}

func TestVerificationMethodScenarioGetTransactionCount(t *testing.T) {
	row := t09FieldRowFor(t, methods.MethodGetTransactionCount)
	a, b := row.variants[0], row.other
	mc := t09MethodCase{Method: row.method, Params: row.params, Typed: t09TypedNonce, TypedWant: t09TypedWantQuantity}
	t09CheckScenario(t, mc, t09Row{Scenario: "all-agree", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "minority-dissent", Answers: []json.RawMessage{a, a, a, b, b}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2}, Reject: []int{3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "outlier", Answers: []json.RawMessage{a, a, a, a, b}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3}, Reject: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "single-fault", Answers: t09Repeat(a, t09MatrixN), Faults: []int{4}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3}, Faults: 1, SourceFaults: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "all-fault", Faults: []int{0, 1, 2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{ethguard.ErrAllSourcesDown}, Participants: 5, Faults: 6, SourceFaults: []int{0, 1, 2, 3, 4}, Representative: -1, ValueFrom: -1, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "conflict", Answers: []json.RawMessage{a, a, b, b, a}, Faults: []int{4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrConflict}, ErrText: "required support 3, winning cluster 2 of 5", Participants: 5, Support: []int{0, 1}, Reject: []int{2, 3}, Faults: 1, SourceFaults: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "notfound", Answers: t09Repeat(json.RawMessage(`null`), t09MatrixN), Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrNoValidResponse}, Participants: 5, Faults: 5, Representative: -1, ValueFrom: -1, TypedFrom: -1, TypedErr: rpc.ErrNoValidResponse}})
	t09CheckScenario(t, mc, t09Row{Scenario: "pending-local", Params: []any{t09Address, "pending"}, Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0}, Representative: -1, ValueFrom: 0, TypedFrom: 0, PreparedPending: true}})
	t09CheckScenario(t, mc, t09Row{Scenario: "quorum-boundary", Answers: t09Repeat(a, t09MatrixN), Faults: []int{2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrConflict}, ErrText: "required support 3, winning cluster 2 of 5", Participants: 5, Support: []int{0, 1}, Faults: 3, SourceFaults: []int{2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: -1}})
}

func TestVerificationMethodScenarioGetStorageAt(t *testing.T) {
	row := t09FieldRowFor(t, methods.MethodGetStorageAt)
	a, b := row.variants[0], row.other
	mc := t09MethodCase{Method: row.method, Params: row.params, Typed: t09TypedStorage, TypedWant: t09TypedWantBytes}
	t09CheckScenario(t, mc, t09Row{Scenario: "all-agree", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "minority-dissent", Answers: []json.RawMessage{a, a, a, b, b}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2}, Reject: []int{3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "outlier", Answers: []json.RawMessage{a, a, a, a, b}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3}, Reject: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "single-fault", Answers: t09Repeat(a, t09MatrixN), Faults: []int{4}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3}, Faults: 1, SourceFaults: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "all-fault", Faults: []int{0, 1, 2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{ethguard.ErrAllSourcesDown}, Participants: 5, Faults: 6, SourceFaults: []int{0, 1, 2, 3, 4}, Representative: -1, ValueFrom: -1, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "conflict", Answers: []json.RawMessage{a, a, b, b, a}, Faults: []int{4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrConflict}, ErrText: "required support 3, winning cluster 2 of 5", Participants: 5, Support: []int{0, 1}, Reject: []int{2, 3}, Faults: 1, SourceFaults: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "notfound", Answers: t09Repeat(json.RawMessage(`null`), t09MatrixN), Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrNoValidResponse}, Participants: 5, Faults: 5, Representative: -1, ValueFrom: -1, TypedFrom: -1, TypedErr: rpc.ErrNoValidResponse}})
	t09CheckScenario(t, mc, t09Row{Scenario: "pending-local", Params: []any{t09Address, t09StorageKey, "pending"}, Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0}, Representative: -1, ValueFrom: 0, TypedFrom: 0, PreparedPending: true}})
	t09CheckScenario(t, mc, t09Row{Scenario: "quorum-boundary", Answers: t09Repeat(a, t09MatrixN), Faults: []int{2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrConflict}, ErrText: "required support 3, winning cluster 2 of 5", Participants: 5, Support: []int{0, 1}, Faults: 3, SourceFaults: []int{2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: -1}})
}

func TestVerificationMethodScenarioGetCode(t *testing.T) {
	row := t09FieldRowFor(t, methods.MethodGetCode)
	a, b := row.variants[0], row.other
	mc := t09MethodCase{Method: row.method, Params: row.params, Typed: t09TypedCode, TypedWant: t09TypedWantBytes}
	t09CheckScenario(t, mc, t09Row{Scenario: "all-agree", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "minority-dissent", Answers: []json.RawMessage{a, a, a, b, b}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2}, Reject: []int{3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "outlier", Answers: []json.RawMessage{a, a, a, a, b}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3}, Reject: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "single-fault", Answers: t09Repeat(a, t09MatrixN), Faults: []int{4}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3}, Faults: 1, SourceFaults: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "all-fault", Faults: []int{0, 1, 2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{ethguard.ErrAllSourcesDown}, Participants: 5, Faults: 6, SourceFaults: []int{0, 1, 2, 3, 4}, Representative: -1, ValueFrom: -1, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "conflict", Answers: []json.RawMessage{a, a, b, b, a}, Faults: []int{4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrConflict}, ErrText: "required support 3, winning cluster 2 of 5", Participants: 5, Support: []int{0, 1}, Reject: []int{2, 3}, Faults: 1, SourceFaults: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "notfound", Answers: t09Repeat(json.RawMessage(`null`), t09MatrixN), Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrNoValidResponse}, Participants: 5, Faults: 5, Representative: -1, ValueFrom: -1, TypedFrom: -1, TypedErr: rpc.ErrNoValidResponse}})
	t09CheckScenario(t, mc, t09Row{Scenario: "pending-local", Params: []any{t09Address, "pending"}, Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0}, Representative: -1, ValueFrom: 0, TypedFrom: 0, PreparedPending: true}})
	t09CheckScenario(t, mc, t09Row{Scenario: "quorum-boundary", Answers: t09Repeat(a, t09MatrixN), Faults: []int{2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrConflict}, ErrText: "required support 3, winning cluster 2 of 5", Participants: 5, Support: []int{0, 1}, Faults: 3, SourceFaults: []int{2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: -1}})
}

func TestVerificationMethodScenarioCall(t *testing.T) {
	row := t09FieldRowFor(t, methods.MethodCall)
	a := row.variants[0]
	// Sorts after the canonical answer, so a 2|2 tie retains the a-cluster.
	b := json.RawMessage(`"0xdeadbf00"`)
	exec := func(support, reject []int, returned bool) []t09DimensionWant {
		return []t09DimensionWant{{Name: "execution", Support: support, Reject: reject, Returned: returned}}
	}
	mc := t09MethodCase{Method: row.method, Params: row.params, Typed: t09TypedCall, TypedWant: t09TypedWantBytes}
	t09CheckScenario(t, mc, t09Row{Scenario: "all-agree", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0, Dimensions: exec([]int{0, 1, 2, 3, 4}, nil, true)}})
	t09CheckScenario(t, mc, t09Row{Scenario: "minority-dissent", Answers: []json.RawMessage{a, a, a, b, b}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2}, Reject: []int{3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0, Dimensions: exec([]int{0, 1, 2}, []int{3, 4}, true)}})
	t09CheckScenario(t, mc, t09Row{Scenario: "outlier", Answers: []json.RawMessage{a, a, a, a, b}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3}, Reject: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0, Dimensions: exec([]int{0, 1, 2, 3}, []int{4}, true)}})
	t09CheckScenario(t, mc, t09Row{Scenario: "single-fault", Answers: t09Repeat(a, t09MatrixN), Faults: []int{4}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3}, Faults: 1, SourceFaults: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0, Dimensions: exec([]int{0, 1, 2, 3}, nil, true)}})
	t09CheckScenario(t, mc, t09Row{Scenario: "all-fault", Faults: []int{0, 1, 2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{ethguard.ErrAllSourcesDown}, Participants: 5, Faults: 6, SourceFaults: []int{0, 1, 2, 3, 4}, Representative: -1, ValueFrom: -1, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "conflict", Answers: []json.RawMessage{a, a, b, b, a}, Faults: []int{4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrConflict}, ErrText: "required support 3, winning execution 2 of 5", Participants: 5, Support: []int{0, 1}, Reject: []int{2, 3}, Faults: 1, SourceFaults: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: -1, Dimensions: exec([]int{0, 1}, []int{2, 3}, false)}})
	t09CheckScenario(t, mc, t09Row{Scenario: "notfound", Answers: t09Repeat(json.RawMessage(`null`), t09MatrixN), Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrNoValidResponse}, Participants: 5, Faults: 5, Representative: -1, ValueFrom: -1, TypedFrom: -1, TypedErr: rpc.ErrNoValidResponse}})
	t09CheckScenario(t, mc, t09Row{Scenario: "pending-local", Params: []any{map[string]any{"to": t09Address}, "pending"}, Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0}, Representative: -1, ValueFrom: 0, TypedFrom: 0, PreparedPending: true}})
	t09CheckScenario(t, mc, t09Row{Scenario: "quorum-boundary", Answers: t09Repeat(a, t09MatrixN), Faults: []int{2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrConflict}, ErrText: "required support 3, winning execution 2 of 5", Participants: 5, Support: []int{0, 1}, Faults: 3, SourceFaults: []int{2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: -1, Dimensions: exec([]int{0, 1}, nil, false)}})
}

func TestVerificationMethodScenarioEstimateGas(t *testing.T) {
	row := t09FieldRowFor(t, methods.MethodEstimateGas)
	a, b, c := row.variants[0], row.other, json.RawMessage(`"0x520a"`)
	mc := t09MethodCase{Method: row.method, Params: row.params, Typed: t09TypedEstimateGas, TypedWant: t09TypedWantQuantity}
	t09CheckScenario(t, mc, t09Row{Scenario: "all-agree", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0, 1, 2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "minority-dissent", Answers: []json.RawMessage{a, a, a, b, b}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0, 1, 2}, Reject: []int{3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "outlier", Answers: []json.RawMessage{a, a, a, a, b}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0, 1, 2, 3}, Reject: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "single-fault", Answers: t09Repeat(a, t09MatrixN), Faults: []int{4}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0, 1, 2, 3}, Faults: 1, SourceFaults: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "all-fault", Faults: []int{0, 1, 2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{ethguard.ErrAllSourcesDown}, Participants: 5, Faults: 6, SourceFaults: []int{0, 1, 2, 3, 4}, Representative: -1, ValueFrom: -1, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "conflict", Answers: []json.RawMessage{a, a, b, b, c}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{2, 3}, Reject: []int{0, 1, 4}, Representative: 2, ValueFrom: 2, TypedFrom: 2, Note: "lower median of five samples is the middle value, not the modal value"}})
	t09CheckScenario(t, mc, t09Row{Scenario: "notfound", Answers: t09Repeat(json.RawMessage(`null`), t09MatrixN), Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrNoValidResponse}, Participants: 5, Faults: 5, Representative: -1, ValueFrom: -1, TypedFrom: -1, TypedErr: rpc.ErrNoValidResponse}})
	t09CheckScenario(t, mc, t09Row{Scenario: "pending-local", Params: []any{map[string]any{"to": t09Address}, "pending"}, Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0}, Representative: -1, ValueFrom: 0, TypedFrom: 0, PreparedPending: true}})
	t09CheckScenario(t, mc, t09Row{Scenario: "quorum-boundary", Answers: t09Repeat(a, t09MatrixN), Faults: []int{2, 3, 4}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0, 1}, Faults: 3, SourceFaults: []int{2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0, Note: "median is not gated by the corroboration floor"}})
}

func TestVerificationMethodScenarioGetLogs(t *testing.T) {
	row := t09FieldRowFor(t, methods.MethodGetLogs)
	a, b := row.variants[0], row.other
	// The data word sorts after the canonical answer, so a 2|2 tie retains
	// the a-cluster deterministically.
	b = t09SetNestedField(t, b, 0, "data", "0x0100")
	mc := t09MethodCase{Method: row.method, Params: row.params, Typed: t09TypedLogs, TypedWant: t09TypedWantLogs}
	t09CheckScenario(t, mc, t09Row{Scenario: "all-agree", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "minority-dissent", Answers: []json.RawMessage{a, a, a, b, b}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2}, Reject: []int{3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "outlier", Answers: []json.RawMessage{a, a, a, a, b}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3}, Reject: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "single-fault", Answers: t09Repeat(a, t09MatrixN), Faults: []int{4}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3}, Faults: 1, SourceFaults: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "all-fault", Faults: []int{0, 1, 2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{ethguard.ErrAllSourcesDown}, Participants: 5, Faults: 6, SourceFaults: []int{0, 1, 2, 3, 4}, Representative: -1, ValueFrom: -1, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "conflict", Answers: []json.RawMessage{a, a, b, b, a}, Faults: []int{4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrConflict}, ErrText: "required support 3, winning cluster 2 of 5", Participants: 5, Support: []int{0, 1}, Reject: []int{2, 3}, Faults: 1, SourceFaults: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "notfound", Answers: t09Repeat(json.RawMessage(`[]`), t09MatrixN), Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0, Note: "an empty array is a legal answer; JSON null is malformed and is covered separately"}})
	t09CheckScenario(t, mc, t09Row{Scenario: "pending-local", Params: []any{map[string]any{"fromBlock": "pending", "toBlock": "0x10"}}, Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrInvalidRequest}, Participants: 0, Faults: 0, Representative: -1, ValueFrom: -1, TypedFrom: -1, NoProvider: true, Note: "range pending is rejected locally before any provider or anchor call"}})
	t09CheckScenario(t, mc, t09Row{Scenario: "quorum-boundary", Answers: t09Repeat(a, t09MatrixN), Faults: []int{2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrConflict}, ErrText: "required support 3, winning cluster 2 of 5", Participants: 5, Support: []int{0, 1}, Faults: 3, SourceFaults: []int{2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: -1}})
}

func TestVerificationMethodScenarioFeeHistory(t *testing.T) {
	row := t09FieldRowFor(t, methods.MethodFeeHistory)
	a, b := row.variants[0], row.other
	// The ratio sorts after the canonical answer, so a 2|2 tie retains the
	// a-cluster deterministically.
	b = t09SetField(t, b, "gasUsedRatio", []float64{0.75, 0.25})
	mc := t09MethodCase{Method: row.method, Params: row.params, Typed: t09TypedFeeHistory, TypedWant: t09TypedWantFee}
	t09CheckScenario(t, mc, t09Row{Scenario: "all-agree", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "minority-dissent", Answers: []json.RawMessage{a, a, a, b, b}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2}, Reject: []int{3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "outlier", Answers: []json.RawMessage{a, a, a, a, b}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3}, Reject: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "single-fault", Answers: t09Repeat(a, t09MatrixN), Faults: []int{4}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3}, Faults: 1, SourceFaults: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "all-fault", Faults: []int{0, 1, 2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{ethguard.ErrAllSourcesDown}, Participants: 5, Faults: 6, SourceFaults: []int{0, 1, 2, 3, 4}, Representative: -1, ValueFrom: -1, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "conflict", Answers: []json.RawMessage{a, a, b, b, a}, Faults: []int{4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrConflict}, ErrText: "required support 3, winning cluster 2 of 5", Participants: 5, Support: []int{0, 1}, Reject: []int{2, 3}, Faults: 1, SourceFaults: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "notfound", Answers: t09Repeat(json.RawMessage(`null`), t09MatrixN), Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrMalformedResponse}, Participants: 5, Faults: 5, SourceFaults: []int{0, 1, 2, 3, 4}, Representative: -1, ValueFrom: -1, TypedFrom: -1, TypedErr: rpc.ErrMalformedResponse}})
	t09CheckScenario(t, mc, t09Row{Scenario: "pending-local", Params: []any{uint64(2), "pending", []float64{50}}, Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0}, Representative: -1, ValueFrom: 0, TypedFrom: 0, PreparedPending: true, Note: "the root FeeHistory wrapper has no pending form; production CallInto decodes the selected wire object"}})
	t09CheckScenario(t, mc, t09Row{Scenario: "quorum-boundary", Answers: t09Repeat(a, t09MatrixN), Faults: []int{2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrConflict}, ErrText: "required support 3, winning cluster 2 of 5", Participants: 5, Support: []int{0, 1}, Faults: 3, SourceFaults: []int{2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: -1}})
}

func TestVerificationMethodScenarioGasPrice(t *testing.T) {
	a, b, c := json.RawMessage(`"0x3b9aca00"`), json.RawMessage(`"0x3b9aca01"`), json.RawMessage(`"0x3b9aca02"`)
	mc := t09MethodCase{Method: methods.MethodGasPrice, Typed: t09TypedGasPrice, TypedWant: t09TypedWantBig}
	t09CheckScenario(t, mc, t09Row{Scenario: "all-agree", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0, 1, 2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "minority-dissent", Answers: []json.RawMessage{a, a, a, b, b}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0, 1, 2}, Reject: []int{3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "outlier", Answers: []json.RawMessage{a, a, a, a, b}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0, 1, 2, 3}, Reject: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "single-fault", Answers: t09Repeat(a, t09MatrixN), Faults: []int{4}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0, 1, 2, 3}, Faults: 1, SourceFaults: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "all-fault", Faults: []int{0, 1, 2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{ethguard.ErrAllSourcesDown}, Participants: 5, Faults: 6, SourceFaults: []int{0, 1, 2, 3, 4}, Representative: -1, ValueFrom: -1, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "conflict", Answers: []json.RawMessage{a, a, b, b, c}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{2, 3}, Reject: []int{0, 1, 4}, Representative: 2, ValueFrom: 2, TypedFrom: 2, Note: "lower median is the middle value, not the modal value"}})
	t09CheckScenario(t, mc, t09Row{Scenario: "notfound", Answers: t09Repeat(json.RawMessage(`null`), t09MatrixN), Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrNoValidResponse}, Participants: 5, Faults: 5, Representative: -1, ValueFrom: -1, TypedFrom: -1, TypedErr: rpc.ErrNoValidResponse}})
	t09CheckScenario(t, mc, t09Row{Scenario: "pending-local", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0, 1, 2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0, CheckNoPending: true, Note: "eth_gasPrice has no block parameter"}})
	t09CheckScenario(t, mc, t09Row{Scenario: "quorum-boundary", Answers: t09Repeat(a, t09MatrixN), Faults: []int{2, 3, 4}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0, 1}, Faults: 3, SourceFaults: []int{2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0, Note: "median is not gated by the corroboration floor"}})
}

func TestVerificationMethodScenarioMaxPriorityFeePerGas(t *testing.T) {
	a, b, c := json.RawMessage(`"0x5f5e100"`), json.RawMessage(`"0x5f5e101"`), json.RawMessage(`"0x5f5e102"`)
	mc := t09MethodCase{Method: methods.MethodMaxPriorityFeePerGas, Typed: t09TypedMaxPriorityFee, TypedWant: t09TypedWantBig}
	t09CheckScenario(t, mc, t09Row{Scenario: "all-agree", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0, 1, 2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "minority-dissent", Answers: []json.RawMessage{a, a, a, b, b}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0, 1, 2}, Reject: []int{3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "outlier", Answers: []json.RawMessage{a, a, a, a, b}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0, 1, 2, 3}, Reject: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "single-fault", Answers: t09Repeat(a, t09MatrixN), Faults: []int{4}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0, 1, 2, 3}, Faults: 1, SourceFaults: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "all-fault", Faults: []int{0, 1, 2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{ethguard.ErrAllSourcesDown}, Participants: 5, Faults: 6, SourceFaults: []int{0, 1, 2, 3, 4}, Representative: -1, ValueFrom: -1, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "conflict", Answers: []json.RawMessage{a, a, b, b, c}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{2, 3}, Reject: []int{0, 1, 4}, Representative: 2, ValueFrom: 2, TypedFrom: 2, Note: "lower median is the middle value, not the modal value"}})
	t09CheckScenario(t, mc, t09Row{Scenario: "notfound", Answers: t09Repeat(json.RawMessage(`null`), t09MatrixN), Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrNoValidResponse}, Participants: 5, Faults: 5, Representative: -1, ValueFrom: -1, TypedFrom: -1, TypedErr: rpc.ErrNoValidResponse}})
	t09CheckScenario(t, mc, t09Row{Scenario: "pending-local", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0, 1, 2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0, CheckNoPending: true, Note: "eth_maxPriorityFeePerGas has no block parameter"}})
	t09CheckScenario(t, mc, t09Row{Scenario: "quorum-boundary", Answers: t09Repeat(a, t09MatrixN), Faults: []int{2, 3, 4}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0, 1}, Faults: 3, SourceFaults: []int{2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0, Note: "median is not gated by the corroboration floor"}})
}

func TestVerificationMethodScenarioBlobBaseFee(t *testing.T) {
	a, b, c := json.RawMessage(`"0x1"`), json.RawMessage(`"0x2"`), json.RawMessage(`"0x3"`)
	mc := t09MethodCase{Method: methods.MethodBlobBaseFee, Typed: t09TypedBlobBaseFee, TypedWant: t09TypedWantBig}
	t09CheckScenario(t, mc, t09Row{Scenario: "all-agree", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0, 1, 2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "minority-dissent", Answers: []json.RawMessage{a, a, a, b, b}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0, 1, 2}, Reject: []int{3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "outlier", Answers: []json.RawMessage{a, a, a, a, b}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0, 1, 2, 3}, Reject: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "single-fault", Answers: t09Repeat(a, t09MatrixN), Faults: []int{4}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0, 1, 2, 3}, Faults: 1, SourceFaults: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "all-fault", Faults: []int{0, 1, 2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{ethguard.ErrAllSourcesDown}, Participants: 5, Faults: 6, SourceFaults: []int{0, 1, 2, 3, 4}, Representative: -1, ValueFrom: -1, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "conflict", Answers: []json.RawMessage{a, a, b, b, c}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{2, 3}, Reject: []int{0, 1, 4}, Representative: 2, ValueFrom: 2, TypedFrom: 2, Note: "lower median is the middle value, not the modal value"}})
	t09CheckScenario(t, mc, t09Row{Scenario: "notfound", Answers: t09Repeat(json.RawMessage(`null`), t09MatrixN), Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrNoValidResponse}, Participants: 5, Faults: 5, Representative: -1, ValueFrom: -1, TypedFrom: -1, TypedErr: rpc.ErrNoValidResponse}})
	t09CheckScenario(t, mc, t09Row{Scenario: "pending-local", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0, 1, 2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0, CheckNoPending: true, Note: "eth_blobBaseFee has no block parameter"}})
	t09CheckScenario(t, mc, t09Row{Scenario: "quorum-boundary", Answers: t09Repeat(a, t09MatrixN), Faults: []int{2, 3, 4}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0, 1}, Faults: 3, SourceFaults: []int{2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0, Note: "median is not gated by the corroboration floor"}})
}

func TestVerificationMethodScenarioBlockNumber(t *testing.T) {
	a, b, c := json.RawMessage(`"0x10"`), json.RawMessage(`"0x11"`), json.RawMessage(`"0x12"`)
	mc := t09MethodCase{Method: methods.MethodBlockNumber, Typed: t09TypedBlockNumber, TypedWant: t09TypedWantQuantity}
	t09CheckScenario(t, mc, t09Row{Scenario: "all-agree", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0, 1, 2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "minority-dissent", Answers: []json.RawMessage{a, a, a, b, b}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0, 1, 2}, Reject: []int{3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "outlier", Answers: []json.RawMessage{a, a, a, a, b}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0, 1, 2, 3}, Reject: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "single-fault", Answers: t09Repeat(a, t09MatrixN), Faults: []int{4}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0, 1, 2, 3}, Faults: 1, SourceFaults: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "all-fault", Faults: []int{0, 1, 2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{ethguard.ErrAllSourcesDown}, Participants: 5, Faults: 6, SourceFaults: []int{0, 1, 2, 3, 4}, Representative: -1, ValueFrom: -1, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "conflict", Answers: []json.RawMessage{a, a, b, b, c}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{2, 3}, Reject: []int{0, 1, 4}, Representative: 2, ValueFrom: 2, TypedFrom: 2, Note: "lower median is the middle value, not the modal value"}})
	t09CheckScenario(t, mc, t09Row{Scenario: "notfound", Answers: t09Repeat(json.RawMessage(`null`), t09MatrixN), Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrNoValidResponse}, Participants: 5, Faults: 5, Representative: -1, ValueFrom: -1, TypedFrom: -1, TypedErr: rpc.ErrNoValidResponse}})
	t09CheckScenario(t, mc, t09Row{Scenario: "pending-local", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0, 1, 2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0, CheckNoPending: true, Note: "eth_blockNumber has no block parameter; the public typed wrapper derives its height from the anchor"}})
	t09CheckScenario(t, mc, t09Row{Scenario: "quorum-boundary", Answers: t09Repeat(a, t09MatrixN), Faults: []int{2, 3, 4}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0, 1}, Faults: 3, SourceFaults: []int{2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0, Note: "median is not gated by the corroboration floor"}})
}

func TestVerificationMethodScenarioCreateAccessList(t *testing.T) {
	row := t09FieldRowFor(t, methods.MethodCreateAccessList)
	a, b := row.variants[0], row.other
	mc := t09MethodCase{Method: row.method, Params: row.params, Typed: t09TypedAccessList, TypedWant: t09TypedWantAccess}
	t09CheckScenario(t, mc, t09Row{Scenario: "all-agree", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "minority-dissent", Answers: []json.RawMessage{a, a, a, b, b}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2}, Reject: []int{3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "outlier", Answers: []json.RawMessage{a, a, a, a, b}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3}, Reject: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "single-fault", Answers: t09Repeat(a, t09MatrixN), Faults: []int{4}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3}, Faults: 1, SourceFaults: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "all-fault", Faults: []int{0, 1, 2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{ethguard.ErrAllSourcesDown}, Participants: 5, Faults: 6, SourceFaults: []int{0, 1, 2, 3, 4}, Representative: -1, ValueFrom: -1, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "conflict", Answers: []json.RawMessage{a, a, b, b, a}, Faults: []int{4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrConflict}, ErrText: "required support 3, winning cluster 2 of 5", Participants: 5, Support: []int{0, 1}, Reject: []int{2, 3}, Faults: 1, SourceFaults: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "notfound", Answers: t09Repeat(json.RawMessage(`null`), t09MatrixN), Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrNoValidResponse}, Participants: 5, Faults: 5, Representative: -1, ValueFrom: -1, TypedFrom: -1, TypedErr: rpc.ErrNoValidResponse}})
	t09CheckScenario(t, mc, t09Row{Scenario: "pending-local", Params: []any{map[string]any{"to": t09Address}, "pending"}, Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0}, Representative: -1, ValueFrom: 0, TypedFrom: 0, PreparedPending: true}})
	t09CheckScenario(t, mc, t09Row{Scenario: "quorum-boundary", Answers: t09Repeat(a, t09MatrixN), Faults: []int{2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrConflict}, ErrText: "required support 3, winning cluster 2 of 5", Participants: 5, Support: []int{0, 1}, Faults: 3, SourceFaults: []int{2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: -1}})
}

func TestVerificationMethodScenarioPeerCount(t *testing.T) {
	row := t09FieldRowFor(t, methods.MethodPeerCount)
	a, b, c := json.RawMessage(`"0x5"`), json.RawMessage(`"0x6"`), json.RawMessage(`"0x7"`)
	mc := t09MethodCase{Method: row.method, Typed: t09TypedPeerCount, TypedWant: t09TypedWantQuantity}
	t09CheckScenario(t, mc, t09Row{Scenario: "all-agree", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0}, Representative: -1, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "minority-dissent", Answers: []json.RawMessage{a, a, a, b, b}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0}, Representative: -1, ValueFrom: 0, TypedFrom: 0, Note: "the first healthy source is the observation; later answers are not Reject"}})
	t09CheckScenario(t, mc, t09Row{Scenario: "outlier", Answers: []json.RawMessage{a, a, a, a, b}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0}, Representative: -1, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "single-fault", Answers: t09Repeat(a, t09MatrixN), Faults: []int{4}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0}, Faults: 1, SourceFaults: []int{4}, Representative: -1, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "all-fault", Faults: []int{0, 1, 2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{ethguard.ErrAllSourcesDown}, Participants: 5, Faults: 6, SourceFaults: []int{0, 1, 2, 3, 4}, Representative: -1, ValueFrom: -1, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "conflict", Answers: []json.RawMessage{a, a, b, b, c}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0}, Representative: -1, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "notfound", Answers: t09Repeat(json.RawMessage(`"0x0"`), t09MatrixN), Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0}, Representative: -1, ValueFrom: 0, TypedFrom: 0, Note: "zero peers is a legal local answer; JSON null is rejected by the normalizer and covered separately"}})
	t09CheckScenario(t, mc, t09Row{Scenario: "pending-local", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0}, Representative: -1, ValueFrom: 0, TypedFrom: 0, CheckNoPending: true, Note: "net_peerCount has no block parameter and is inherently node-local"}})
	t09CheckScenario(t, mc, t09Row{Scenario: "quorum-boundary", Answers: t09Repeat(a, t09MatrixN), Faults: []int{2, 3, 4}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0}, Faults: 3, SourceFaults: []int{2, 3, 4}, Representative: -1, ValueFrom: 0, TypedFrom: 0}})
}

func TestVerificationMethodScenarioSyncing(t *testing.T) {
	row := t09FieldRowFor(t, methods.MethodSyncing)
	a := row.variants[0]
	mc := t09MethodCase{Method: row.method, Typed: t09TypedSyncing, TypedWant: t09TypedWantSync}
	t09CheckScenario(t, mc, t09Row{Scenario: "all-agree", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0}, Representative: -1, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "minority-dissent", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0}, Representative: -1, ValueFrom: 0, TypedFrom: 0, Note: "the only other legal shape is the progress object, which T09 does not synthesize; local selection ignores later answers"}})
	t09CheckScenario(t, mc, t09Row{Scenario: "outlier", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0}, Representative: -1, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "single-fault", Answers: t09Repeat(a, t09MatrixN), Faults: []int{4}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0}, Faults: 1, SourceFaults: []int{4}, Representative: -1, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "all-fault", Faults: []int{0, 1, 2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{ethguard.ErrAllSourcesDown}, Participants: 5, Faults: 6, SourceFaults: []int{0, 1, 2, 3, 4}, Representative: -1, ValueFrom: -1, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "conflict", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0}, Representative: -1, ValueFrom: 0, TypedFrom: 0, Note: "no second legal shape is synthesized; the local answer is not compared"}})
	t09CheckScenario(t, mc, t09Row{Scenario: "notfound", Answers: t09Repeat(json.RawMessage(`false`), t09MatrixN), Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0}, Representative: -1, ValueFrom: 0, TypedFrom: 0, Note: "false means fully synced; JSON null is rejected by the shape gate and covered separately"}})
	t09CheckScenario(t, mc, t09Row{Scenario: "pending-local", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0}, Representative: -1, ValueFrom: 0, TypedFrom: 0, CheckNoPending: true, Note: "eth_syncing has no block parameter and is inherently node-local"}})
	t09CheckScenario(t, mc, t09Row{Scenario: "quorum-boundary", Answers: t09Repeat(a, t09MatrixN), Faults: []int{2, 3, 4}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0}, Faults: 3, SourceFaults: []int{2, 3, 4}, Representative: -1, ValueFrom: 0, TypedFrom: 0}})
}

func TestVerificationMethodScenarioNetVersion(t *testing.T) {
	row := t09FieldRowFor(t, methods.MethodNetVersion)
	a, b, c := json.RawMessage(`"1"`), json.RawMessage(`"2"`), json.RawMessage(`"3"`)
	_ = c
	mc := t09MethodCase{Method: row.method, Typed: t09TypedNetVersion, TypedWant: t09TypedWantDecimal}
	t09CheckScenario(t, mc, t09Row{Scenario: "all-agree", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3, 4}, Representative: 0, ValueFrom: 0, ValueExact: `"1"`, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "minority-dissent", Answers: []json.RawMessage{a, a, a, b, b}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2}, Reject: []int{3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "outlier", Answers: []json.RawMessage{a, a, a, a, b}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3}, Reject: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "single-fault", Answers: t09Repeat(a, t09MatrixN), Faults: []int{4}, Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3}, Faults: 1, SourceFaults: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "all-fault", Faults: []int{0, 1, 2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{ethguard.ErrAllSourcesDown}, Participants: 5, Faults: 6, SourceFaults: []int{0, 1, 2, 3, 4}, Representative: -1, ValueFrom: -1, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "conflict", Answers: []json.RawMessage{a, a, b, b, a}, Faults: []int{4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrConflict}, ErrText: "required support 3, winning cluster 2 of 5", Participants: 5, Support: []int{0, 1}, Reject: []int{2, 3}, Faults: 1, SourceFaults: []int{4}, Representative: 0, ValueFrom: 0, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "notfound", Answers: t09Repeat(json.RawMessage(`null`), t09MatrixN), Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrNoValidResponse}, Participants: 5, Faults: 5, Representative: -1, ValueFrom: -1, TypedFrom: -1, TypedErr: rpc.ErrNoValidResponse}})
	t09CheckScenario(t, mc, t09Row{Scenario: "pending-local", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Corroborated, Participants: 5, Support: []int{0, 1, 2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: 0, CheckNoPending: true, Note: "net_version has no block parameter"}})
	t09CheckScenario(t, mc, t09Row{Scenario: "quorum-boundary", Answers: t09Repeat(a, t09MatrixN), Faults: []int{2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{rpc.ErrConflict}, ErrText: "required support 3, winning cluster 2 of 5", Participants: 5, Support: []int{0, 1}, Faults: 3, SourceFaults: []int{2, 3, 4}, Representative: 0, ValueFrom: 0, TypedFrom: -1}})
}

func TestVerificationMethodScenarioListening(t *testing.T) {
	row := t09FieldRowFor(t, methods.MethodListening)
	a, b := json.RawMessage(`true`), json.RawMessage(`false`)
	_ = b
	boolText := func(t *testing.T, raw json.RawMessage) string {
		t.Helper()
		var value bool
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatalf("decode expected listening: %v", err)
		}
		return fmt.Sprint(value)
	}
	mc := t09MethodCase{Method: row.method, Typed: t09TypedListening, TypedWant: boolText}
	t09CheckScenario(t, mc, t09Row{Scenario: "all-agree", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0}, Representative: -1, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "minority-dissent", Answers: []json.RawMessage{a, a, a, b, b}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0}, Representative: -1, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "outlier", Answers: []json.RawMessage{a, a, a, a, b}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0}, Representative: -1, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "single-fault", Answers: t09Repeat(a, t09MatrixN), Faults: []int{4}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0}, Faults: 1, SourceFaults: []int{4}, Representative: -1, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "all-fault", Faults: []int{0, 1, 2, 3, 4}, Want: t09Want{Status: rpc.Unknown, ErrIs: []error{ethguard.ErrAllSourcesDown}, Participants: 5, Faults: 6, SourceFaults: []int{0, 1, 2, 3, 4}, Representative: -1, ValueFrom: -1, TypedFrom: -1}})
	t09CheckScenario(t, mc, t09Row{Scenario: "conflict", Answers: []json.RawMessage{a, a, b, b, a}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0}, Representative: -1, ValueFrom: 0, TypedFrom: 0}})
	t09CheckScenario(t, mc, t09Row{Scenario: "notfound", Answers: t09Repeat(json.RawMessage(`false`), t09MatrixN), Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0}, Representative: -1, ValueFrom: 0, TypedFrom: 0, Note: "false is a legal local answer; JSON null is rejected by the shape gate and covered separately"}})
	t09CheckScenario(t, mc, t09Row{Scenario: "pending-local", Answers: t09Repeat(a, t09MatrixN), Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0}, Representative: -1, ValueFrom: 0, TypedFrom: 0, CheckNoPending: true, Note: "net_listening has no block parameter and is inherently node-local"}})
	t09CheckScenario(t, mc, t09Row{Scenario: "quorum-boundary", Answers: t09Repeat(a, t09MatrixN), Faults: []int{2, 3, 4}, Want: t09Want{Status: rpc.Selected, Participants: 5, Support: []int{0}, Faults: 3, SourceFaults: []int{2, 3, 4}, Representative: -1, ValueFrom: 0, TypedFrom: 0}})
}

// TestVerificationMethodScenarioLocalShapeRejection pins the boundary between a legal local zero and
// an illegal JSON null for the three upstream-local methods.
func TestVerificationMethodScenarioLocalShapeRejection(t *testing.T) {
	for _, tc := range []struct {
		method string
		typed  func(*testing.T, *t09Fleet, []any) (string, error)
	}{
		{methods.MethodPeerCount, t09TypedPeerCount},
		{methods.MethodSyncing, t09TypedSyncing},
		{methods.MethodListening, t09TypedListening},
	} {
		t.Run(tc.method, func(t *testing.T) {
			fleet := t09NewFleet(t, t09MatrixN)
			for _, node := range fleet.nodes {
				node.ReturnRaw(tc.method, json.RawMessage(`null`))
			}
			decision, err := fleet.client.Call(fleet.context(t), tc.method)
			if !errors.Is(err, rpc.ErrNoValidResponse) || decision.Status != rpc.Unknown || len(decision.Value) != 0 {
				t.Fatalf("null local answer = status %v value %s err %v", decision.Status, decision.Value, err)
			}
			if len(decision.Faults) != t09MatrixN || len(decision.Support) != 0 || len(decision.Reject) != 0 {
				t.Fatalf("null local faults/support/reject = %d/%v/%v", len(decision.Faults), decision.Support, decision.Reject)
			}
		})
	}
}

// TestVerificationMethodScenarioGetLogsNullIsMalformed separates an empty result from JSON null:
// empty logs are legal, null is an admission fault on every source.
func TestVerificationMethodScenarioGetLogsNullIsMalformed(t *testing.T) {
	fleet := t09NewFleet(t, t09MatrixN)
	for _, node := range fleet.nodes {
		node.ReturnRaw(methods.MethodGetLogs, json.RawMessage(`null`))
	}
	decision, err := fleet.client.Call(fleet.context(t), methods.MethodGetLogs, map[string]any{"blockHash": t09AnchorHash})
	if !errors.Is(err, rpc.ErrMalformedResponse) || decision.Status != rpc.Unknown || len(decision.Value) != 0 {
		t.Fatalf("null logs = status %v value %s err %v", decision.Status, decision.Value, err)
	}
	if len(decision.Faults) != t09MatrixN || len(decision.SourceFaults) != t09MatrixN {
		t.Fatalf("null logs faults = %d/%d, want one per source", len(decision.Faults), len(decision.SourceFaults))
	}
}

// t09CallRevertDimensionMatrix keeps eth_call's second dimension in the
// matrix: three sources carry the same data, two omit it. Execution is
// unanimous; missing data abstains rather than rejecting empty bytes, and the
// data cluster reaches Q=3.
func TestVerificationMethodScenarioCallRevertDimensions(t *testing.T) {
	fleet := t09NewFleet(t, t09MatrixN)
	for i, node := range fleet.nodes {
		if i < 3 {
			node.FailWithData(methods.MethodCall, 3, "execution reverted", json.RawMessage(`"0xdead"`), true)
		} else {
			node.FailWithData(methods.MethodCall, 3, "execution reverted", nil, false)
		}
	}
	decision, err := fleet.client.Call(fleet.context(t), methods.MethodCall,
		map[string]any{"to": t09Address}, fmt.Sprintf("0x%x", t09AnchorHeight))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != rpc.Corroborated || decision.Revert == nil || string(decision.Value) != `{"data":"0xdead"}` {
		t.Fatalf("revert decision = %+v, Value = %s", decision, decision.Value)
	}
	if len(decision.Dimensions) != 2 {
		t.Fatalf("dimensions = %+v", decision.Dimensions)
	}
	execution, data := decision.Dimensions[0], decision.Dimensions[1]
	if execution.Name != "execution" || len(execution.Support) != t09MatrixN || !execution.Returned {
		t.Fatalf("execution dimension = %+v", execution)
	}
	if data.Name != "data" || len(data.Support) != 3 || len(data.Reject) != 0 || len(data.Abstain) != 2 || !data.Returned {
		t.Fatalf("data dimension = %+v", data)
	}
	t09AssertSourceRefs(t, "call revert", "data support", data.Support, fleet, []int{0, 1, 2})
	t09AssertSourceRefs(t, "call revert", "data abstain", data.Abstain, fleet, []int{3, 4})
}

// TestVerificationQuorumFloorInterlock pins the floor behavior from both sides at the
// fixed N=5 basis. It computes the expected quorum independently of
// rpc.Corroboration.Required, then proves that two supporters conflict while
// three corroborate, so a floor regression cannot hide behind error text.
func TestVerificationQuorumFloorInterlock(t *testing.T) {
	want := t09MatrixN/2 + 1
	if want < t09MatrixFloor {
		want = t09MatrixFloor
	}
	if got := t09MatrixQuorum(); got != want {
		t.Fatalf("quorum for N=%d floor=%d = %d, want %d", t09MatrixN, t09MatrixFloor, got, want)
	}
	answer := json.RawMessage(`"0x1"`)
	below := t09NewFleet(t, t09MatrixN)
	below.nodes[0].ReturnRaw(methods.MethodChainID, answer)
	below.nodes[1].ReturnRaw(methods.MethodChainID, answer)
	for _, index := range []int{2, 3, 4} {
		below.failHTTP(methods.MethodChainID, index)
	}
	decision, err := below.client.Call(below.context(t), methods.MethodChainID)
	if !errors.Is(err, rpc.ErrConflict) || decision.Status != rpc.Unknown {
		t.Fatalf("two supporters = status %v err %v, want conflict", decision.Status, err)
	}
	if len(decision.Support) != 2 || string(decision.Value) != `"0x1"` {
		t.Fatalf("two supporters = support %d value %s, want 2 and the retained cluster value", len(decision.Support), decision.Value)
	}
	if err == nil || !strings.Contains(err.Error(), "required support 3") {
		t.Fatalf("two supporters error = %v, want the actual quorum", err)
	}

	at := t09NewFleet(t, t09MatrixN)
	for _, index := range []int{0, 1, 2} {
		at.nodes[index].ReturnRaw(methods.MethodChainID, answer)
	}
	for _, index := range []int{3, 4} {
		at.failHTTP(methods.MethodChainID, index)
	}
	decision, err = at.client.Call(at.context(t), methods.MethodChainID)
	if err != nil || decision.Status != rpc.Corroborated {
		t.Fatalf("three supporters = status %v err %v, want corroborated", decision.Status, err)
	}
	if len(decision.Support) != 3 || string(decision.Value) != `"0x1"` {
		t.Fatalf("three supporters = support %d value %s", len(decision.Support), decision.Value)
	}
}

// TestVerificationAnchorSeam exercises the moving-tag production seam once, outside
// the per-method matrix: eth_getBalance("latest") first resolves a corroborated
// head through eth_getBlockByNumber, then all five sources are asked for the
// same rewritten hash. Explicit-reference rows do not contact the anchor.
func TestVerificationAnchorSeam(t *testing.T) {
	chain := t09NewChain(t)
	block, hash := chain.block(t09AnchorHeight, 1_700_000_000, 21_000)
	fleet := t09NewFleet(t, t09MatrixN)
	for _, node := range fleet.nodes {
		node.ReturnRaw(methods.MethodGetBlockByNumber, block)
		node.ReturnRaw(methods.MethodGetBalance, json.RawMessage(`"0x64"`))
	}
	decision, err := fleet.client.Call(fleet.context(t), methods.MethodGetBalance, t09Address, "latest")
	if err != nil {
		t.Fatalf("anchor seam: %v", err)
	}
	if decision.Status != rpc.Corroborated || len(decision.Support) != t09MatrixN {
		t.Fatalf("anchor seam: decision = %+v", decision)
	}
	if decision.BlockRef != hash {
		t.Fatalf("anchor seam: block ref = %q, want %q", decision.BlockRef, hash)
	}
	if decision.Prepared == nil || len(decision.Prepared.Params) != 2 {
		t.Fatalf("anchor seam: prepared = %+v", decision.Prepared)
	}
	if got := decision.Prepared.Params[1]; got != hash {
		t.Fatalf("anchor seam: rewritten param = %#v, want %q", got, hash)
	}
	if got := fleet.calls(methods.MethodGetBlockByNumber); got != t09MatrixN {
		t.Fatalf("anchor seam: %d anchor calls, want %d", got, t09MatrixN)
	}
	if got := fleet.calls(methods.MethodGetBalance); got != t09MatrixN {
		t.Fatalf("anchor seam: %d subject calls, want %d", got, t09MatrixN)
	}
	value, err := t09DecodeBig(decision.Value)
	if err != nil || value != "100" {
		t.Fatalf("anchor seam: Value = %s (%v), want 100", decision.Value, err)
	}
}

var t09MatrixTestNames = map[string]string{
	methods.MethodChainID:               "TestVerificationMethodScenarioChainID",
	methods.MethodGetBlockByHash:        "TestVerificationMethodScenarioGetBlockByHash",
	methods.MethodGetBlockByNumber:      "TestVerificationMethodScenarioGetBlockByNumber",
	methods.MethodGetBalance:            "TestVerificationMethodScenarioGetBalance",
	methods.MethodGetTransactionReceipt: "TestVerificationMethodScenarioGetTransactionReceipt",
	methods.MethodGetTransactionByHash:  "TestVerificationMethodScenarioGetTransactionByHash",
	methods.MethodGetBlockReceipts:      "TestVerificationMethodScenarioGetBlockReceipts",
	methods.MethodGetTransactionCount:   "TestVerificationMethodScenarioGetTransactionCount",
	methods.MethodGetStorageAt:          "TestVerificationMethodScenarioGetStorageAt",
	methods.MethodGetCode:               "TestVerificationMethodScenarioGetCode",
	methods.MethodCall:                  "TestVerificationMethodScenarioCall",
	methods.MethodEstimateGas:           "TestVerificationMethodScenarioEstimateGas",
	methods.MethodGetLogs:               "TestVerificationMethodScenarioGetLogs",
	methods.MethodFeeHistory:            "TestVerificationMethodScenarioFeeHistory",
	methods.MethodGasPrice:              "TestVerificationMethodScenarioGasPrice",
	methods.MethodMaxPriorityFeePerGas:  "TestVerificationMethodScenarioMaxPriorityFeePerGas",
	methods.MethodBlobBaseFee:           "TestVerificationMethodScenarioBlobBaseFee",
	methods.MethodBlockNumber:           "TestVerificationMethodScenarioBlockNumber",
	methods.MethodCreateAccessList:      "TestVerificationMethodScenarioCreateAccessList",
	methods.MethodPeerCount:             "TestVerificationMethodScenarioPeerCount",
	methods.MethodSyncing:               "TestVerificationMethodScenarioSyncing",
	methods.MethodNetVersion:            "TestVerificationMethodScenarioNetVersion",
	methods.MethodListening:             "TestVerificationMethodScenarioListening",
}

// TestVerificationMethodScenarioCoverage interlocks the registered inventory with the method
// tests. Each method maps to one function that names exactly the nine expected
// scenario rows and invokes t09CheckScenario once per row. The AST check
// rejects a missing, duplicate, unknown, or unexecuted row, and rejects any
// call to a generic matrix runner. A test that delegates to a shared scenario
// function therefore fails the row-literal check.
func TestVerificationMethodScenarioCoverage(t *testing.T) {
	if got, want := len(methods.RegisteredNames()), len(t09MatrixTestNames); got != want {
		t.Fatalf("%d registered methods, %d matrix tests", got, want)
	}
	source, err := os.ReadFile("method_scenarios_test.go")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), "method_scenarios_test.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	functions := map[string]*ast.FuncDecl{}
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil {
			continue
		}
		functions[fn.Name.Name] = fn
	}
	used := map[string]string{}
	for _, method := range methods.RegisteredNames() {
		testName, ok := t09MatrixTestNames[method]
		if !ok {
			t.Fatalf("registered method %q has no matrix test", method)
		}
		if first, duplicate := used[testName]; duplicate {
			t.Fatalf("%q and %q share matrix test %s", first, method, testName)
		}
		used[testName] = method
		fn, ok := functions[testName]
		if !ok {
			t.Fatalf("%s: matrix test %s does not exist", method, testName)
		}
		calls := 0
		scenarios := map[string]int{}
		ast.Inspect(fn, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.CallExpr:
				ident, ok := typed.Fun.(*ast.Ident)
				if !ok {
					return true
				}
				switch ident.Name {
				case "t09CheckScenario":
					calls++
				case "t09RunMatrix", "t09RunRow":
					t.Fatalf("%s: %s calls generic runner %s", method, testName, ident.Name)
				}
			case *ast.CompositeLit:
				for _, element := range typed.Elts {
					field, ok := element.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					key, ok := field.Key.(*ast.Ident)
					if !ok || key.Name != "Scenario" {
						continue
					}
					literal, ok := field.Value.(*ast.BasicLit)
					if !ok || literal.Kind != token.STRING {
						t.Fatalf("%s: %s has a non-literal Scenario value", method, testName)
					}
					name, err := strconv.Unquote(literal.Value)
					if err != nil {
						t.Fatalf("%s: %s scenario literal: %v", method, testName, err)
					}
					scenarios[name]++
				}
			}
			return true
		})
		if calls != len(t09ScenarioNames) {
			t.Fatalf("%s: %s calls t09CheckScenario %d times, want %d", method, testName, calls, len(t09ScenarioNames))
		}
		if len(scenarios) != len(t09ScenarioNames) {
			t.Fatalf("%s: %s names %d scenarios, want exactly %d: %v", method, testName, len(scenarios), len(t09ScenarioNames), scenarios)
		}
		for _, name := range t09ScenarioNames {
			if scenarios[name] != 1 {
				t.Fatalf("%s: %s has %d %q rows, want exactly 1", method, testName, scenarios[name], name)
			}
		}
		for name := range scenarios {
			if !containsString(t09ScenarioNames, name) {
				t.Fatalf("%s: %s has unknown scenario %q", method, testName, name)
			}
		}
	}
	for name := range functions {
		if !strings.HasPrefix(name, "TestVerificationMethodScenario") || name == "TestVerificationMethodScenarioCoverage" || name == "TestVerificationAnchorSeam" ||
			name == "TestVerificationMethodScenarioCallRevertDimensions" || name == "TestVerificationMethodScenarioLocalShapeRejection" ||
			name == "TestVerificationMethodScenarioGetLogsNullIsMalformed" || name == "TestVerificationQuorumFloorInterlock" {
			continue
		}
		if _, ok := used[name]; !ok {
			t.Fatalf("orphan matrix test %s names no registered method", name)
		}
	}
}
