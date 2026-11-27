package aggregator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/internal/rpcengine"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard/internal/diag/rpcsample/replay"
	objectreceipt "github.com/codermaybe/ethguard/internal/ethobject/receipt"

	"github.com/codermaybe/ethguard/internal/methods"
	"github.com/codermaybe/ethguard/internal/pool"
	"github.com/codermaybe/ethguard/internal/projection"
	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"
	"github.com/codermaybe/ethguard/internal/uncertain"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpctest"
)

// Receipt acceptance runs the registered canonical plan through real HTTP
// collection, processing, diagnostics, field projection and health accounting.
// receiptRouteMethod is the method under test.
const receiptRouteMethod = methods.MethodGetTransactionReceipt

// receiptRouteFixture loads one captured receipt response.
func receiptRouteFixture(tb testing.TB) json.RawMessage {
	tb.Helper()
	_, records, err := replay.LoadBatch(filepath.Join("..", "diag", "rpcsample", "testdata", "block-receipt-tx", "2026-09-23"))
	if err != nil {
		tb.Fatal(err)
	}
	for _, record := range records {
		if record.RequestID != "receipt-one" || record.Source.ID != "s1" {
			continue
		}
		_, body, err := replay.BodyOf(record)
		if err != nil {
			tb.Fatal(err)
		}
		var doc struct {
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(body, &doc); err != nil {
			tb.Fatal(err)
		}
		return append(json.RawMessage(nil), doc.Result...)
	}
	tb.Fatal("receipt sample missing")
	return nil
}

// receiptRouteHash reads the fixture's own transaction hash.
func receiptRouteHash(tb testing.TB, raw json.RawMessage) string {
	tb.Helper()
	var fields struct {
		TransactionHash string `json:"transactionHash"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil || fields.TransactionHash == "" {
		tb.Fatalf("fixture carries no transactionHash: %v", err)
	}
	return fields.TransactionHash
}

// receiptRouteFields decodes one receipt spelling into its field map.
func receiptRouteFields(tb testing.TB, raw json.RawMessage) map[string]json.RawMessage {
	tb.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		tb.Fatalf("fixture is not a receipt object: %v", err)
	}
	return fields
}

// receiptRouteReplace replaces one field with already-encoded JSON.
func receiptRouteReplace(tb testing.TB, raw json.RawMessage, key, value string) json.RawMessage {
	tb.Helper()
	fields := receiptRouteFields(tb, raw)
	fields[key] = json.RawMessage(value)
	out, err := json.Marshal(fields)
	if err != nil {
		tb.Fatal(err)
	}
	return out
}

// receiptRouteDelete removes one field.
func receiptRouteDelete(tb testing.TB, raw json.RawMessage, key string) json.RawMessage {
	tb.Helper()
	fields := receiptRouteFields(tb, raw)
	delete(fields, key)
	out, err := json.Marshal(fields)
	if err != nil {
		tb.Fatal(err)
	}
	return out
}

// receiptRouteReorder writes the same fields alphabetically, a spelling the
// canonical encoder never emits, so the route visibly folds member order.
func receiptRouteReorder(tb testing.TB, raw json.RawMessage) json.RawMessage {
	tb.Helper()
	out, err := json.Marshal(receiptRouteFields(tb, raw))
	if err != nil {
		tb.Fatal(err)
	}
	return out
}

// receiptRouteFoldHex shouts the addresses and hashes.
func receiptRouteFoldHex(tb testing.TB, raw json.RawMessage) json.RawMessage {
	tb.Helper()
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		tb.Fatal(err)
	}
	for _, key := range []string{"transactionHash", "blockHash", "from", "to"} {
		if value, ok := fields[key].(string); ok {
			fields[key] = strings.ToUpper(value)
		}
	}
	out, err := json.Marshal(fields)
	if err != nil {
		tb.Fatal(err)
	}
	return out
}

// receiptRouteWithLogs rewrites the receipt to carry one log and the bloom
// that log produces. logTxHash is the log's own transactionHash, which may
// claim another transaction than the receipt.
func receiptRouteWithLogs(tb testing.TB, raw json.RawMessage, logTxHash string) json.RawMessage {
	tb.Helper()
	fields := receiptRouteFields(tb, raw)
	var blockHash, blockNumber, transactionIndex string
	for key, target := range map[string]*string{
		"blockHash":        &blockHash,
		"blockNumber":      &blockNumber,
		"transactionIndex": &transactionIndex,
	} {
		if err := json.Unmarshal(fields[key], target); err != nil {
			tb.Fatalf("fixture %s: %v", key, err)
		}
	}
	address := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	topics := []common.Hash{common.HexToHash("0x00000000000000000000000000000000000000000000000000000000000000ab")}
	entry := map[string]any{
		"address":          address.Hex(),
		"topics":           []string{topics[0].Hex()},
		"data":             "0x01",
		"blockNumber":      blockNumber,
		"transactionIndex": transactionIndex,
		"transactionHash":  logTxHash,
		"blockHash":        blockHash,
		"logIndex":         "0x0",
		"removed":          false,
	}
	logs, err := json.Marshal([]any{entry})
	if err != nil {
		tb.Fatal(err)
	}
	bloom := types.CreateBloom(&types.Receipt{Logs: []*types.Log{{Address: address, Topics: topics}}})
	fields["logs"] = logs
	fields["logsBloom"] = json.RawMessage(`"` + hexutil.Encode(bloom.Bytes()) + `"`)
	out, err := json.Marshal(fields)
	if err != nil {
		tb.Fatal(err)
	}
	return out
}

// receiptRouteMembers scripts one node per body over the same member names.
func receiptRouteMembers(tb testing.TB, bodies []json.RawMessage) []pool.Member {
	members, _ := receiptRouteNodes(tb, bodies)
	return members
}

func receiptRouteNodes(tb testing.TB, bodies []json.RawMessage) ([]pool.Member, []*rpctest.Node) {
	tb.Helper()
	members := make([]pool.Member, len(bodies))
	nodes := make([]*rpctest.Node, len(bodies))
	for i, body := range bodies {
		node := rpctest.NewNode(0)
		node.ReturnRaw(receiptRouteMethod, body)
		tb.Cleanup(node.Close)
		members[i] = pool.Member{Name: fmt.Sprintf("src-%d", i), URL: node.URL()}
		nodes[i] = node
	}
	return members, nodes
}

func assertReceiptRouteRequests(t *testing.T, nodes []*rpctest.Node, perNode int, transactionHash string) {
	t.Helper()
	for i, node := range nodes {
		calls := node.Calls()
		if len(calls) != perNode {
			t.Fatalf("source %d requests = %d, want %d", i, len(calls), perNode)
		}
		for _, call := range calls {
			if call.Method != receiptRouteMethod || len(call.Params) != 1 || string(call.Params[0]) != `"`+transactionHash+`"` {
				t.Fatalf("source %d received unexpected request: %s %s", i, call.Method, call.Params)
			}
		}
	}
}

// receiptRouteProjector binds the production receipt projector, so
// extensions and diagnostics are compared through the same assembly the
// runtime installs.
func receiptRouteProjector(tb testing.TB) rpcengine.ObservationProjector {
	tb.Helper()
	projector, err := projection.NewSchemaProjector(receiptRouteMethod)
	if err != nil {
		tb.Fatal(err)
	}
	return projector
}

// receiptRouteCompiledPlan compiles the registered receipt contract.
func receiptRouteCompiledPlan(tb testing.TB) rpcengine.ReadPlan {
	tb.Helper()
	spec, ok := methods.SpecFor(receiptRouteMethod)
	if !ok {
		tb.Fatalf("%s is not registered", receiptRouteMethod)
	}
	if spec.ProcessCanonical == nil {
		tb.Fatal("receipt spec declares no processor")
	}
	plan, err := methods.CompilePlan(spec)
	if err != nil {
		tb.Fatal(err)
	}

	return plan
}

// receiptRouteRequest binds the method and the requested hash.
func receiptRouteRequest(hash string) rpc.Request {
	return rpc.Request{Method: receiptRouteMethod, Params: []any{hash}}
}

// receiptRouteHealth is the business part of one source's health snapshot:
// timings and timestamps are deliberately excluded from route comparison.
type receiptRouteHealth struct {
	Successes           uint64
	Failures            uint64
	ConsecutiveFailures uint64
	LastError           string
}

// receiptRouteRun is one complete Aggregate invocation plus the material it
// retained and the health it left behind.
type receiptRouteRun struct {
	decision rpc.Decision
	err      error
	raw      []uncertain.Response
	health   map[string]receiptRouteHealth
}

// receiptRouteAggregate builds one fresh pool over the shared scripted
// members, runs the real coordinator once, and captures material and health.
func receiptRouteAggregate(t *testing.T, plan rpcengine.ReadPlan, members []pool.Member, req rpc.Request, mode rpc.CorroborationMode, floor int) receiptRouteRun {
	t.Helper()
	p := fixedPoolWith(t, 0, []pool.PoolOption{pool.WithDialer(nativehttp.NewDialer())}, members...)
	shared, err := rpcengine.NewCorroboration(mode, floor)
	if err != nil {
		t.Fatal(err)
	}
	recorder := &captureRecorder{}
	agg := New(p, shared, WithRecorder(recorder), WithPlans(map[string]rpcengine.ReadPlan{req.Method: plan}), WithProjectors(map[string]rpcengine.ObservationProjector{methods.ProjectorReceipt: receiptRouteProjector(t)}))
	collector := uncertain.NewCollector(uncertain.Config{BudgetBytes: 1 << 20})
	ctx := uncertain.WithCollector(context.Background(), collector)
	decision, err := agg.Aggregate(ctx, req)
	if event := oneEvent(t, recorder); event.Faults != len(decision.Faults) {
		t.Fatalf("event faults = %d, decision faults = %d", event.Faults, len(decision.Faults))
	}
	run := receiptRouteRun{decision: decision, err: err, health: map[string]receiptRouteHealth{}}
	run.raw = collector.Snapshot().Raw
	for _, src := range p.VotingMembers() {
		h := src.Health()
		lastError := ""
		if h.LastError != nil {
			lastError = h.LastError.Error()
		}
		run.health[src.ID()] = receiptRouteHealth{
			Successes:           h.Successes,
			Failures:            h.Failures,
			ConsecutiveFailures: h.ConsecutiveFailures,
			LastError:           lastError,
		}
	}
	return run
}

// receiptRouteExecute runs one production invocation over scripted sources.
func receiptRouteExecute(t *testing.T, bodies []json.RawMessage, requestHash string, mode rpc.CorroborationMode, floor int) receiptRouteRun {
	t.Helper()
	members, nodes := receiptRouteNodes(t, bodies)
	run := receiptRouteAggregate(t, receiptRouteCompiledPlan(t), members, receiptRouteRequest(requestHash), mode, floor)
	assertReceiptRouteRequests(t, nodes, 1, requestHash)
	return run
}

// receiptRouteSourceByIndex names the scripted source the way the pool does.
func receiptRouteSourceByIndex(index int) string { return fmt.Sprintf("src-%d", index) }

// TestReceiptCanonicalRouteSpellings folds case and member order into one
// canonical value and reports three supporters.
func TestReceiptCanonicalRouteSpellings(t *testing.T) {
	valid := receiptRouteFixture(t)
	self := receiptRouteHash(t, valid)
	bodies := []json.RawMessage{valid, receiptRouteFoldHex(t, valid), receiptRouteReorder(t, valid)}
	candidate := receiptRouteExecute(t, bodies, self, rpc.CorroborationMajority, 2)
	if candidate.decision.Status != rpc.Corroborated {
		t.Fatalf("status = %v", candidate.decision.Status)
	}
	if len(candidate.decision.Support) != 3 || len(candidate.decision.Reject) != 0 {
		t.Fatalf("support/reject = %d/%d", len(candidate.decision.Support), len(candidate.decision.Reject))
	}
	for i, member := range candidate.decision.Support {
		if member.Name != receiptRouteSourceByIndex(i) {
			t.Fatalf("support = %+v", candidate.decision.Support)
		}
	}
	if candidate.decision.Representative.Name != receiptRouteSourceByIndex(0) {
		t.Fatalf("representative = %+v", candidate.decision.Representative)
	}
	want, err := objectreceipt.Encode(valid)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(candidate.decision.Value, want) {
		t.Fatalf("value = %s, want %s", candidate.decision.Value, want)
	}
}

// TestReceiptCanonicalRouteNull keeps absent/null semantics: three nulls
// corroborate the canonical null with no object invented.
func TestReceiptCanonicalRouteNull(t *testing.T) {
	null := json.RawMessage(`null`)
	bodies := []json.RawMessage{null, null, null}
	candidate := receiptRouteExecute(t, bodies, "0x"+strings.Repeat("11", 32), rpc.CorroborationMajority, 2)
	if candidate.decision.Status != rpc.Corroborated || string(candidate.decision.Value) != "null" {
		t.Fatalf("null = %s status %v", candidate.decision.Value, candidate.decision.Status)
	}
	if len(candidate.decision.Support) != 3 {
		t.Fatalf("support = %+v", candidate.decision.Support)
	}
	for i, member := range candidate.decision.Support {
		if member.Name != receiptRouteSourceByIndex(i) {
			t.Fatalf("support = %+v", candidate.decision.Support)
		}
	}
}

// TestReceiptCanonicalRouteDissent keeps a minority answer visible in Reject
// while the majority commits, for a changed from and a changed gasUsed.
func TestReceiptCanonicalRouteDissent(t *testing.T) {
	valid := receiptRouteFixture(t)
	self := receiptRouteHash(t, valid)
	cases := []struct {
		name  string
		other json.RawMessage
	}{
		{"from", receiptRouteReplace(t, valid, "from", `"0x0000000000000000000000000000000000000002"`)},
		{"gasUsed", receiptRouteReplace(t, valid, "gasUsed", `"0x1"`)},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			bodies := []json.RawMessage{valid, valid, item.other}
			candidate := receiptRouteExecute(t, bodies, self, rpc.CorroborationMajority, 2)
			if candidate.decision.Status != rpc.Corroborated {
				t.Fatalf("status = %v", candidate.decision.Status)
			}
			if len(candidate.decision.Support) != 2 || len(candidate.decision.Reject) != 1 {
				t.Fatalf("support/reject = %d/%d", len(candidate.decision.Support), len(candidate.decision.Reject))
			}
			if candidate.decision.Reject[0].Name != receiptRouteSourceByIndex(2) {
				t.Fatalf("reject = %+v", candidate.decision.Reject)
			}
		})
	}
}

// TestReceiptCanonicalRouteDenominator keeps the four-member basis with two
// agreeing, one differing, and one missing logs; the bad source is attributed
// once.
func TestReceiptCanonicalRouteDenominator(t *testing.T) {
	valid := receiptRouteFixture(t)
	self := receiptRouteHash(t, valid)
	gasOther := receiptRouteReplace(t, valid, "gasUsed", `"0x1"`)
	missingLogs := receiptRouteDelete(t, valid, "logs")
	bodies := []json.RawMessage{valid, valid, gasOther, missingLogs}
	candidate := receiptRouteExecute(t, bodies, self, rpc.CorroborationMajority, 2)
	if candidate.decision.Status != rpc.Unknown || !errors.Is(candidate.err, rpc.ErrConflict) {
		t.Fatalf("status/error = %v/%v", candidate.decision.Status, candidate.err)
	}
	if !strings.Contains(candidate.err.Error(), "of 4 voting members") {
		t.Fatalf("denominator changed: %v", candidate.err)
	}
	if len(candidate.decision.SourceFaults) != 1 || candidate.decision.SourceFaults[0].Source.Name != receiptRouteSourceByIndex(3) {
		t.Fatalf("source faults = %+v", candidate.decision.SourceFaults)
	}
	if !strings.Contains(candidate.decision.SourceFaults[0].Err.Error(), "logs must be an array") {
		t.Fatalf("bad source attribution = %v", candidate.decision.SourceFaults[0].Err)
	}
}

func TestReceiptCanonicalRouteIdenticalMinorityRemainsResidual(t *testing.T) {
	valid := receiptRouteFixture(t)
	bad := receiptRouteDelete(t, valid, "logs")
	candidate := receiptRouteExecute(t, []json.RawMessage{valid, valid, bad, bad, bad}, receiptRouteHash(t, valid), rpc.CorroborationMajority, 2)
	decision := candidate.decision
	if !errors.Is(candidate.err, rpc.ErrInsufficientSupport) || decision.Status != rpc.Unknown {
		t.Fatalf("identical minority status/error = %v/%v", decision.Status, candidate.err)
	}
	if decision.Adjudication.Basis != 5 || decision.Adjudication.Required != 3 {
		t.Fatalf("fixed denominator changed: %+v", decision.Adjudication)
	}
	if len(decision.SourceFaults) != 3 || decision.Diagnostics == nil || len(decision.Diagnostics.Conflicts) != 0 {
		t.Fatalf("rejected sources or diagnostics lost: %+v / %+v", decision.SourceFaults, decision.Diagnostics)
	}
	foundHash := false
	for _, residual := range decision.Diagnostics.Residuals {
		names := make([]string, len(residual.Sources))
		for i, source := range residual.Sources {
			names[i] = source.Name
		}
		sort.Strings(names)
		if !reflect.DeepEqual(names, []string{receiptRouteSourceByIndex(0), receiptRouteSourceByIndex(1)}) {
			t.Fatalf("raw leaf gained or lost a source vote: %+v", residual)
		}
		if residual.Path == "/transactionHash" {
			foundHash = string(residual.Value) == `"`+receiptRouteHash(t, valid)+`"`
		}
	}
	if !foundHash {
		t.Fatalf("identical valid replies lost their residual: %+v", decision.Diagnostics)
	}
}

func TestReceiptCanonicalRouteTimestampSpellingRemainsDiagnostic(t *testing.T) {
	valid := receiptRouteFixture(t)
	self := receiptRouteHash(t, valid)
	withLog := receiptRouteWithLogs(t, valid, self)
	withTimestamp := func(timestamp string) json.RawMessage {
		fields := receiptRouteFields(t, withLog)
		var logs []map[string]json.RawMessage
		if err := json.Unmarshal(fields["logs"], &logs); err != nil {
			t.Fatal(err)
		}
		logs[0]["blockTimestamp"] = json.RawMessage(`"` + timestamp + `"`)
		raw, err := json.Marshal(logs)
		if err != nil {
			t.Fatal(err)
		}
		return receiptRouteReplace(t, withLog, "logs", string(raw))
	}
	lower, upper := withTimestamp("0xab"), withTimestamp("0xAB")
	candidate := receiptRouteExecute(t, []json.RawMessage{upper, lower, lower}, self, rpc.CorroborationMajority, 2)
	if candidate.err != nil || candidate.decision.Status != rpc.Corroborated || len(candidate.decision.Support) != 3 {
		t.Fatalf("canonical timestamp spellings did not agree: %+v / %v", candidate.decision, candidate.err)
	}
	if !bytes.Contains(candidate.decision.Value, []byte(`"blockTimestamp":"0xab"`)) {
		t.Fatalf("canonical timestamp missing: %s", candidate.decision.Value)
	}
	if candidate.decision.Diagnostics == nil {
		t.Fatal("raw spelling diagnostics missing")
	}
	for _, residual := range candidate.decision.Diagnostics.Residuals {
		if residual.Path == "/logs/0/blockTimestamp" {
			if string(residual.Value) != `"0xAB"` || len(residual.Sources) != 1 || residual.Sources[0].Name != receiptRouteSourceByIndex(0) {
				t.Fatalf("raw timestamp residual = %+v", residual)
			}
			return
		}
	}
	t.Fatalf("canonical agreement hid the raw timestamp spelling: %+v", candidate.decision.Diagnostics)
}

// TestReceiptCanonicalRouteAllMalformed keeps the malformed-response class
// when every delivered response fails the same check.
func TestReceiptCanonicalRouteAllMalformed(t *testing.T) {
	valid := receiptRouteFixture(t)
	self := receiptRouteHash(t, valid)
	bad := receiptRouteDelete(t, valid, "logs")
	bodies := []json.RawMessage{bad, bad, bad}
	candidate := receiptRouteExecute(t, bodies, self, rpc.CorroborationMajority, 2)
	if candidate.decision.Status != rpc.Unknown || !errors.Is(candidate.err, rpc.ErrMalformedResponse) {
		t.Fatalf("status/error = %v/%v", candidate.decision.Status, candidate.err)
	}
	if len(candidate.decision.SourceFaults) != 3 {
		t.Fatalf("source faults = %+v", candidate.decision.SourceFaults)
	}
}

// TestReceiptCanonicalRouteBadSources mixes two bad sources (bloom and log
// binding) beside three agreeing valid ones: the valid sources are not
// penalized and each bad source is attributed once.
func TestReceiptCanonicalRouteBadSources(t *testing.T) {
	valid := receiptRouteFixture(t)
	self := receiptRouteHash(t, valid)
	other := "0x" + strings.Repeat("22", 32)
	bloomBad := receiptRouteReplace(t, receiptRouteWithLogs(t, valid, self), "logsBloom", `"0x`+strings.Repeat("00", 255)+`01"`)
	bindingBad := receiptRouteWithLogs(t, valid, other)
	bodies := []json.RawMessage{valid, valid, valid, bloomBad, bindingBad}
	candidate := receiptRouteExecute(t, bodies, self, rpc.CorroborationMajority, 2)
	if candidate.decision.Status != rpc.Corroborated || len(candidate.decision.Support) != 3 {
		t.Fatalf("status/support = %v/%d", candidate.decision.Status, len(candidate.decision.Support))
	}
	if len(candidate.decision.Reject) != 0 {
		t.Fatalf("valid sources were penalized: %+v", candidate.decision.Reject)
	}
	if len(candidate.decision.SourceFaults) != 2 {
		t.Fatalf("source faults = %+v", candidate.decision.SourceFaults)
	}
	if candidate.decision.SourceFaults[0].Source.Name != receiptRouteSourceByIndex(3) ||
		!strings.Contains(candidate.decision.SourceFaults[0].Err.Error(), "logsBloom disagrees with logs") {
		t.Fatalf("bloom attribution = %+v", candidate.decision.SourceFaults[0])
	}
	if candidate.decision.SourceFaults[1].Source.Name != receiptRouteSourceByIndex(4) ||
		!strings.Contains(candidate.decision.SourceFaults[1].Err.Error(), "does not match receipt transactionHash") {
		t.Fatalf("binding attribution = %+v", candidate.decision.SourceFaults[1])
	}
	for id, h := range candidate.health {
		if h.LastError == "" {
			if h.Successes != 1 || h.Failures != 0 {
				t.Fatalf("valid source %s health = %+v", id, h)
			}
			continue
		}
		if h.Successes != 0 || h.Failures != 1 {
			t.Fatalf("bad source %s health = %+v", id, h)
		}
	}
}

// TestReceiptCanonicalRouteForeignRequest rejects every receipt bound to
// another transaction with the same malformed class.
func TestReceiptCanonicalRouteForeignRequest(t *testing.T) {
	valid := receiptRouteFixture(t)
	other := "0x" + strings.Repeat("22", 32)
	bodies := []json.RawMessage{valid, valid, valid}
	candidate := receiptRouteExecute(t, bodies, other, rpc.CorroborationMajority, 2)
	if !errors.Is(candidate.err, rpc.ErrMalformedResponse) ||
		!strings.Contains(candidate.err.Error(), "does not match requested hash") {
		t.Fatalf("error = %v", candidate.err)
	}
	if len(candidate.decision.SourceFaults) != 3 {
		t.Fatalf("source faults = %+v", candidate.decision.SourceFaults)
	}
}

// TestReceiptCanonicalRouteForwarded reuses validated canonical bytes in
// Forwarded mode while preserving the complete raw route's delivery.
func TestReceiptCanonicalRouteForwarded(t *testing.T) {
	valid := receiptRouteFixture(t)
	self := receiptRouteHash(t, valid)
	members := receiptRouteMembers(t, []json.RawMessage{valid})
	req := receiptRouteRequest(self)
	counts := &workCounters{}
	candidate := receiptRouteAggregate(t, instrumentedPlan(t, receiptRouteMethod, counts), members, req, rpc.CorroborationForwarded, 0)
	if candidate.decision.Status != rpc.Forwarded {
		t.Fatalf("status = %v", candidate.decision.Status)
	}
	if counts.process.Load() != 1 || counts.admit.Load() != 0 || counts.encode.Load() != 0 || counts.normalize.Load() != 0 {
		t.Fatalf("forwarded route work = process %d admit %d encode %d normalize %d",
			counts.process.Load(), counts.admit.Load(), counts.encode.Load(), counts.normalize.Load())
	}
}

// TestReceiptCanonicalRouteProjection keeps Extensions and Diagnostics
// sourced from the original response even though the committed value is the
// canonical receipt without the vendor members.
func TestReceiptCanonicalRouteProjection(t *testing.T) {
	valid := receiptRouteFixture(t)
	self := receiptRouteHash(t, valid)
	extended := receiptRouteReplace(t, valid, "vendorExtension", `"x"`)
	diagnostic := receiptRouteReplace(t, extended, "diagnosticOnly", `"y"`)
	bodies := []json.RawMessage{diagnostic, extended, extended}
	candidate := receiptRouteExecute(t, bodies, self, rpc.CorroborationMajority, 2)
	if bytes.Contains(candidate.decision.Value, []byte("vendorExtension")) || bytes.Contains(candidate.decision.Value, []byte("diagnosticOnly")) {
		t.Fatalf("canonical value retained vendor members: %s", candidate.decision.Value)
	}
	if candidate.decision.Extensions == nil {
		t.Fatal("extensions from the original response are missing")
	}
	found := false
	for _, field := range candidate.decision.Extensions.Fields {
		if field.Path == "/vendorExtension" {
			found = true
			if string(field.Value) != `"x"` || len(field.Sources) != 3 {
				t.Fatalf("extension field = %+v", field)
			}
		}
	}
	if !found {
		t.Fatalf("vendor extension missing: %+v", candidate.decision.Extensions)
	}
	if candidate.decision.Diagnostics == nil {
		t.Fatal("diagnostics from the original response are missing")
	}
	found = false
	for _, residual := range candidate.decision.Diagnostics.Residuals {
		if residual.Path == "/diagnosticOnly" {
			found = true
			if string(residual.Value) != `"y"` || len(residual.Sources) != 1 || residual.Sources[0].Name != receiptRouteSourceByIndex(0) {
				t.Fatalf("diagnostic field = %+v", residual)
			}
		}
	}
	if !found {
		t.Fatalf("diagnostic residual missing: %+v", candidate.decision.Diagnostics)
	}
}

// Each usable source runs one processor and no standalone normalization.
func TestReceiptCanonicalRouteWorkCounts(t *testing.T) {
	valid := receiptRouteFixture(t)
	self := receiptRouteHash(t, valid)
	members, nodes := receiptRouteNodes(t, []json.RawMessage{valid, valid, valid})
	req := receiptRouteRequest(self)

	counts := &workCounters{}
	candidatePlan := instrumentedPlan(t, receiptRouteMethod, counts)
	candidate := receiptRouteAggregate(t, candidatePlan, members, req, rpc.CorroborationMajority, 2)
	assertReceiptRouteRequests(t, nodes, 1, self)
	if candidate.decision.Status != rpc.Corroborated {
		t.Fatalf("candidate status = %v", candidate.decision.Status)
	}
	if counts.process.Load() != 3 || counts.admit.Load() != 0 || counts.encode.Load() != 0 || counts.normalize.Load() != 0 {
		t.Fatalf("candidate work = process %d admit %d encode %d normalize %d",
			counts.process.Load(), counts.admit.Load(), counts.encode.Load(), counts.normalize.Load())
	}

}

// receiptRouteBenchPool builds one benchmark pool over the shared members.
func receiptRouteBenchPool(b *testing.B, members []pool.Member) *pool.Pool {
	b.Helper()
	p, err := pool.NewWithMembers(0, members, pool.WithDialer(nativehttp.NewDialer()))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(p.Close)
	return p
}

// receiptRouteBenchAggregator installs one plan and the production projector.
func receiptRouteBenchAggregator(b *testing.B, p *pool.Pool, plan rpcengine.ReadPlan, mode rpc.CorroborationMode, floor int) *Aggregator {
	b.Helper()
	shared, err := rpcengine.NewCorroboration(mode, floor)
	if err != nil {
		b.Fatal(err)
	}
	return New(p, shared, WithPlans(map[string]rpcengine.ReadPlan{receiptRouteMethod: plan}), WithProjectors(map[string]rpcengine.ObservationProjector{methods.ProjectorReceipt: receiptRouteProjector(b)}))
}

// BenchmarkReceiptCanonicalMicro measures the registered processor.
func BenchmarkReceiptCanonicalMicro(b *testing.B) {
	raw := receiptRouteFixture(b)
	req := receiptRouteRequest(receiptRouteHash(b, raw))
	plan := receiptRouteCompiledPlan(b)
	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	b.ResetTimer()
	for range b.N {
		if _, err := plan.Canonical.Process(context.Background(), req, raw, nil); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkReceiptCanonicalAggregate measures a warmed production Aggregate.
func BenchmarkReceiptCanonicalAggregate(b *testing.B) {
	valid := receiptRouteFixture(b)
	req := receiptRouteRequest(receiptRouteHash(b, valid))
	members := receiptRouteMembers(b, []json.RawMessage{valid, valid, valid})
	{
		b.Run("canonical", func(b *testing.B) {
			plan := receiptRouteCompiledPlan(b)
			p := receiptRouteBenchPool(b, members)
			agg := receiptRouteBenchAggregator(b, p, plan, rpc.CorroborationMajority, 2)
			warm, err := agg.Aggregate(context.Background(), req)
			if err != nil || warm.Status != rpc.Corroborated || len(warm.Support) != 3 {
				b.Fatalf("warm-up = %v, %v", warm.Status, err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				decision, err := agg.Aggregate(context.Background(), req)
				if err != nil || decision.Status != rpc.Corroborated || len(decision.Support) != 3 {
					b.Fatalf("aggregate = %v, %v", decision.Status, err)
				}
			}
		})
	}
}

// Admission may exclude a candidate, but the completed wire answer remains
// available for diagnostics and later source summaries on both method routes.
func TestReceiptAdmissionRetainsResponseFacts(t *testing.T) {
	valid := receiptRouteFixture(t)
	hash := receiptRouteHash(t, valid)
	{
		for _, tc := range []struct {
			name string
			raw  json.RawMessage
			want rpc.InterpretationState
		}{
			{"missing-field", receiptRouteDelete(t, valid, "transactionHash"), rpc.InterpretationRejected},
			{"null", json.RawMessage(`null`), rpc.InterpretationAdmitted},
		} {
			t.Run(tc.name, func(t *testing.T) {
				members := receiptRouteMembers(t, []json.RawMessage{tc.raw})
				p := fixedPoolWith(t, 0, []pool.PoolOption{pool.WithDialer(nativehttp.NewDialer())}, members...)
				req := receiptRouteRequest(hash)
				emissions, err := p.Collect(context.Background(), req.Method, req.Params, pool.CollectOptions{})
				if err != nil || len(emissions) != 1 {
					t.Fatalf("collect: %v (%d responses)", err, len(emissions))
				}
				before := emissions[0].Response
				plan := receiptRouteCompiledPlan(t)
				state := &invocationState{ctx: context.Background(), plan: &plan, admissionRequest: req, preparedMethod: req.Method, processorRoute: plan.Canonical.Process, emissions: emissions}
				New(p, rpcengine.Corroboration{Floor: 1}).admitPhase(state)
				got := state.emissions[0].Response
				if got.Exchange != before.Exchange || got.Exchange.Progress != rpc.ExchangeCompleted || got.Exchange.HTTPStatus != 200 || got.Answer.Kind != rpc.AnswerResult || string(got.Answer.Result) != string(tc.raw) {
					t.Fatalf("admission changed HTTP/protocol facts: %+v", got)
				}
				if got.Answer.Interpretation.State != tc.want || got.Validate() != nil {
					t.Fatalf("method interpretation = %+v", got.Answer.Interpretation)
				}
				if (got.CandidateErr(false) == nil) != (tc.want == rpc.InterpretationAdmitted) {
					t.Fatalf("candidate eligibility contradicts method state: %+v", got)
				}
			})
		}
	}
}
