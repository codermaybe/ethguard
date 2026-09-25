package replay_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ethguard"
	"ethguard/aggregator"
	"ethguard/internal/refcontext"
	"ethguard/internal/rpcsample"
	"ethguard/internal/rpcsample/replay"
	"ethguard/observe"
	"ethguard/pool"
	"ethguard/rpc"
	"ethguard/rpc/methods"
	"ethguard/rpctest"
)

const replayHeadHash = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const replaySafeHash = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
const replayFinalizedHash = "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

const sampleBatchID = "2026-09-23"

func batchDir(feature string) string {
	return filepath.Join("..", "testdata", feature, sampleBatchID)
}

func TestSameFixtureReplaysThroughPolicyAndAggregator(t *testing.T) {
	revert := loadBatch(t, "revert-data")
	objects := loadBatch(t, "block-receipt-tx")

	t.Run("revert hex agrees", func(t *testing.T) {
		records := recordsByID(revert, "revert-hex")
		assertReplay(t, records, methods.CallPolicy(fixedAnchor), true)
	})
	t.Run("revert empty withholds data", func(t *testing.T) {
		records := recordsByID(revert, "revert-empty-1")
		policy := methods.CallPolicy(fixedAnchor)
		assertReplay(t, records, policy, true)
		assertDataStatesDiffer(t, policy, records)
		obs := []rpc.Observation{observationFromRecord(t, records[0]), observationFromRecord(t, records[1])}
		decision, err := policy.Resolve(obs)
		if err != nil || decision.Status != rpc.Corroborated {
			t.Fatalf("status %v err %v, want a corroborated revert", decision.Status, err)
		}
		if present, _ := dataField(t, decision.Value); present {
			t.Fatalf("short data cluster returned data: %s", decision.Value)
		}
		if len(decision.DeletedFields) != 1 || decision.DeletedFields[0] != "data" {
			t.Fatalf("deleted = %v", decision.DeletedFields)
		}
	})
	t.Run("transaction admits and agrees", func(t *testing.T) {
		records := recordsByID(objects, "tx-one")
		assertReplay(t, records, methods.TransactionByHashPolicy(), false)
	})
}

func TestReplayErrorDataStatesStayDistinct(t *testing.T) {
	policy := methods.CallPolicy(fixedAnchor)
	states := []struct {
		name    string
		present bool
		data    string
	}{
		{name: "missing", present: false},
		{name: "null", present: true, data: "null"},
		{name: "empty", present: true, data: `"0x"`},
		{name: "hex", present: true, data: `"0xdeadbeef"`},
	}
	seen := map[string]string{}
	for _, state := range states {
		t.Run(state.name, func(t *testing.T) {
			raw := errorRaw(t, 3, "execution reverted", json.RawMessage(state.data), state.present)
			left := observationFromError("s1", raw)
			right := observationFromError("s2", raw)
			policyDecision, policyErr := policy.Resolve([]rpc.Observation{left, right})
			if policyErr != nil || policyDecision.Status != rpc.Corroborated {
				t.Fatalf("policy status %v err %v", policyDecision.Status, policyErr)
			}
			agg, nodes := newReplayAggregator(t, 2)
			scriptLatest(nodes)
			for _, node := range nodes {
				node.FailWithData(methods.MethodCall, 3, "execution reverted", json.RawMessage(state.data), state.present)
			}
			aggDecision, aggErr := aggregateCall(t, agg, false)
			sameDecision(t, policyDecision, policyErr, aggDecision, aggErr)
			present, data := dataField(t, aggDecision.Value)
			if state.name == "missing" || state.name == "null" {
				if present {
					t.Fatalf("abstained %s data was returned: %s", state.name, aggDecision.Value)
				}
			} else {
				if present != state.present {
					t.Fatalf("aggregator data present = %v, want %v (%s)", present, state.present, aggDecision.Value)
				}
				if present && data != state.data {
					t.Fatalf("aggregator data = %s, want %s", data, state.data)
				}
			}
			for _, node := range nodes {
				if tags := callTags(node, methods.MethodCall, 1); len(tags) != 1 || tags[0] != replayHeadHash {
					t.Fatalf("eth_call block args = %#v, want the latest anchor hash", tags)
				}
				if tags := callTags(node, methods.MethodGetBlockByNumber, 0); len(tags) == 0 || tags[0] != "latest" {
					t.Fatalf("anchor tags = %#v, want latest", tags)
				}
			}
			key, err := policy.Normalize(left.RPCError.Raw())
			if err != nil {
				t.Fatal(err)
			}
			if previous, ok := seen[key]; ok {
				t.Fatalf("state %s collapsed into %s (%s)", state.name, previous, key)
			}
			seen[key] = state.name
		})
	}
	if len(seen) != len(states) {
		t.Fatalf("distinct states = %d, want %d", len(seen), len(states))
	}
}

