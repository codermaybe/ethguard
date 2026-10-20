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

// TestReadLocalBatchMatchesExpect runs the read-local batch's own expect
// replay entry. Without this entry the expect.json would exist but never be
// executed (capture-tasks.md T1.5).
func TestReadLocalBatchMatchesExpect(t *testing.T) {
	if err := matchCapturedExpect(t, "read-local"); err != nil {
		t.Fatal(err)
	}
}

func TestReadVolatileBatchMatchesExpect(t *testing.T) {
	if err := matchCapturedExpect(t, "read-volatile"); err != nil {
		t.Fatal(err)
	}
}

func TestCapturedExpectTamperProducesReviewableDiff(t *testing.T) {
	for _, feature := range []string{"block-receipt-tx", "eip1898-object", "read-local", "read-volatile"} {
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
	for _, feature := range []string{"block-receipt-tx", "eip1898-object", "read-local", "read-volatile"} {
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
	for _, feature := range []string{"revert-data", "block-receipt-tx", "eip1898-object", "read-local", "read-volatile"} {
		_, records, err := replay.LoadBatch(batchDir(feature))
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range records {
			archived[record.Method] = feature
		}
	}
	noCapture := map[string]bool{
		methods.MethodGetLogs:    true,
		methods.MethodFeeHistory: true,
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
	dir := batchDir(feature)
	index, batch, err := replay.LoadBatch(dir)
	if err != nil {
		return expectFile{}, err
	}
	meta, err := replay.LoadBatchMeta(dir)
	if err != nil {
		return expectFile{}, err
	}
	records, err := replay.BusinessRecords(meta, index, batch)
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
		measured, err := capturedCase(t, meta, batch, pinned.Mode, pinned.Floor, group)
		if err != nil {
			return expectFile{}, err
		}
		got.Cases = append(got.Cases, measured)
	}
	return got, nil
}

// capturedCase resolves the batch reference_context entry for one business
// request and replays the case with it. A block-reference case (contract G2:
// getLogs / feeHistory with a moving tag) without an entry is a replay
// failure, not a fallback to an offline simulated height.
func capturedCase(t *testing.T, meta replay.BatchMeta, batch []rpcsample.Record, mode string, floor int, group []rpcsample.Record) (expectCase, error) {
	t.Helper()
	entry := lookupReferenceContext(meta, group[0].RequestID)
	if entry == nil && requiresReferenceContext(group) {
		return expectCase{}, fmt.Errorf("%s is a block-reference case with no reference_context", group[0].RequestID)
	}
	return replayCapturedCase(t, mode, floor, group, batch, entry)
}

func lookupReferenceContext(meta replay.BatchMeta, requestID string) *replay.BatchReferenceContext {
	if len(meta.ReferenceContext) == 0 {
		return nil
	}
	entry, ok := meta.ReferenceContext[requestID]
	if !ok {
		return nil
	}
	return &entry
}

// requiresReferenceContext reports whether contract G2 pins this captured
// case to a batch reference_context entry: only block-reference methods with
// a moving tag in the recorded request do.
func requiresReferenceContext(group []rpcsample.Record) bool {
	if len(group) == 0 {
		return false
	}
	switch group[0].Method {
	case methods.MethodGetLogs, methods.MethodFeeHistory:
	default:
		return false
	}
	var wire struct {
		Params []any `json:"params"`
	}
	if err := json.Unmarshal([]byte(group[0].Request), &wire); err != nil {
		return false
	}
	for _, param := range wire.Params {
		if _, moving := movingReference(param); moving {
			return true
		}
	}
	return false
}

func replayCapturedCase(t *testing.T, mode string, floor int, group []rpcsample.Record, batch []rpcsample.Record, entry *replay.BatchReferenceContext) (expectCase, error) {
	t.Helper()
	if len(group) == 0 {
		return expectCase{}, fmt.Errorf("empty request group")
	}
	id := group[0].RequestID
	method := group[0].Method
	for _, record := range group {
		if record.Method != method || record.RequestID != id {
			return expectCase{}, fmt.Errorf("%s/%s is not one %s request", record.RequestID, record.Source.ID, method)
		}
	}
	requestRaw, err := capturedRequest(group)
	if err != nil {
		return expectCase{}, err
	}
	snap, err := snapshotOf(mode, floor)
	if err != nil {
		return expectCase{}, err
	}
	req := requestOf(t, group[0])
	if entry != nil {
		if entry.LogicalRequest.Method != method {
			return expectCase{}, fmt.Errorf("%s reference context is for %s, record is %s", id, entry.LogicalRequest.Method, method)
		}
		logical, err := logicalRequest(t, entry.LogicalRequest)
		if err != nil {
			return expectCase{}, fmt.Errorf("%s reference context: %w", id, err)
		}
		req = logical
	}
	// One batch reference context for both layers: the pool-backed height
	// resolver built here carries the same member order (index order) and is
	// handed to the Policy layer's registry and the Aggregator alike. The
	// mode/floor snapshot stays the single shared configuration; both layers
	// resolve through one reference path, no second quorum setup.
	names := make([]string, len(group))
	for i, record := range group {
		names[i] = record.Source.ID
	}
	shared, err := rpc.NewCorroborationShared(snap.Mode, snap.Floor)
	if err != nil {
		return expectCase{}, err
	}
	agg, nodes, sharedHeight := newSharedReplayAggregator(t, names, shared)
	bySource := map[string]*rpctest.Node{}
	for i, name := range names {
		bySource[name] = nodes[i]
	}
	if entry != nil {
		if err := membersMatchCase(names, entry.Members); err != nil {
			return expectCase{}, fmt.Errorf("%s: %w", id, err)
		}
		if err := scriptReferenceContext(t, bySource, *entry, batch); err != nil {
			return expectCase{}, fmt.Errorf("%s: %w", id, err)
		}
	} else {
		scriptTaggedHeads(nodes)
	}
	for _, record := range group {
		scriptRecord(t, bySource[record.Source.ID], record)
	}
	policy := replayRegistry(taggedAnchor, sharedHeight)[method]
	if policy == nil {
		return expectCase{}, fmt.Errorf("%s has no replay policy", method)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	prepared, prepErr := policy.Prepare(ctx, req)
	if entry != nil && prepErr == nil {
		// Three prepared-consistency gates, in contract terms:
		//  - the recorded request itself equals the prepared request built
		//    from the context's expected reference (record integrity);
		//  - the Policy layer's offline-recomputed prepared request equals
		//    that expected reference, too: the recomputation is asserted
		//    against the target, never fed into the resolver.
		if len(requestRaw) > 0 {
			if err := replay.MatchesPreparedRecord(*entry, requestRaw); err != nil {
				return expectCase{}, fmt.Errorf("%s prepared request does not match the record: %w", id, err)
			}
		}
		if wantMethod, wantParams, err := replay.PreparedBusinessRequest(*entry); err != nil {
			return expectCase{}, fmt.Errorf("%s reference context: %w", id, err)
		} else if canonicalAny(prepared.Params) != canonicalParamBytes(t, wantParams) {
			return expectCase{}, fmt.Errorf(
				"%s recomputed prepared request %s diverges from the expected reference %s",
				id, canonicalAny(prepared.Params), wantParams)
		} else if wantMethod != method {
			return expectCase{}, fmt.Errorf("%s prepared method %s, record %s", id, prepared.Method, method)
		}
	}

	obs := make([]rpc.Observation, len(group))
	sources := make([]expectSource, len(group))
	payloads := make([]json.RawMessage, len(group))
	// Admission binding runs against the request the Aggregator itself
	// advertises: for a batch reference context that is the prepared
	// (height-rewritten) request, so a response is bound to the recorded
	// height, not to the bare tag. Historical batches keep the recorded
	// request.
	admitReq := req
	if entry != nil && prepErr == nil {
		admitReq = prepared
	}
	for i, record := range group {
		item := observationFromRecord(t, record)
		payload, admission, faults, key := classifySource(t, policy, admitReq, item)
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
	if err := assertTwoLayerPreparedAgrees(t, nodes, method, prepared, prepErr); err != nil {
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
		N:          len(group),
		Sources:    sources,
		Dimensions: expectDimensions(dims),
		Projection: expectProjection{Reductive: len(policyDecision.DeletedFields) > 0, DeletedFields: deleted},
		Decision: expectDecision{
			Status:         policyDecision.Status.String(),
			Error:          decisionError(policyErr),
			Representative: representativeOf(policyDecision, group),
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
		// Logs-style range filters carry the moving tag in fromBlock/toBlock
		// (blockNumber keeps the object/position form used elsewhere).
		keys := []string{"blockNumber", "fromBlock", "toBlock"}
		for _, key := range keys {
			number, ok := value[key].(string)
			if !ok {
				continue
			}
			tag := strings.ToLower(number)
			if tag == "latest" || tag == "safe" || tag == "finalized" {
				return tag, true
			}
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
	var comparable string
	var raw json.RawMessage
	for _, record := range records {
		if strings.TrimSpace(record.Response.Body) == "" {
			continue
		}
		comparableDoc, err := comparableRequest(record)
		if err != nil {
			return nil, err
		}
		if first == "" {
			first, err = compactRequest(record)
			if err != nil {
				return nil, err
			}
			comparable = comparableDoc
			raw = json.RawMessage(first)
			continue
		}
		// Sources may run different JSON-RPC ids; request identity across
		// sources is method and params only.
		if comparableDoc != comparable {
			return nil, fmt.Errorf("%s request differs across sources", record.RequestID)
		}
	}
	if first == "" {
		return sameRequest(records)
	}
	return raw, nil
}

// canonicalParamBytes hashes one marshaled params document onto the same
// canonical form canonicalAny produces for Go-decoded params.
func canonicalParamBytes(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	buf, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("canonical params: %v", err)
	}
	return string(buf)
}

// logicalRequest rebuilds the pre-anchor request from the context entry's
// logical method and params. No JSON-RPC id is involved: ids are transport
// correlation labels, never part of a prepared comparison.
func logicalRequest(t *testing.T, doc replay.BatchLogicalRequest) (rpc.Request, error) {
	t.Helper()
	var params []any
	if len(doc.Params) > 0 {
		if err := json.Unmarshal(doc.Params, &params); err != nil {
			return rpc.Request{}, fmt.Errorf("logical params: %w", err)
		}
	}
	if params == nil {
		params = []any{}
	}
	return rpc.Request{Method: doc.Method, Params: params}, nil
}

// membersMatchCase enforces the shared-context contract: the reference
// context's member order is the batch member order (index order), the same
// list and sequence both replay layers are driven with.
func membersMatchCase(names []string, members []string) error {
	if len(names) != len(members) {
		return fmt.Errorf("reference members %v do not match the batch member order %v", members, names)
	}
	for i := range names {
		if names[i] != members[i] {
			return fmt.Errorf("reference member %d = %s, batch member order has %s", i, members[i], names[i])
		}
	}
	return nil
}

// scriptReferenceContext restores the anchor responses from the batch's
// stored reference records: the real anchor path is then driven by captured
// reference responses, never by an injected expected height. The expected
// height is checked afterwards, from what the resolver recomputed.
func scriptReferenceContext(t *testing.T, bySource map[string]*rpctest.Node, entry replay.BatchReferenceContext, batch []rpcsample.Record) error {
	t.Helper()
	stored := map[string]rpcsample.Record{}
	for _, record := range batch {
		stored[record.RequestID+"/"+record.Source.ID] = record
	}
	for _, ref := range entry.ReferenceRecords {
		record, ok := stored[ref.RequestID+"/"+ref.SourceID]
		if !ok {
			return fmt.Errorf("reference record %s/%s is missing from the batch", ref.RequestID, ref.SourceID)
		}
		node, ok := bySource[ref.SourceID]
		if !ok {
			return fmt.Errorf("reference record %s source %s is not a batch member", ref.RequestID, ref.SourceID)
		}
		scriptRecord(t, node, record)
	}
	return nil
}

// assertTwoLayerPreparedAgrees is the prepared-request check across the two
// layers: every business request the Aggregator put on the wire must equal
// the Policy layer's prepared request on method and params (the JSON-RPC id
// is not carried by replay and not compared). It covers both the historical
// offline anchor and a batch reference context, and it is where a moving tag
// resolving to different heights in the two layers would fail.
func assertTwoLayerPreparedAgrees(t *testing.T, nodes []*rpctest.Node, method string, prepared rpc.Request, prepErr error) error {
	t.Helper()
	if prepErr != nil {
		return nil
	}
	wire := canonicalAny(prepared.Params)
	for _, node := range nodes {
		calls := 0
		for _, item := range node.Calls() {
			if item.Method != method {
				continue
			}
			calls++
			var params []any
			for _, param := range item.Params {
				var value any
				if err := json.Unmarshal(param, &value); err != nil {
					return fmt.Errorf("%s wire param %s: %w", method, param, err)
				}
				params = append(params, value)
			}
			if params == nil {
				params = []any{}
			}
			if canonicalAny(params) != wire {
				return fmt.Errorf("aggregator %s wire params %s diverge from the prepared %s", method, canonicalAny(params), wire)
			}
		}
		if calls == 0 {
			return fmt.Errorf("%s sent no business request", method)
		}
	}
	return nil
}

func requestDiffers(record rpcsample.Record, pinned json.RawMessage) bool {
	comparable, err := comparableRequest(record)
	if err != nil {
		return true
	}
	pinnedComparable, err := comparableJSONRequest(pinned)
	if err != nil {
		return true
	}
	return comparable != pinnedComparable
}

func compactRequest(record rpcsample.Record) (string, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(record.Request)); err != nil {
		return "", fmt.Errorf("%s request: %w", record.RequestID, err)
	}
	return buf.String(), nil
}

// comparableRequest canonicalizes one recorded request onto method and
// params only. The JSON-RPC id is a transport correlation label and never
// part of the comparison (contract §2.1bis).
func comparableRequest(record rpcsample.Record) (string, error) {
	return comparableJSONRequest(json.RawMessage(record.Request))
}

func comparableJSONRequest(raw json.RawMessage) (string, error) {
	var doc struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", fmt.Errorf("request document: %w", err)
	}
	params := "null"
	if len(doc.Params) > 0 {
		var value any
		if err := json.Unmarshal(doc.Params, &value); err != nil {
			return "", fmt.Errorf("request params: %w", err)
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return "", fmt.Errorf("request params: %w", err)
		}
		params = string(encoded)
	}
	canonical, err := json.Marshal(struct {
		Method string `json:"method"`
		Params string `json:"params"`
	}{Method: doc.Method, Params: params})
	if err != nil {
		return "", fmt.Errorf("request identity: %w", err)
	}
	return string(canonical), nil
}

// Offline block-reference fixture helpers. The synthetic records are in
// memory only: no captured batch is invented, no test touches the network,
// and no expected height is injected into any resolver.

type recordSpec struct {
	requestID string
	sourceID  string
	method    string
	request   string
	response  string
}

// syntheticBatchRecords marshals specs into record structures the way the
// collector would have written them and returns the digest of each record's
// would-be file bytes, so reference_records can pin real stored bytes.
func syntheticBatchRecords(t *testing.T, specs []recordSpec) ([]rpcsample.Record, map[string]string) {
	t.Helper()
	records := make([]rpcsample.Record, 0, len(specs))
	shas := map[string]string{}
	for _, spec := range specs {
		document := map[string]any{
			"schema":      rpcsample.RecordSchema,
			"observed_at": "2026-10-18T12:00:00Z",
			"network":     map[string]any{"chain_id": 1, "fork": "unspecified"},
			"source":      map[string]any{"id": spec.sourceID, "endpoint": "[REDACTED]"},
			"request_id":  spec.requestID,
			"method":      spec.method,
			"request":     spec.request,
			"response":    map[string]any{"http_status": 200, "content_type": "application/json", "body": spec.response},
		}
		encoded, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		var record rpcsample.Record
		if err := json.Unmarshal(encoded, &record); err != nil {
			t.Fatal(err)
		}
		if record.RequestID != spec.requestID || record.Method != spec.method || record.Request != spec.request {
			t.Fatalf("synthetic record lost identity: %+v", record)
		}
		summary := sha256.Sum256(encoded)
		shas[spec.requestID+"/"+spec.sourceID] = hex.EncodeToString(summary[:])
		records = append(records, record)
	}
	return records, shas
}

func anchorSafeResponse(height uint64, hash string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"result":{"number":"0x%x","hash":"%s"}}`, height, hash)
}

func syntheticLogsEntry(shas map[string]string) replay.BatchReferenceContext {
	return replay.BatchReferenceContext{
		LogicalRequest: replay.BatchLogicalRequest{
			Method: methods.MethodGetLogs,
			Params: json.RawMessage(`[{"fromBlock":"safe","toBlock":"safe","topics":null}]`),
		},
		Tag:           "safe",
		EstablishedAt: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC),
		Members:       []string{"s1", "s2"},
		ReferenceRecords: []replay.BatchRecordRef{
			{RequestID: "anchor-safe", SourceID: "s1", SHA256: shas["anchor-safe/s1"]},
			{RequestID: "anchor-safe", SourceID: "s2", SHA256: shas["anchor-safe/s2"]},
		},
		ExpectedReference: replay.BatchExpectedReference{
			Height:  e2eSafeHeight,
			Hash:    replaySafeHash,
			Status:  "corroborated",
			Support: []string{"s1", "s2"},
		},
	}
}

const e2eSafeHeight = 0x0e // the safe head scriptTaggedHeads pins

func splitSyntheticBatch(t *testing.T, specs []recordSpec) (group []rpcsample.Record, batch []rpcsample.Record, shas map[string]string) {
	t.Helper()
	records, shas := syntheticBatchRecords(t, specs)
	for _, record := range records {
		batch = append(batch, record)
		if record.RequestID == "logs-safe" {
			group = append(group, record)
		}
	}
	if len(group) == 0 {
		t.Fatal("business group lost")
	}
	return group, batch, shas
}

// TestReferenceContextCaseRecoversMovingTagOffline covers the stage-0
// recovery contract end to end for one getLogs case: the anchor path is
// driven by the stored reference responses (the only height source), the
// Policy layer and the Aggregator layer rewrite the moving tag to the same
// reference height, and the recorded business request equals the prepared
// request the replay itself reconstructed.
func TestReferenceContextCaseRecoversMovingTagOffline(t *testing.T) {
	logsSpec := func(requestID int) recordSpec {
		return recordSpec{
			requestID: "logs-safe",
			sourceID:  fmt.Sprintf("s%d", requestID-10),
			method:    methods.MethodGetLogs,
			request:   fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"eth_getLogs","params":[{"fromBlock":"0xe","toBlock":"0xe","topics":null}]}`, requestID),
			response:  `{"jsonrpc":"2.0","id":11,"result":[]}`,
		}
	}
	group, batch, shas := splitSyntheticBatch(t, []recordSpec{
		{requestID: "anchor-safe", sourceID: "s1", method: methods.MethodGetBlockByNumber,
			request:  `{"jsonrpc":"2.0","id":3,"method":"eth_getBlockByNumber","params":["safe",false]}`,
			response: anchorSafeResponse(e2eSafeHeight, replaySafeHash)},
		{requestID: "anchor-safe", sourceID: "s2", method: methods.MethodGetBlockByNumber,
			request:  `{"jsonrpc":"2.0","id":3,"method":"eth_getBlockByNumber","params":["safe",false]}`,
			response: anchorSafeResponse(e2eSafeHeight, replaySafeHash)},
		logsSpec(11),
		logsSpec(12),
	})
	entry := syntheticLogsEntry(shas)
	measured, err := replayCapturedCase(t, "majority", 2, group, batch, &entry)
	if err != nil {
		t.Fatalf("replay failed: %v", err)
	}
	if measured.Decision.Status != rpc.Corroborated.String() {
		t.Fatalf("status = %s, want corroborated", measured.Decision.Status)
	}
	if measured.N != 2 || len(measured.Sources) != 2 {
		t.Fatalf("voting basis n=%d sources=%d, want the two members", measured.N, len(measured.Sources))
	}
	for _, source := range measured.Sources {
		if source.Admission != "passed" {
			t.Fatalf("%s admission = %s", source.ID, source.Admission)
		}
	}
	// The recorded request stays the actual prepared wire request.
	if !bytes.Contains(measured.Request, []byte(`"fromBlock":"0xe"`)) {
		t.Fatalf("recorded request lost the prepared height: %s", measured.Request)
	}
}

