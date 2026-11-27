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

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"

	objectreceipt "github.com/codermaybe/ethguard/internal/ethobject/receipt"
	"github.com/codermaybe/ethguard/internal/rpcsample/replay"
	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"
	"github.com/codermaybe/ethguard/internal/uncertain"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/codermaybe/ethguard/rpc/projection"
	"github.com/codermaybe/ethguard/rpctest"
)

// The receipt canonical route's real-coordinator acceptance. Both sides call
// Aggregator.Aggregate through the same compiled plan over the same scripted
// sources; the baseline plan only drops ProcessCanonical, keeping Admit and
// the strict resolver, while the candidate keeps the production processor.
// No CanonicalObservation is hand-built here: the coordinator's own
// processing, fault attribution, health accounting, diagnostics, and
// extensions are the material under test.

// receiptRouteMethod is the method under test.
const receiptRouteMethod = methods.MethodGetTransactionReceipt

// receiptRouteFixture loads one captured receipt response.
func receiptRouteFixture(tb testing.TB) json.RawMessage {
	tb.Helper()
	_, records, err := replay.LoadBatch(filepath.Join("..", "internal", "rpcsample", "testdata", "block-receipt-tx", "2026-09-23"))
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
func receiptRouteProjector(tb testing.TB) rpc.ObservationProjector {
	tb.Helper()
	projector, err := projection.NewSchemaProjector(receiptRouteMethod)
	if err != nil {
		tb.Fatal(err)
	}
	return projector
}

// receiptRouteCompiledPlan compiles the registered receipt spec. The
// baseline only clears ProcessCanonical; every other declaration is shared.
func receiptRouteCompiledPlan(tb testing.TB, withProcessor bool) rpc.ReadPlan {
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
	if !withProcessor {
		plan.ProcessCanonical = nil
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
func receiptRouteAggregate(t *testing.T, plan rpc.ReadPlan, members []pool.Member, req rpc.Request, mode rpc.CorroborationMode, floor int) receiptRouteRun {
	t.Helper()
	p := fixedPoolWith(t, 0, []pool.PoolOption{pool.WithDialer(nativehttp.NewDialer())}, members...)
	shared, err := rpc.NewCorroborationShared(mode, floor)
	if err != nil {
		t.Fatal(err)
	}
	recorder := &captureRecorder{}
	agg := New(p,
		WithRecorder(recorder),
		WithPlans(map[string]rpc.ReadPlan{req.Method: plan}),
		WithCorroboration(shared),
		WithProjectors(map[string]rpc.ObservationProjector{methods.ProjectorReceipt: receiptRouteProjector(t)}),
	)
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

// compareReceiptRoutes asserts the candidate's business outcome, evidence,
// fault attribution, retained material, and health match the baseline.
func compareReceiptRoutes(t *testing.T, baseline, candidate receiptRouteRun) {
	t.Helper()
	if (baseline.err == nil) != (candidate.err == nil) {
		t.Fatalf("verdicts differ: baseline=%v candidate=%v", baseline.err, candidate.err)
	}
	if baseline.err != nil {
		if baseline.err.Error() != candidate.err.Error() {
			t.Fatalf("error texts differ:\nbaseline  %q\ncandidate %q", baseline.err, candidate.err)
		}
		for _, class := range []error{rpc.ErrInvalidRequest, rpc.ErrMalformedResponse, rpc.ErrConflict, rpc.ErrInsufficientSupport} {
			if errors.Is(baseline.err, class) != errors.Is(candidate.err, class) {
				t.Fatalf("error class %v differs: baseline=%v candidate=%v", class, baseline.err, candidate.err)
			}
		}
	}
	if baseline.decision.Status != candidate.decision.Status {
		t.Fatalf("status differs: baseline=%v candidate=%v", baseline.decision.Status, candidate.decision.Status)
	}
	if !reflect.DeepEqual(baseline.decision.Adjudication, candidate.decision.Adjudication) {
		t.Fatalf("adjudication differs: baseline=%+v candidate=%+v", baseline.decision.Adjudication, candidate.decision.Adjudication)
	}
	if !bytes.Equal(baseline.decision.Value, candidate.decision.Value) {
		t.Fatalf("value differs:\nbaseline  %s\ncandidate %s", baseline.decision.Value, candidate.decision.Value)
	}
	if !reflect.DeepEqual(baseline.decision.Support, candidate.decision.Support) {
		t.Fatalf("support differs:\nbaseline  %+v\ncandidate %+v", baseline.decision.Support, candidate.decision.Support)
	}
	if !reflect.DeepEqual(baseline.decision.Reject, candidate.decision.Reject) {
		t.Fatalf("reject differs:\nbaseline  %+v\ncandidate %+v", baseline.decision.Reject, candidate.decision.Reject)
	}
	if baseline.decision.Representative != candidate.decision.Representative {
		t.Fatalf("representative differs: baseline=%+v candidate=%+v", baseline.decision.Representative, candidate.decision.Representative)
	}
	if !reflect.DeepEqual(baseline.decision.Participants, candidate.decision.Participants) {
		t.Fatalf("participants differ:\nbaseline  %+v\ncandidate %+v", baseline.decision.Participants, candidate.decision.Participants)
	}
	if !bytes.Equal(baseline.decision.Provenance, candidate.decision.Provenance) {
		t.Fatalf("provenance differs:\nbaseline  %s\ncandidate %s", baseline.decision.Provenance, candidate.decision.Provenance)
	}
	baselinePrepared, candidatePrepared := baseline.decision.Prepared, candidate.decision.Prepared
	if (baselinePrepared == nil) != (candidatePrepared == nil) {
		t.Fatalf("prepared request differs: baseline=%+v candidate=%+v", baselinePrepared, candidatePrepared)
	}
	if baselinePrepared != nil && (baselinePrepared.Method != candidatePrepared.Method || !bytes.Equal(baselinePrepared.Params, candidatePrepared.Params)) {
		t.Fatalf("prepared request differs: baseline=%+v candidate=%+v", baselinePrepared, candidatePrepared)
	}
	if (baseline.decision.Reference == nil) != (candidate.decision.Reference == nil) {
		t.Fatalf("reference presence differs: baseline=%+v candidate=%+v", baseline.decision.Reference, candidate.decision.Reference)
	}
	if want, got := faultTexts(baseline.decision.Faults), faultTexts(candidate.decision.Faults); !reflect.DeepEqual(want, got) {
		t.Fatalf("faults differ:\nbaseline  %v\ncandidate %v", want, got)
	}
	if want, got := collectionFaultTexts(baseline.decision.CollectionFaults), collectionFaultTexts(candidate.decision.CollectionFaults); !reflect.DeepEqual(want, got) {
		t.Fatalf("collection faults differ:\nbaseline  %v\ncandidate %v", want, got)
	}
	compareReceiptSourceFaults(t, baseline.decision.SourceFaults, candidate.decision.SourceFaults)
	if want, got := jsonOf(t, baseline.decision.Diagnostics), jsonOf(t, candidate.decision.Diagnostics); !bytes.Equal(want, got) {
		t.Fatalf("diagnostics differ:\nbaseline  %s\ncandidate %s", want, got)
	}
	if want, got := jsonOf(t, baseline.decision.Extensions), jsonOf(t, candidate.decision.Extensions); !bytes.Equal(want, got) {
		t.Fatalf("extensions differ:\nbaseline  %s\ncandidate %s", want, got)
	}
}

// faultTexts renders a fault list as a sorted multiset, preserving
// duplicates while ignoring only collection order.
func faultTexts(faults []error) []string {
	out := make([]string, len(faults))
	for i, fault := range faults {
		out[i] = fault.Error()
	}
	sort.Strings(out)
	return out
}

// collectionFaultTexts is the collection-fault list in the same shape.
func collectionFaultTexts(faults []error) []string { return faultTexts(faults) }

// jsonOf renders a diagnostic view deterministically for comparison.
func jsonOf(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// compareReceiptSourceFaults compares structured attribution by source
// identity, so an undiffering pair of lists cannot pass on length alone.
func compareReceiptSourceFaults(t *testing.T, baseline, candidate []rpc.SourceFault) {
	t.Helper()
	if len(baseline) != len(candidate) {
		t.Fatalf("source fault counts differ: baseline=%d candidate=%d", len(baseline), len(candidate))
	}
	for i := range baseline {
		if baseline[i].Source != candidate[i].Source {
			t.Fatalf("source fault %d attribution differs: baseline=%+v candidate=%+v", i, baseline[i].Source, candidate[i].Source)
		}
		be, ce := "", ""
		if baseline[i].Err != nil {
			be = baseline[i].Err.Error()
		}
		if candidate[i].Err != nil {
			ce = candidate[i].Err.Error()
		}
		if be != ce {
			t.Fatalf("source fault %d error differs: baseline=%q candidate=%q", i, be, ce)
		}
	}
}

// receiptRouteMaterial renders the retained raw records as a sorted
// identity-of-source multiset. Timestamps never enter the snapshot.
func receiptRouteMaterial(raw []uncertain.Response) []string {
	out := make([]string, len(raw))
	for i, item := range raw {
		out[i] = fmt.Sprintf("%s|%s|%s|%s|%s|%t", item.Stage, item.Method, item.Source.ID, item.Value, item.Error, item.Truncated)
	}
	sort.Strings(out)
	return out
}

// compareReceiptMaterial asserts both routes retained the same original
// response material, keyed by source identity.
func compareReceiptMaterial(t *testing.T, baseline, candidate []uncertain.Response) {
	t.Helper()
	want, got := receiptRouteMaterial(baseline), receiptRouteMaterial(candidate)
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("raw material differs:\nbaseline  %v\ncandidate %v", want, got)
	}
}

// compareReceiptHealth asserts per-source health accounting matches without
// comparing timestamps or latency.
func compareReceiptHealth(t *testing.T, baseline, candidate map[string]receiptRouteHealth) {
	t.Helper()
	if !reflect.DeepEqual(baseline, candidate) {
		t.Fatalf("source health differs:\nbaseline  %+v\ncandidate %+v", baseline, candidate)
	}
}

// receiptRouteCompare runs the same sources through the baseline (no
// processor) and candidate (production processor) plans and asserts the
// complete route-visible contract.
func receiptRouteCompare(t *testing.T, bodies []json.RawMessage, requestHash string, mode rpc.CorroborationMode, floor int) (receiptRouteRun, receiptRouteRun) {
	t.Helper()
	members, nodes := receiptRouteNodes(t, bodies)
	baseline := receiptRouteAggregate(t, receiptRouteCompiledPlan(t, false), members, receiptRouteRequest(requestHash), mode, floor)
	assertReceiptRouteRequests(t, nodes, 1, requestHash)
	candidate := receiptRouteAggregate(t, receiptRouteCompiledPlan(t, true), members, receiptRouteRequest(requestHash), mode, floor)
	assertReceiptRouteRequests(t, nodes, 2, requestHash)
	compareReceiptRoutes(t, baseline, candidate)
	compareReceiptMaterial(t, baseline.raw, candidate.raw)
	compareReceiptHealth(t, baseline.health, candidate.health)
	return baseline, candidate
}

// receiptRouteSourceByIndex names the scripted source the way the pool does.
func receiptRouteSourceByIndex(index int) string { return fmt.Sprintf("src-%d", index) }

// TestReceiptCanonicalRouteSpellings folds case and member order into one
// canonical value and reports three supporters.
func TestReceiptCanonicalRouteSpellings(t *testing.T) {
	valid := receiptRouteFixture(t)
	self := receiptRouteHash(t, valid)
	bodies := []json.RawMessage{valid, receiptRouteFoldHex(t, valid), receiptRouteReorder(t, valid)}
	_, candidate := receiptRouteCompare(t, bodies, self, rpc.CorroborationMajority, 2)
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
	_, candidate := receiptRouteCompare(t, bodies, "0x"+strings.Repeat("11", 32), rpc.CorroborationMajority, 2)
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
			_, candidate := receiptRouteCompare(t, bodies, self, rpc.CorroborationMajority, 2)
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
	_, candidate := receiptRouteCompare(t, bodies, self, rpc.CorroborationMajority, 2)
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
	_, candidate := receiptRouteCompare(t, []json.RawMessage{valid, valid, bad, bad, bad}, receiptRouteHash(t, valid), rpc.CorroborationMajority, 2)
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
	_, candidate := receiptRouteCompare(t, []json.RawMessage{upper, lower, lower}, self, rpc.CorroborationMajority, 2)
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
	_, candidate := receiptRouteCompare(t, bodies, self, rpc.CorroborationMajority, 2)
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
	_, candidate := receiptRouteCompare(t, bodies, self, rpc.CorroborationMajority, 2)
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
	_, candidate := receiptRouteCompare(t, bodies, other, rpc.CorroborationMajority, 2)
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
	baseline := receiptRouteAggregate(t, receiptRouteCompiledPlan(t, false), members, req, rpc.CorroborationForwarded, 0)
	counts := &workCounters{}
	candidate := receiptRouteAggregate(t, instrumentedPlan(t, receiptRouteMethod, counts), members, req, rpc.CorroborationForwarded, 0)
	compareReceiptRoutes(t, baseline, candidate)
	compareReceiptMaterial(t, baseline.raw, candidate.raw)
	compareReceiptHealth(t, baseline.health, candidate.health)
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
	_, candidate := receiptRouteCompare(t, bodies, self, rpc.CorroborationMajority, 2)
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

// TestReceiptCanonicalRouteWorkCounts pins which compiled hooks run. These
// are hook entries, not JSON parse counts: the candidate runs the processor
// once per source and no raw hook; the baseline runs admission plus the
// resolver encoder once per usable source.
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

	counts = &workCounters{}
	baselinePlan := instrumentedPlan(t, receiptRouteMethod, counts)
	baselinePlan.ProcessCanonical = nil
	baseline := receiptRouteAggregate(t, baselinePlan, members, req, rpc.CorroborationMajority, 2)
	assertReceiptRouteRequests(t, nodes, 2, self)
	if baseline.decision.Status != rpc.Corroborated {
		t.Fatalf("baseline status = %v", baseline.decision.Status)
	}
	if counts.process.Load() != 0 || counts.admit.Load() != 3 || counts.encode.Load() != 3 || counts.normalize.Load() != 0 {
		t.Fatalf("baseline work = process %d admit %d encode %d normalize %d",
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
func receiptRouteBenchAggregator(b *testing.B, p *pool.Pool, plan rpc.ReadPlan, mode rpc.CorroborationMode, floor int) *Aggregator {
	b.Helper()
	shared, err := rpc.NewCorroborationShared(mode, floor)
	if err != nil {
		b.Fatal(err)
	}
	return New(p,
		WithPlans(map[string]rpc.ReadPlan{receiptRouteMethod: plan}),
		WithCorroboration(shared),
		WithProjectors(map[string]rpc.ObservationProjector{methods.ProjectorReceipt: receiptRouteProjector(b)}),
	)
}

// BenchmarkReceiptCanonicalMicro compares the two per-source hook chains on
// the identical captured payload and request: the baseline admission (which
// validates by decoding) plus the resolver's clustering encoder, against the
// production processor that hands out the bytes its admission just checked.
func BenchmarkReceiptCanonicalMicro(b *testing.B) {
	raw := receiptRouteFixture(b)
	req := receiptRouteRequest(receiptRouteHash(b, raw))
	baseline := receiptRouteCompiledPlan(b, false)
	candidate := receiptRouteCompiledPlan(b, true)
	ctx := context.Background()
	b.Run("baseline-admit-plus-encode", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(raw)))
		b.ResetTimer()
		for range b.N {
			if _, err := baseline.Admit(ctx, req, raw, nil); err != nil {
				b.Fatal(err)
			}
			if _, err := rpc.CanonicalOf(baseline.Resolver, raw); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("candidate-process-canonical", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(raw)))
		b.ResetTimer()
		for range b.N {
			if _, err := candidate.ProcessCanonical(ctx, req, raw, nil); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkReceiptCanonicalAggregate measures a real Aggregate over three
// scripted sources with identical fixture, request, and membership. Server
// and connection setup and one warm-up invocation stay outside the timed
// loop; the baseline plan only clears ProcessCanonical.
func BenchmarkReceiptCanonicalAggregate(b *testing.B) {
	valid := receiptRouteFixture(b)
	req := receiptRouteRequest(receiptRouteHash(b, valid))
	members := receiptRouteMembers(b, []json.RawMessage{valid, valid, valid})
	variants := []struct {
		name string
		plan bool
	}{
		{"baseline-raw-route", false},
		{"candidate-canonical-route", true},
	}
	for _, variant := range variants {
		b.Run(variant.name, func(b *testing.B) {
			plan := receiptRouteCompiledPlan(b, variant.plan)
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
	for _, processed := range []bool{false, true} {
		for _, tc := range []struct {
			name string
			raw  json.RawMessage
			want rpc.InterpretationState
		}{
			{"missing-field", receiptRouteDelete(t, valid, "transactionHash"), rpc.InterpretationRejected},
			{"null", json.RawMessage(`null`), rpc.InterpretationAdmitted},
		} {
			t.Run(fmt.Sprintf("%s/processed=%v", tc.name, processed), func(t *testing.T) {
				members := receiptRouteMembers(t, []json.RawMessage{tc.raw})
				p := fixedPoolWith(t, 0, []pool.PoolOption{pool.WithDialer(nativehttp.NewDialer())}, members...)
				req := receiptRouteRequest(hash)
				emissions, err := p.Collect(context.Background(), req.Method, req.Params, pool.CollectOptions{})
				if err != nil || len(emissions) != 1 {
					t.Fatalf("collect: %v (%d responses)", err, len(emissions))
				}
				before := emissions[0].Response
				plan := receiptRouteCompiledPlan(t, processed)
				state := &invocationState{ctx: context.Background(), plan: &plan, admissionRequest: req, preparedMethod: req.Method, processorRoute: plan.ProcessCanonical, emissions: emissions}
				New(p).admitPhase(state)
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
