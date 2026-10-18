package replay_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/aggregator"
	"github.com/codermaybe/ethguard/internal/refcontext"
	"github.com/codermaybe/ethguard/internal/rpcsample"
	"github.com/codermaybe/ethguard/internal/rpcsample/replay"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/methods"
	"github.com/codermaybe/ethguard/rpctest"
)

func TestBlockReceiptTxBatchMatchesExpect(t *testing.T) {
	if err := matchCapturedExpect(t, "block-receipt-tx"); err != nil {
		t.Fatal(err)
	}
}

func TestEIP1898ObjectBatchMatchesExpect(t *testing.T) {
	if err := matchCapturedExpect(t, "eip1898-object"); err != nil {
		t.Fatal(err)
	}
}

func TestCapturedExpectTamperProducesReviewableDiff(t *testing.T) {
	for _, feature := range []string{"block-receipt-tx", "eip1898-object"} {
		t.Run(feature, func(t *testing.T) {
			dir := batchDir(feature)
			raw, err := os.ReadFile(filepath.Join(dir, "expect.json"))
			if err != nil {
				t.Fatal(err)
			}
			cases := []struct {
				name   string
				mutate func(*expectFile) string
			}{
				{
					name: "comparison key",
					mutate: func(file *expectFile) string {
						file.Cases[0].Sources[0].ComparisonKey += "|tampered"
						return "|tampered"
					},
				},
				{
					name: "abstain",
					mutate: func(file *expectFile) string {
						file.Cases[0].Dimensions[0].Abstain = append(append([]string{}, file.Cases[0].Dimensions[0].Abstain...), "tampered")
						return "tampered"
					},
				},
				{
					name: "deleted fields",
					mutate: func(file *expectFile) string {
						file.Cases[0].Projection.DeletedFields = append(append([]string{}, file.Cases[0].Projection.DeletedFields...), "tampered")
						return "tampered"
					},
				},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					var current expectFile
					if err := decodeExpect(raw, &current); err != nil {
						t.Fatal(err)
					}
					token := tc.mutate(&current)
					tampered, err := json.Marshal(current)
					if err != nil {
						t.Fatal(err)
					}
					err = matchCapturedExpectRaw(t, dir, tampered)
					if err == nil {
						t.Fatal("tampered expect matched the replay")
					}
					diff := err.Error()
					if !strings.Contains(diff, "\n- ") || !strings.Contains(diff, "\n+ ") || !strings.Contains(diff, token) {
						t.Fatalf("diff is not reviewable:\n%s", diff)
					}
				})
			}
		})
	}
}

func TestCapturedExpectTransportRejectsWSS(t *testing.T) {
	for _, feature := range []string{"block-receipt-tx", "eip1898-object"} {
		err := validateExpectHeader(feature, "2026-09-23", expectFile{
			SchemaVersion: expectSchemaV1,
			BatchID:       "2026-09-23",
			Feature:       feature,
			Transport:     "wss",
			Cases:         []expectCase{{RequestID: "x", Method: methods.MethodGetBalance, Mode: "majority", Floor: 2, N: 1}},
		})
		if err == nil || !strings.Contains(err.Error(), "wss") || !strings.Contains(err.Error(), "null") {
			t.Fatalf("%s err = %v", feature, err)
		}
	}
}

// TestRegisteredMethodSampleStatus pins which registered methods have an
// archived capture and which do not. Uncaptured methods stay without an
// expect.json; a synthetic body must not be described as a provider capture.
func TestRegisteredMethodSampleStatus(t *testing.T) {
	archived := map[string]string{}
	for _, feature := range []string{"revert-data", "block-receipt-tx", "eip1898-object"} {
		_, records, err := replay.LoadBatch(batchDir(feature))
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range records {
			archived[record.Method] = feature
		}
	}
	noCapture := map[string]bool{
		methods.MethodGetLogs:              true,
		methods.MethodFeeHistory:           true,
		methods.MethodChainID:              true,
		methods.MethodNetVersion:           true,
		methods.MethodGasPrice:             true,
		methods.MethodMaxPriorityFeePerGas: true,
		methods.MethodBlobBaseFee:          true,
		methods.MethodBlockNumber:          true,
		methods.MethodSyncing:              true,
		methods.MethodPeerCount:            true,
		methods.MethodListening:            true,
	}
	for _, name := range methods.RegisteredNames() {
		feature, has := archived[name]
		if noCapture[name] == has {
			t.Fatalf("%s archive=%q declared-uncaptured=%v", name, feature, noCapture[name])
		}
	}
}