// TestReferenceContextRecordMismatchFails is the mismatch gate: a recorded
// business request whose height disagrees with the batch reference context
// must fail the replay, not be reinterpreted.
func TestReferenceContextRecordMismatchFails(t *testing.T) {
	logsSpec := func(requestID int) recordSpec {
		return recordSpec{
			requestID: "logs-safe",
			sourceID:  fmt.Sprintf("s%d", requestID-10),
			method:    methods.MethodGetLogs,
			request:   fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"eth_getLogs","params":[{"fromBlock":"0xf","toBlock":"0xf","topics":null}]}`, requestID),
			response:  `{"jsonrpc":"2.0","id":11,"result":[]}`,
		}
	}
	group, batch, shas := splitSyntheticBatch(t, []recordSpec{
		{requestID: "anchor-safe", sourceID: "s1", method: methods.MethodGetBlockByNumber,
			request:  `{"jsonrpc":"2.0","id":3,"method":"eth_getBlockByNumber","params":["safe",false]}`,
			response: anchorSafeResponse(e2eSafeHeight, replaySafeHash)},
		{requestID: "anchor-safe", sourceID: "s2", method: methods.MethodGetBlockByNumber,
			request:  `{"jsonrpc":"2.0","id":3,"method":"eth_getBlockByNumber","params":["safe",false]}`,
			response: anchorSafeResponse(e2eSafeHeight, replaySafeHash)},
		logsSpec(11),
		logsSpec(12),
	})
	entry := syntheticLogsEntry(shas)
	_, err := replayCapturedCase(t, "majority", 2, group, batch, &entry)
	if err == nil || !strings.Contains(err.Error(), "does not match the record") {
		t.Fatalf("mismatched record err = %v, want a prepared-vs-record failure", err)
	}
}

// TestBlockRefCaseWithoutContextFails closes the other missing side: a
// block-reference case (moving tag on getLogs) without a batch
// reference_context entry fails outright.
func TestBlockRefCaseWithoutContextFails(t *testing.T) {
	logical := `{"jsonrpc":"2.0","id":11,"method":"eth_getLogs","params":[{"fromBlock":"safe","toBlock":"safe","topics":null}]}`
	specs := []recordSpec{
		{requestID: "logs-safe", sourceID: "s1", method: methods.MethodGetLogs,
			request:  logical,
			response: `{"jsonrpc":"2.0","id":11,"result":[]}`},
	}
	records, _ := syntheticBatchRecords(t, specs)
	_, err := capturedCase(t, replay.BatchMeta{}, records, "majority", 2, records)
	if err == nil || !strings.Contains(err.Error(), "reference_context") {
		t.Fatalf("err = %v, want the missing reference_context failure", err)
	}
}