func TestReplayTypedReadGateRejectsUncorroboratedCall(t *testing.T) {
	policy := methods.CallPolicy(fixedAnchor)
	success := rpc.Observation{Source: rpc.SourceRef{Name: "s1", ID: "s1"}, Value: json.RawMessage(`"0x01"`)}
	fault := rpc.Observation{
		Source:   rpc.SourceRef{Name: "s2", ID: "s2"},
		Err:      errors.New("panic"),
		RPCError: &rpc.RPCError{Code: -32603, Message: "panic"},
	}
	if admit, ok := policy.(rpc.AdmissionPolicy); !ok {
		t.Fatal("eth_call policy lost Admit")
	} else if _, err := admit.Admit(context.Background(), callRequest(), success.Value, refuseCaller(t)); err != nil {
		t.Fatal(err)
	}
	policyDecision, policyErr := policy.Resolve([]rpc.Observation{success, fault})
	if policyErr != nil || policyDecision.Status != rpc.Selected {
		t.Fatalf("policy status %v err %v, want Selected", policyDecision.Status, policyErr)
	}

	agg, nodes := newReplayAggregator(t, 2)
	scriptLatest(nodes)
	nodes[0].Return(methods.MethodCall, "0x01")
	nodes[1].Fail(methods.MethodCall, -32603, "panic")

	openDecision, openErr := aggregateCall(t, agg, false)
	sameDecision(t, policyDecision, policyErr, openDecision, openErr)

	ctx, cancel := context.WithTimeout(refcontext.RequireCorroborated(context.Background()), 5*time.Second)
	defer cancel()
	gated, gatedErr := agg.Aggregate(ctx, callRequest())
	if !errors.Is(gatedErr, rpc.ErrConflict) || gated.Status != rpc.Unknown {
		t.Fatalf("gated status %v err %v", gated.Status, gatedErr)
	}
	if gatedErr == nil || !bytes.Contains([]byte(gatedErr.Error()), []byte("typed read requires corroboration")) {
		t.Fatalf("gate err = %v", gatedErr)
	}
	before := methodCount(nodes[0], methods.MethodGetBlockByNumber)
	if before < 1 {
		t.Fatal("first call did not establish latest")
	}
	if _, err := agg.Aggregate(ctx, callRequest()); !errors.Is(err, rpc.ErrConflict) {
		t.Fatalf("second typed call err = %v", err)
	}
	if methodCount(nodes[0], methods.MethodGetBlockByNumber) <= before {
		t.Fatal("typed call reused the previous anchor")
	}
}