func matchCapturedExpect(t *testing.T, feature string) error {
	t.Helper()
	dir := batchDir(feature)
	raw, err := os.ReadFile(filepath.Join(dir, "expect.json"))
	if err != nil {
		return err
	}
	return matchCapturedExpectRaw(t, dir, raw)
}

func matchCapturedExpectRaw(t *testing.T, dir string, raw []byte) error {
	t.Helper()
	var want expectFile
	if err := decodeExpect(raw, &want); err != nil {
		return err
	}
	feature := filepath.Base(filepath.Dir(dir))
	batchID := filepath.Base(dir)
	if err := validateExpectHeader(feature, batchID, want); err != nil {
		return err
	}
	got, err := measureCapturedExpect(t, feature, want)
	if err != nil {
		return err
	}
	wantCanon, err := canonicalExpect(want)
	if err != nil {
		return err
	}
	gotCanon, err := canonicalExpect(got)
	if err != nil {
		return err
	}
	if wantCanon == gotCanon {
		return nil
	}
	return fmt.Errorf("golden mismatch\n%s", reviewableDiff(wantCanon, gotCanon))
}

func measureCapturedExpect(t *testing.T, feature string, want expectFile) (expectFile, error) {
	t.Helper()
	_, records, err := replay.LoadBatch(batchDir(feature))
	if err != nil {
		return expectFile{}, err
	}
	groups := groupRecords(records)
	if len(groups) != len(want.Cases) {
		return expectFile{}, fmt.Errorf("cases = %d, requests = %d", len(want.Cases), len(groups))
	}
	got := expectFile{
		SchemaVersion: want.SchemaVersion,
		BatchID:       want.BatchID,
		Feature:       want.Feature,
		Transport:     want.Transport,
	}
	for i, group := range groups {
		pinned := want.Cases[i]
		if pinned.RequestID != group[0].RequestID {
			return expectFile{}, fmt.Errorf("case %d request_id = %s, index has %s", i, pinned.RequestID, group[0].RequestID)
		}
		measured, err := replayCapturedCase(t, pinned.Mode, pinned.Floor, group)
		if err != nil {
			return expectFile{}, err
		}
		got.Cases = append(got.Cases, measured)
	}
	return got, nil
}