func TestReplayChainIDAgreesWithoutAnchor(t *testing.T) {
	policy := methods.ChainIDPolicy()
	value := json.RawMessage(`"0x1"`)
	obs := []rpc.Observation{
		{Source: rpc.SourceRef{Name: "s1", ID: "s1"}, Value: value},
		{Source: rpc.SourceRef{Name: "s2", ID: "s2"}, Value: append(json.RawMessage(nil), value...)},
	}
	if _, err := policy.Normalize(value); err != nil {
		t.Fatal(err)
	}
	policyDecision, policyErr := policy.Resolve(obs)
	agg, nodes := newReplayAggregator(t, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	aggDecision, aggErr := agg.Aggregate(ctx, rpc.Request{Method: methods.MethodChainID})
	sameDecision(t, policyDecision, policyErr, aggDecision, aggErr)
	if aggDecision.Status != rpc.Corroborated {
		t.Fatalf("status = %v", aggDecision.Status)
	}
	for _, node := range nodes {
		if methodCount(node, methods.MethodGetBlockByNumber) != 0 {
			t.Fatal("chain id established an anchor")
		}
	}
}

func assertReplay(t *testing.T, records []rpcsample.Record, policy rpc.Policy, anchored bool) {
	t.Helper()
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	obs := []rpc.Observation{observationFromRecord(t, records[0]), observationFromRecord(t, records[1])}
	req := requestOf(t, records[0])
	for _, item := range obs {
		if item.Err == nil {
			if admit, ok := policy.(rpc.AdmissionPolicy); ok {
				if _, err := admit.Admit(context.Background(), req, item.Value, refuseCaller(t)); err != nil {
					t.Fatalf("admit %s: %v", item.Source.ID, err)
				}
			}
			if _, err := policy.Normalize(item.Value); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if item.RPCError == nil {
			t.Fatal("error observation lost RPCError")
		}
		if _, err := policy.Normalize(item.RPCError.Raw()); err != nil {
			t.Fatal(err)
		}
	}
	policyDecision, policyErr := policy.Resolve(obs)

	agg, nodes := newReplayAggregator(t, 2)
	if anchored {
		scriptLatest(nodes)
	}
	bySource := map[string]*rpctest.Node{"s1": nodes[0], "s2": nodes[1]}
	for _, record := range records {
		scriptRecord(t, bySource[record.Source.ID], record)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	aggDecision, aggErr := agg.Aggregate(ctx, req)
	sameDecision(t, policyDecision, policyErr, aggDecision, aggErr)
	if anchored {
		for _, node := range nodes {
			if tags := callTags(node, records[0].Method, 1); len(tags) != 1 || tags[0] != replayHeadHash {
				t.Fatalf("%s block args = %#v", node.URL(), tags)
			}
		}
	} else if methodCount(nodes[0], methods.MethodGetBlockByNumber) != 0 {
		t.Fatal("unanchored replay established an anchor")
	}
	for _, node := range nodes {
		if methodCount(node, records[0].Method) != 1 {
			t.Fatalf("%s calls = %#v", records[0].Method, node.Calls())
		}
	}
}

func assertDataStatesDiffer(t *testing.T, policy rpc.Policy, records []rpcsample.Record) {
	t.Helper()
	left := observationFromRecord(t, records[0])
	right := observationFromRecord(t, records[1])
	leftKey, err := policy.Normalize(left.RPCError.Raw())
	if err != nil {
		t.Fatal(err)
	}
	rightKey, err := policy.Normalize(right.RPCError.Raw())
	if err != nil {
		t.Fatal(err)
	}
	if leftKey == rightKey {
		t.Fatal("missing data and 0x collapsed to one comparison")
	}
}

func sameDecision(t *testing.T, left rpc.Decision, leftErr error, right rpc.Decision, rightErr error) {
	t.Helper()
	if left.Status != right.Status {
		t.Fatalf("status policy %v aggregator %v", left.Status, right.Status)
	}
	if (leftErr == nil) != (rightErr == nil) || !errors.Is(leftErr, rightErr) && !errors.Is(rightErr, leftErr) {
		if leftErr == nil || rightErr == nil || !errors.Is(leftErr, rpc.ErrConflict) || !errors.Is(rightErr, rpc.ErrConflict) {
			t.Fatalf("policy err %v, aggregator err %v", leftErr, rightErr)
		}
	}
	if leftErr == nil && compact(t, left.Value) != compact(t, right.Value) {
		t.Fatalf("value policy %s aggregator %s", left.Value, right.Value)
	}
}

func aggregateCall(t *testing.T, agg *aggregator.Aggregator, typed bool) (rpc.Decision, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if typed {
		ctx = refcontext.RequireCorroborated(ctx)
	}
	return agg.Aggregate(ctx, callRequest())
}

func callRequest() rpc.Request {
	return rpc.Request{
		Method: methods.MethodCall,
		Params: []any{map[string]any{"to": "0x0000000000000000000000000000000000000001"}, "latest"},
	}
}

func fixedAnchor(context.Context) (string, error) { return replayHeadHash, nil }

func taggedAnchor(ctx context.Context) (string, error) {
	switch rpc.AnchorTag(ctx) {
	case "safe":
		return replaySafeHash, nil
	case "finalized":
		return replayFinalizedHash, nil
	default:
		return replayHeadHash, nil
	}
}

func newReplayAggregator(t *testing.T, count int) (*aggregator.Aggregator, []*rpctest.Node) {
	t.Helper()
	names := make([]string, count)
	for i := range names {
		names[i] = fmt.Sprintf("s%d", i+1)
	}
	return newNamedReplayAggregator(t, names, nil)
}

func newNamedReplayAggregator(t *testing.T, names []string, corrob *rpc.CorroborationShared) (*aggregator.Aggregator, []*rpctest.Node) {
	t.Helper()
	sources := pool.NewPoolWithChainID(1)
	t.Cleanup(sources.Close)
	nodes := make([]*rpctest.Node, 0, len(names))
	for _, name := range names {
		node := rpctest.NewNode(1)
		t.Cleanup(node.Close)
		if _, err := sources.Join(name, node.URL()); err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, node)
	}
	var opts []aggregator.Option
	if corrob != nil {
		opts = append(opts, aggregator.WithCorroboration(corrob))
	}
	resolver := ethguard.NewStrictHeadAnchor(sources, observe.Nop)
	reference := func(ctx context.Context) (rpc.ReferenceEvidence, error) {
		tag := rpc.AnchorTag(ctx)
		var ref rpc.ReferenceEvidence
		var err error
		if tag != "" && tag != "latest" {
			ref, err = resolver.AnchorReferenceForTag(ctx, tag)
		} else {
			ref, err = resolver.AnchorReference(ctx)
		}
		ref.Tag = tag
		refcontext.Publish(ctx, ref)
		if err != nil {
			return ref, err
		}
		if ref.Status != rpc.Corroborated {
			return ref, fmt.Errorf("uncorroborated reference: %w", rpc.ErrConflict)
		}
		return ref, nil
	}
	anchor := func(ctx context.Context) (string, error) {
		ref, err := reference(ctx)
		return ref.Hash, err
	}
	height := func(ctx context.Context) (uint64, error) {
		ref, err := reference(ctx)
		return ref.Height, err
	}
	return aggregator.New(sources, replayRegistry(anchor, height), opts...), nodes
}

func replayRegistry(anchor rpc.AnchorFunc, height rpc.AnchorHeightFunc) rpc.Registry {
	return rpc.Registry{
		methods.MethodChainID:               methods.ChainIDPolicy(),
		methods.MethodGetBlockByHash:        methods.BlockByHashPolicy(),
		methods.MethodGetBlockByNumber:      methods.BlockByNumberPolicy(),
		methods.MethodGetBalance:            methods.GetBalancePolicy(anchor),
		methods.MethodGetTransactionReceipt: methods.TransactionReceiptPolicy(),
		methods.MethodGetTransactionByHash:  methods.TransactionByHashPolicy(),
		methods.MethodGetBlockReceipts:      methods.BlockReceiptsPolicy(anchor),
		methods.MethodGetTransactionCount:   methods.GetTransactionCountPolicy(anchor),
		methods.MethodGetStorageAt:          methods.GetStorageAtPolicy(anchor),
		methods.MethodGetCode:               methods.GetCodePolicy(anchor),
		methods.MethodCall:                  methods.CallPolicy(anchor),
		methods.MethodEstimateGas:           methods.EstimateGasPolicy(anchor),
		methods.MethodGetLogs:               methods.LogsPolicy(height),
		methods.MethodFeeHistory:            methods.FeeHistoryPolicy(height),
		methods.MethodGasPrice:              methods.GasPricePolicy(),
		methods.MethodMaxPriorityFeePerGas:  methods.MaxPriorityFeePerGasPolicy(),
		methods.MethodBlobBaseFee:           methods.BlobBaseFeePolicy(),
		methods.MethodBlockNumber:           methods.BlockNumberPolicy(),
		methods.MethodCreateAccessList:      methods.CreateAccessListPolicy(anchor),
		methods.MethodPeerCount:             methods.PeerCountPolicy(),
		methods.MethodSyncing:               methods.SyncingPolicy(),
		methods.MethodNetVersion:            methods.NetVersionPolicy(),
		methods.MethodListening:             methods.ListeningPolicy(),
	}
}

func scriptLatest(nodes []*rpctest.Node) { scriptTaggedHeads(nodes) }

func scriptTaggedHeads(nodes []*rpctest.Node) {
	heads := map[string]map[string]any{
		"latest":    {"number": "0x10", "hash": replayHeadHash},
		"safe":      {"number": "0x0e", "hash": replaySafeHash},
		"finalized": {"number": "0x0c", "hash": replayFinalizedHash},
	}
	for _, node := range nodes {
		node.Handle(methods.MethodGetBlockByNumber, func(call rpctest.Call) (any, error) {
			tag := "latest"
			if len(call.Params) > 0 {
				if err := json.Unmarshal(call.Params[0], &tag); err != nil || tag == "" {
					tag = "latest"
				}
			}
			head, ok := heads[strings.ToLower(tag)]
			if !ok {
				return nil, rpctest.RPCError(-32602, "rpctest: unscripted block tag "+tag)
			}
			return head, nil
		})
	}
}

func scriptRecord(t *testing.T, node *rpctest.Node, record rpcsample.Record) {
	t.Helper()
	if strings.TrimSpace(record.Response.Body) == "" {
		node.FailHTTP(record.Method, recordedTransportStatus(record))
		return
	}
	isError, body, err := replay.BodyOf(record)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if !isError {
		node.ReturnRaw(record.Method, doc.Result)
		return
	}
	code, message, data, present := errorParts(t, doc.Error)
	node.FailWithData(record.Method, code, message, data, present)
}

func observationFromRecord(t *testing.T, record rpcsample.Record) rpc.Observation {
	t.Helper()
	source := rpc.SourceRef{Name: record.Source.ID, ID: record.Source.ID}
	if strings.TrimSpace(record.Response.Body) == "" {
		return rpc.Observation{Source: source, Err: errors.New(transportFault(record))}
	}
	isError, body, err := replay.BodyOf(record)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if !isError {
		return rpc.Observation{Source: source, Value: doc.Result}
	}
	return observationFromError(record.Source.ID, doc.Error)
}

func recordedTransportStatus(record rpcsample.Record) int {
	for _, item := range record.Errors {
		if item.Kind != "transport" {
			continue
		}
		switch {
		case strings.Contains(item.Message, "403"):
			return http.StatusForbidden
		case strings.Contains(item.Message, "429"):
			return http.StatusTooManyRequests
		}
	}
	return http.StatusTooManyRequests
}

func transportFault(record rpcsample.Record) string {
	for _, item := range record.Errors {
		if item.Kind == "transport" && item.Message != "" {
			return item.Message
		}
	}
	return "transport fault"
}

func observationFromError(id string, raw json.RawMessage) rpc.Observation {
	code, message, data, present := errorParts(nil, raw)
	rpcErr := &rpc.RPCError{Code: code, Message: message}
	if present {
		rpcErr.Data = append(json.RawMessage(nil), data...)
	}
	return rpc.Observation{
		Source:   rpc.SourceRef{Name: id, ID: id},
		Err:      errors.New(message),
		RPCError: rpcErr,
	}
}

func errorRaw(t *testing.T, code int, message string, data json.RawMessage, present bool) json.RawMessage {
	t.Helper()
	object := map[string]any{"code": code, "message": message}
	if present {
		if len(data) == 0 {
			data = json.RawMessage("null")
		}
		object["data"] = json.RawMessage(data)
	}
	raw, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func errorParts(t *testing.T, raw json.RawMessage) (int, string, json.RawMessage, bool) {
	if t != nil {
		t.Helper()
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		panic(err)
	}
	var code int
	var message string
	if err := json.Unmarshal(fields["code"], &code); err != nil {
		panic(err)
	}
	if err := json.Unmarshal(fields["message"], &message); err != nil {
		panic(err)
	}
	data, present := fields["data"]
	if present {
		data = append(json.RawMessage(nil), data...)
	}
	return code, message, data, present
}

func requestOf(t *testing.T, record rpcsample.Record) rpc.Request {
	t.Helper()
	var wire struct {
		Params []any `json:"params"`
	}
	if err := json.Unmarshal([]byte(record.Request), &wire); err != nil {
		t.Fatal(err)
	}
	return rpc.Request{Method: record.Method, Params: wire.Params}
}

func dataField(t *testing.T, raw json.RawMessage) (bool, string) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	data, present := fields["data"]
	return present, string(data)
}

func compact(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	if len(raw) == 0 {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		t.Fatalf("compact %s: %v", raw, err)
	}
	return buf.String()
}

func callTags(node *rpctest.Node, method string, index int) []string {
	var tags []string
	for _, call := range node.Calls() {
		if call.Method != method || len(call.Params) <= index {
			continue
		}
		var tag string
		if json.Unmarshal(call.Params[index], &tag) == nil {
			tags = append(tags, tag)
		}
	}
	return tags
}

func methodCount(node *rpctest.Node, method string) int {
	count := 0
	for _, call := range node.Calls() {
		if call.Method == method {
			count++
		}
	}
	return count
}

func loadBatch(t *testing.T, name string) []rpcsample.Record {
	t.Helper()
	_, records, err := replay.LoadBatch(batchDir(name))
	if err != nil {
		t.Fatal(err)
	}
	return records
}

func recordsByID(records []rpcsample.Record, id string) []rpcsample.Record {
	var out []rpcsample.Record
	for _, record := range records {
		if record.RequestID == id {
			out = append(out, record)
		}
	}
	return out
}

func refuseCaller(t *testing.T) rpc.AdmissionCaller {
	t.Helper()
	return func(context.Context, string, []any) (json.RawMessage, error) {
		t.Fatal("admission issued a dependent call")
		return nil, nil
	}
}