func replayCapturedCase(t *testing.T, mode string, floor int, records []rpcsample.Record) (expectCase, error) {
	t.Helper()
	if len(records) == 0 {
		return expectCase{}, fmt.Errorf("empty request group")
	}
	id := records[0].RequestID
	method := records[0].Method
	for _, record := range records {
		if record.Method != method || record.RequestID != id {
			return expectCase{}, fmt.Errorf("%s/%s is not one %s request", record.RequestID, record.Source.ID, method)
		}
	}
	requestRaw, err := capturedRequest(records)
	if err != nil {
		return expectCase{}, err
	}
	snap, err := snapshotOf(mode, floor)
	if err != nil {
		return expectCase{}, err
	}
	policy := replayRegistry(taggedAnchor, nil)[method]
	if policy == nil {
		return expectCase{}, fmt.Errorf("%s has no replay policy", method)
	}
	req := requestOf(t, records[0])
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	prepared, prepErr := policy.Prepare(ctx, req)

	obs := make([]rpc.Observation, len(records))
	sources := make([]expectSource, len(records))
	payloads := make([]json.RawMessage, len(records))
	for i, record := range records {
		item := observationFromRecord(t, record)
		payload, admission, faults, key := classifySource(t, policy, req, item)
		payloads[i] = payload
		if requestDiffers(record, requestRaw) {
			faults = append(append([]string{}, faults...), "request_differs")
		}
		sources[i] = expectSource{ID: record.Source.ID, ComparisonKey: key, Admission: admission, Faults: faults}
		if prepErr == nil {
			item = admitCaptured(t, policy, prepared, item)
		}
		obs[i] = item
	}
	policyDecision, policyErr := resolveCaptured(policy, prepared, prepErr, snap, obs)

	names := make([]string, len(records))
	for i, record := range records {
		names[i] = record.Source.ID
	}
	shared, err := rpc.NewCorroborationShared(snap.Mode, snap.Floor)
	if err != nil {
		return expectCase{}, err
	}
	agg, nodes := newNamedReplayAggregator(t, names, shared)
	scriptTaggedHeads(nodes)
	for i, record := range records {
		scriptRecord(t, nodes[i], record)
	}
	aggDecision, aggErr := agg.Aggregate(ctx, req)
	if policyDecision.Status != aggDecision.Status || decisionError(policyErr) != decisionError(aggErr) {
		return expectCase{}, fmt.Errorf("%s policy %s/%s aggregator %s/%s", id, policyDecision.Status, decisionError(policyErr), aggDecision.Status, decisionError(aggErr))
	}
	if !sameCapturedValue(t, policyDecision.Value, aggDecision.Value) {
		return expectCase{}, fmt.Errorf("%s value diverged\npolicy %s\naggregator %s", id, policyDecision.Value, aggDecision.Value)
	}
	if strings.Join(namesOf(policyDecision.Support), ",") != strings.Join(namesOf(aggDecision.Support), ",") || strings.Join(namesOf(policyDecision.Reject), ",") != strings.Join(namesOf(aggDecision.Reject), ",") {
		return expectCase{}, fmt.Errorf("%s support/reject diverged: policy %v/%v aggregator %v/%v", id, namesOf(policyDecision.Support), namesOf(policyDecision.Reject), namesOf(aggDecision.Support), namesOf(aggDecision.Reject))
	}
	// The aggregator keeps resolver faults and also appends one source fault
	// for every unusable answer (transport, rejected protocol error). The
	// two layers still have to agree on that sum; a missing or doubled
	// fault fails the case.
	unusable := 0
	if prepErr == nil {
		unusable = unusableAnswers(policy, obs)
	}
	if len(aggDecision.Faults) != len(policyDecision.Faults)+unusable {
		return expectCase{}, fmt.Errorf("%s faults policy %d aggregator %d unusable %d", id, len(policyDecision.Faults), len(aggDecision.Faults), unusable)
	}
	if strings.Join(policyDecision.DeletedFields, ",") != strings.Join(aggDecision.DeletedFields, ",") || !sameDimensions(policyDecision.Dimensions, aggDecision.Dimensions) {
		return expectCase{}, fmt.Errorf("%s projection diverged", id)
	}
	if policyDecision.Representative.Name != aggDecision.Representative.Name {
		return expectCase{}, fmt.Errorf("%s representative policy %s aggregator %s", id, policyDecision.Representative.Name, aggDecision.Representative.Name)
	}
	if err := assertCapturedFanout(nodes, req, prepErr); err != nil {
		return expectCase{}, fmt.Errorf("%s: %w", id, err)
	}
	if prepErr == nil {
		if err := assertCapturedGate(t, agg, policy, prepared, req, policyDecision, policyErr); err != nil {
			return expectCase{}, fmt.Errorf("%s: %w", id, err)
		}
	}
	deleted := append([]string{}, policyDecision.DeletedFields...)
	if deleted == nil {
		deleted = []string{}
	}
	if err := replayValueAgrees(t, policy, policyDecision, payloads, sources, deleted, true); err != nil {
		return expectCase{}, fmt.Errorf("%s %w", id, err)
	}
	dims := capturedDimensions(policyDecision, policyErr)
	return expectCase{
		RequestID:  id,
		Method:     method,
		Request:    requestRaw,
		Mode:       mode,
		Floor:      floor,
		N:          len(records),
		Sources:    sources,
		Dimensions: expectDimensions(dims),
		Projection: expectProjection{Reductive: len(policyDecision.DeletedFields) > 0, DeletedFields: deleted},
		Decision: expectDecision{
			Status:         policyDecision.Status.String(),
			Error:          decisionError(policyErr),
			Representative: representativeOf(policyDecision, records),
			ValueSHA256:    capturedValueSHA256(method, policyDecision.Value),
		},
	}, nil
}

func capturedValueSHA256(method string, value json.RawMessage) string {
	if method != methods.MethodGetBlockByHash && method != methods.MethodGetBlockByNumber || len(value) == 0 {
		return ""
	}
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func unusableAnswers(policy rpc.Policy, obs []rpc.Observation) int {
	include := false
	if provider, ok := policy.(rpc.RPCErrorPolicy); ok {
		include = provider.IncludeRPCError()
	}
	count := 0
	for _, item := range obs {
		if item.Err == nil {
			continue
		}
		if item.RPCError != nil && include {
			if _, err := policy.Normalize(item.RPCError.Raw()); err == nil {
				continue
			}
		}
		count++
	}
	return count
}

func resolveCaptured(policy rpc.Policy, prepared rpc.Request, prepErr error, snap rpc.Corroboration, obs []rpc.Observation) (rpc.Decision, error) {
	if prepErr != nil {
		return rpc.Decision{Status: rpc.Unknown}, prepErr
	}
	if local, ok := policy.(rpc.LocalObservationPolicy); ok && local.IsLocalObservation(prepared) {
		return local.ResolveLocal(obs)
	}
	if resolver, ok := policy.(rpc.CorroboratingPolicy); ok {
		return resolver.ResolveCorroborated(snap, obs)
	}
	return policy.Resolve(obs)
}

func admitCaptured(t *testing.T, policy rpc.Policy, prepared rpc.Request, item rpc.Observation) rpc.Observation {
	t.Helper()
	if item.Err != nil || len(item.Value) == 0 {
		return item
	}
	admit, ok := policy.(rpc.AdmissionPolicy)
	if !ok {
		return item
	}
	if _, err := admit.Admit(context.Background(), prepared, item.Value, refuseCaller(t)); err != nil {
		item.Err = fmt.Errorf("admission: %w", err)
		item.Value = nil
		item.RPCError = nil
	}
	return item
}

func capturedDimensions(decision rpc.Decision, decisionErr error) []rpc.DimensionEvidence {
	if decision.Dimensions != nil {
		return decision.Dimensions
	}
	returned := decisionErr == nil && (decision.Status == rpc.Corroborated || decision.Status == rpc.Selected) && len(decision.Value) > 0
	return []rpc.DimensionEvidence{{
		Name:     "value",
		Support:  decision.Support,
		Reject:   decision.Reject,
		Abstain:  []rpc.SourceRef{},
		Returned: returned,
	}}
}

func assertCapturedGate(t *testing.T, agg *aggregator.Aggregator, policy rpc.Policy, prepared rpc.Request, req rpc.Request, decision rpc.Decision, decisionErr error) error {
	t.Helper()
	agreement, ok := policy.(rpc.AgreementPolicy)
	if !ok || !agreement.RequiresAgreement() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	gated, gatedErr := agg.Aggregate(refcontext.RequireCorroborated(ctx), req)
	pending := false
	if local, ok := policy.(rpc.LocalObservationPolicy); ok {
		pending = local.IsLocalObservation(prepared)
	}
	if pending {
		if gatedErr != nil || gated.Status != rpc.Selected {
			return fmt.Errorf("pending typed gate status %s err %v", gated.Status, gatedErr)
		}
		return nil
	}
	if decisionErr == nil && decision.Status == rpc.Corroborated {
		if gatedErr != nil || gated.Status != rpc.Corroborated {
			return fmt.Errorf("typed gate status %s err %v", gated.Status, gatedErr)
		}
		return nil
	}
	if gatedErr == nil && gated.Status == rpc.Corroborated {
		return fmt.Errorf("typed gate corroborated an uncorroborated %s", req.Method)
	}
	return nil
}

func sameCapturedValue(t *testing.T, left, right json.RawMessage) bool {
	t.Helper()
	if len(strings.TrimSpace(string(left))) == 0 && len(strings.TrimSpace(string(right))) == 0 {
		return true
	}
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	return canonicalJSON(t, left) == canonicalJSON(t, right)
}

func assertCapturedFanout(nodes []*rpctest.Node, req rpc.Request, prepErr error) error {
	if prepErr != nil {
		for _, node := range nodes {
			if methodCount(node, req.Method) != 0 || methodCount(node, methods.MethodGetBlockByNumber) != 0 {
				return fmt.Errorf("invalid request still fanned out: %v", node.Calls())
			}
		}
		return nil
	}
	pos, tracked := blockParamPos(req.Method)
	if !tracked || pos >= len(req.Params) {
		for _, node := range nodes {
			if methodCount(node, req.Method) == 0 {
				return fmt.Errorf("%s sent no business request", req.Method)
			}
		}
		return nil
	}
	tag, moving := movingReference(req.Params[pos])
	for _, node := range nodes {
		business := callParamValues(node, req.Method, pos)
		if len(business) == 0 {
			return fmt.Errorf("%s sent no business request", req.Method)
		}
		var want any
		if moving {
			want = rewrittenReference(req.Params[pos], tag)
		} else {
			want = req.Params[pos]
		}
		for _, got := range business {
			if canonicalAny(got) != canonicalAny(want) {
				return fmt.Errorf("%s wire = %s, want %s", req.Method, canonicalAny(got), canonicalAny(want))
			}
		}
		if req.Method == methods.MethodGetBlockByNumber {
			continue
		}
		anchors := callParamValues(node, methods.MethodGetBlockByNumber, 0)
		if !moving {
			if len(anchors) != 0 {
				return fmt.Errorf("%s anchored a fixed reference: %v", req.Method, anchors)
			}
			continue
		}
		seen := false
		for _, got := range anchors {
			text, _ := got.(string)
			if text == "latest" && tag != "latest" {
				return fmt.Errorf("%s anchor collapsed %s to latest", req.Method, tag)
			}
			if strings.EqualFold(text, tag) {
				seen = true
			}
		}
		if !seen {
			return fmt.Errorf("%s did not anchor at %s: %v", req.Method, tag, anchors)
		}
	}
	return nil
}

func blockParamPos(method string) (int, bool) {
	switch method {
	case methods.MethodGetBalance, methods.MethodGetTransactionCount, methods.MethodGetCode, methods.MethodCall, methods.MethodEstimateGas, methods.MethodCreateAccessList, methods.MethodGetBlockByNumber, methods.MethodGetBlockReceipts:
		if method == methods.MethodGetBlockByNumber || method == methods.MethodGetBlockReceipts {
			return 0, true
		}
		return 1, true
	case methods.MethodGetStorageAt:
		return 2, true
	default:
		return 0, false
	}
}

func movingReference(param any) (string, bool) {
	switch value := param.(type) {
	case string:
		tag := strings.ToLower(value)
		if tag == "latest" || tag == "safe" || tag == "finalized" {
			return tag, true
		}
	case map[string]any:
		if _, ok := value["blockHash"]; ok {
			return "", false
		}
		number, ok := value["blockNumber"].(string)
		if !ok {
			return "", false
		}
		tag := strings.ToLower(number)
		if tag == "latest" || tag == "safe" || tag == "finalized" {
			return tag, true
		}
	}
	return "", false
}

func rewrittenReference(param any, tag string) any {
	hash := replayHeadHash
	switch tag {
	case "safe":
		hash = replaySafeHash
	case "finalized":
		hash = replayFinalizedHash
	}
	switch value := param.(type) {
	case string:
		return hash
	case map[string]any:
		out := map[string]any{"blockHash": hash}
		if value["requireCanonical"] == true {
			out["requireCanonical"] = true
		}
		return out
	default:
		return hash
	}
}

func callParamValues(node *rpctest.Node, method string, index int) []any {
	var out []any
	for _, call := range node.Calls() {
		if call.Method != method || len(call.Params) <= index {
			continue
		}
		var value any
		if json.Unmarshal(call.Params[index], &value) != nil {
			continue
		}
		out = append(out, value)
	}
	return out
}

func canonicalAny(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(raw)
}

func capturedRequest(records []rpcsample.Record) (json.RawMessage, error) {
	var first string
	var raw json.RawMessage
	for _, record := range records {
		if strings.TrimSpace(record.Response.Body) == "" {
			continue
		}
		compacted, err := compactRequest(record)
		if err != nil {
			return nil, err
		}
		if first == "" {
			first = compacted
			raw = json.RawMessage(compacted)
			continue
		}
		if compacted != first {
			return nil, fmt.Errorf("%s request differs across sources", record.RequestID)
		}
	}
	if first == "" {
		return sameRequest(records)
	}
	return raw, nil
}

func requestDiffers(record rpcsample.Record, pinned json.RawMessage) bool {
	compacted, err := compactRequest(record)
	if err != nil {
		return true
	}
	return compacted != string(pinned)
}

func compactRequest(record rpcsample.Record) (string, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(record.Request)); err != nil {
		return "", fmt.Errorf("%s request: %w", record.RequestID, err)
	}
	return buf.String(), nil
}
