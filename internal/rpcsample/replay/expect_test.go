package replay_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ethguard/internal/refcontext"
	"ethguard/internal/rpcsample"
	"ethguard/internal/rpcsample/replay"
	"ethguard/rpc"
	"ethguard/rpc/methods"
)

// expectSchemaV1 is the golden data format. The file pins a request, the
// corroboration configuration, and the measured outcome. It is not a test
// language: no conditions, inheritance, or wildcards.
const expectSchemaV1 = "ethguard.expect/v1"

// Golden replay for the first archived batch. Transport http is the only
// replay this file asserts. WSS still collapses a missing data field into
// null, so a WSS golden is rejected instead of compared. Abstain and
// reductive projection are part of the compared document.
func TestRevertDataBatchMatchesExpect(t *testing.T) {
	dir := batchDir("revert-data")
	raw, err := os.ReadFile(filepath.Join(dir, "expect.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := matchBatchExpect(t, dir, raw); err != nil {
		t.Fatal(err)
	}
}

func TestExpectTamperProducesReviewableDiff(t *testing.T) {
	dir := batchDir("revert-data")
	raw, err := os.ReadFile(filepath.Join(dir, "expect.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file expectFile
	if err := decodeExpect(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Cases) == 0 || len(file.Cases[0].Sources) == 0 || len(file.Cases[0].Dimensions) == 0 {
		t.Fatal("expect.json has nothing to tamper")
	}
	cases := []struct {
		name   string
		mutate func(*expectFile) (before, after string)
	}{
		{
			name: "comparison key",
			mutate: func(file *expectFile) (string, string) {
				original := file.Cases[0].Sources[0].ComparisonKey
				file.Cases[0].Sources[0].ComparisonKey = original + "|tampered"
				return original, "|tampered"
			},
		},
		{
			name: "abstain",
			mutate: func(file *expectFile) (string, string) {
				file.Cases[0].Dimensions[0].Abstain = append(append([]string{}, file.Cases[0].Dimensions[0].Abstain...), "tampered")
				return `"abstain"`, "tampered"
			},
		},
		{
			name: "deleted fields",
			mutate: func(file *expectFile) (string, string) {
				file.Cases[0].Projection.DeletedFields = append(append([]string{}, file.Cases[0].Projection.DeletedFields...), "tampered")
				return `"data"`, "tampered"
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var current expectFile
			if err := decodeExpect(raw, &current); err != nil {
				t.Fatal(err)
			}
			before, after := tc.mutate(&current)
			tampered, err := json.Marshal(current)
			if err != nil {
				t.Fatal(err)
			}
			err = matchBatchExpect(t, dir, tampered)
			if err == nil {
				t.Fatal("tampered expect matched the replay")
			}
			diff := err.Error()
			if !strings.Contains(diff, "\n- ") || !strings.Contains(diff, "\n+ ") || !strings.Contains(diff, before) || !strings.Contains(diff, after) {
				t.Fatalf("diff is not reviewable:\n%s", diff)
			}
		})
	}
}

func TestExpectTransportRejectsWSS(t *testing.T) {
	err := validateExpectHeader("revert-data", "2026-09-23", expectFile{
		SchemaVersion: expectSchemaV1,
		BatchID:       "2026-09-23",
		Feature:       "revert-data",
		Transport:     "wss",
		Cases:         []expectCase{{RequestID: "x", Method: methods.MethodCall, Mode: "majority", Floor: 2, N: 1}},
	})
	if err == nil || !strings.Contains(err.Error(), "wss") || !strings.Contains(err.Error(), "null") {
		t.Fatalf("err = %v", err)
	}
}

func matchBatchExpect(t *testing.T, dir string, raw []byte) error {
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
	_, records, err := replay.LoadBatch(dir)
	if err != nil {
		return err
	}
	groups := groupRecords(records)
	if len(groups) != len(want.Cases) {
		return fmt.Errorf("cases = %d, requests = %d", len(want.Cases), len(groups))
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
			return fmt.Errorf("case %d request_id = %s, index has %s", i, pinned.RequestID, group[0].RequestID)
		}
		snap, err := snapshotOf(pinned.Mode, pinned.Floor)
		if err != nil {
			return fmt.Errorf("%s: %w", pinned.RequestID, err)
		}
		measured, err := replayRevertCase(t, snap, group)
		if err != nil {
			return err
		}
		measured.Mode = pinned.Mode
		measured.Floor = pinned.Floor
		got.Cases = append(got.Cases, measured)
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

func replayRevertCase(t *testing.T, snap rpc.Corroboration, records []rpcsample.Record) (expectCase, error) {
	t.Helper()
	if len(records) == 0 {
		return expectCase{}, errors.New("empty request group")
	}
	id := records[0].RequestID
	for _, record := range records {
		if record.Method != methods.MethodCall || record.RequestID != id {
			return expectCase{}, fmt.Errorf("%s/%s is not one eth_call request", record.RequestID, record.Source.ID)
		}
	}
	requestRaw, err := sameRequest(records)
	if err != nil {
		return expectCase{}, err
	}
	policy := methods.CallPolicy(fixedAnchor)
	resolver, ok := policy.(rpc.CorroboratingPolicy)
	if !ok {
		return expectCase{}, errors.New("eth_call policy lost ResolveCorroborated")
	}
	obs := make([]rpc.Observation, len(records))
	sources := make([]expectSource, len(records))
	payloads := make([]json.RawMessage, len(records))
	req := requestOf(t, records[0])
	for i, record := range records {
		item := observationFromRecord(t, record)
		obs[i] = item
		payload, admission, faults, key := classifySource(t, policy, req, item)
		payloads[i] = payload
		sources[i] = expectSource{ID: record.Source.ID, ComparisonKey: key, Admission: admission, Faults: faults}
	}
	policyDecision, policyErr := resolver.ResolveCorroborated(snap, obs)

	names := make([]string, len(records))
	for i, record := range records {
		names[i] = record.Source.ID
	}
	shared, err := rpc.NewCorroborationShared(snap.Mode, snap.Floor)
	if err != nil {
		return expectCase{}, err
	}
	agg, nodes := newNamedReplayAggregator(t, names, shared)
	scriptLatest(nodes)
	for i, record := range records {
		scriptRecord(t, nodes[i], record)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	aggDecision, aggErr := agg.Aggregate(ctx, req)
	if policyDecision.Status != aggDecision.Status || decisionError(policyErr) != decisionError(aggErr) {
		return expectCase{}, fmt.Errorf("%s policy %s/%s aggregator %s/%s", id, policyDecision.Status, decisionError(policyErr), aggDecision.Status, decisionError(aggErr))
	}
	if compact(t, policyDecision.Value) != compact(t, aggDecision.Value) {
		return expectCase{}, fmt.Errorf("%s value diverged\npolicy %s\naggregator %s", id, policyDecision.Value, aggDecision.Value)
	}
	if strings.Join(namesOf(policyDecision.Support), ",") != strings.Join(namesOf(aggDecision.Support), ",") || strings.Join(namesOf(policyDecision.Reject), ",") != strings.Join(namesOf(aggDecision.Reject), ",") {
		return expectCase{}, fmt.Errorf("%s support/reject diverged: policy %v/%v aggregator %v/%v", id, namesOf(policyDecision.Support), namesOf(policyDecision.Reject), namesOf(aggDecision.Support), namesOf(aggDecision.Reject))
	}
	if len(policyDecision.Faults) != len(aggDecision.Faults) {
		return expectCase{}, fmt.Errorf("%s faults policy %d aggregator %d", id, len(policyDecision.Faults), len(aggDecision.Faults))
	}
	if strings.Join(policyDecision.DeletedFields, ",") != strings.Join(aggDecision.DeletedFields, ",") || !sameDimensions(policyDecision.Dimensions, aggDecision.Dimensions) {
		return expectCase{}, fmt.Errorf("%s projection diverged", id)
	}
	if policyErr == nil && policyDecision.Status == rpc.Corroborated {
		gatedCtx, gatedCancel := context.WithTimeout(refcontext.RequireCorroborated(context.Background()), 5*time.Second)
		defer gatedCancel()
		gated, gatedErr := agg.Aggregate(gatedCtx, req)
		if gatedErr != nil || gated.Status != rpc.Corroborated {
			return expectCase{}, fmt.Errorf("%s typed gate status %s err %v", id, gated.Status, gatedErr)
		}
	}
	for _, node := range nodes {
		tags := callTags(node, methods.MethodCall, 1)
		if len(tags) == 0 {
			return expectCase{}, fmt.Errorf("%s did not send eth_call", id)
		}
		for _, tag := range tags {
			if tag != replayHeadHash {
				return expectCase{}, fmt.Errorf("%s block arg = %s", id, tag)
			}
		}
	}
	deleted := append([]string{}, policyDecision.DeletedFields...)
	if deleted == nil {
		deleted = []string{}
	}
	if !projectionMatches(t, policyDecision.Value, payloads, deleted) {
		return expectCase{}, fmt.Errorf("%s decision value is not one source payload with %v removed", id, deleted)
	}
	return expectCase{
		RequestID:  id,
		Method:     records[0].Method,
		Request:    requestRaw,
		N:          len(records),
		Sources:    sources,
		Dimensions: expectDimensions(policyDecision.Dimensions),
		Projection: expectProjection{Reductive: len(deleted) > 0, DeletedFields: deleted},
		Decision:   expectDecision{Status: policyDecision.Status.String(), Error: decisionError(policyErr)},
	}, nil
}

func classifySource(t *testing.T, policy rpc.Policy, req rpc.Request, item rpc.Observation) (json.RawMessage, string, []string, string) {
	t.Helper()
	faults := []string{}
	if item.Err == nil {
		payload := append(json.RawMessage(nil), item.Value...)
		if admit, ok := policy.(rpc.AdmissionPolicy); ok {
			if _, err := admit.Admit(context.Background(), req, item.Value, refuseCaller(t)); err != nil {
				return payload, "rejected", []string{err.Error()}, ""
			}
		}
		key, err := policy.Normalize(item.Value)
		if err != nil {
			return payload, "passed", []string{err.Error()}, ""
		}
		return payload, "passed", faults, key
	}
	if item.RPCError == nil {
		return nil, "not_applicable", []string{"fault"}, ""
	}
	payload := item.RPCError.Raw()
	key, err := policy.Normalize(payload)
	if err != nil {
		return payload, "not_applicable", []string{err.Error()}, ""
	}
	return payload, "not_applicable", faults, key
}

func snapshotOf(mode string, floor int) (rpc.Corroboration, error) {
	var parsed rpc.CorroborationMode
	switch mode {
	case "majority":
		parsed = rpc.CorroborationMajority
	case "unanimity":
		parsed = rpc.CorroborationUnanimity
	case "forwarded":
		parsed = rpc.CorroborationForwarded
	default:
		return rpc.Corroboration{}, fmt.Errorf("unknown corroboration mode %q", mode)
	}
	if _, err := rpc.NewCorroborationShared(parsed, floor); err != nil {
		return rpc.Corroboration{}, err
	}
	return rpc.Corroboration{Mode: parsed, Floor: floor}, nil
}

func decisionError(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, rpc.ErrConflict):
		return "conflict"
	case errors.Is(err, rpc.ErrNoValidResponse):
		return "no_valid_response"
	case errors.Is(err, rpc.ErrMalformedResponse):
		return "malformed"
	case errors.Is(err, rpc.ErrInvalidRequest):
		return "invalid_request"
	default:
		return "unexpected"
	}
}

func validateExpectHeader(feature, batchID string, file expectFile) error {
	if file.SchemaVersion != expectSchemaV1 {
		return fmt.Errorf("schema_version = %s", file.SchemaVersion)
	}
	if file.Feature != feature || file.BatchID != batchID {
		return fmt.Errorf("golden names %s/%s, directory is %s/%s", file.Feature, file.BatchID, feature, batchID)
	}
	if file.Transport != "http" {
		return fmt.Errorf("transport %s cannot use this HTTP golden; on WSS a missing data field collapses into null", file.Transport)
	}
	if len(file.Cases) == 0 {
		return errors.New("expect.json has no cases")
	}
	for _, item := range file.Cases {
		if item.RequestID == "" || item.Method == "" || item.N < 1 || item.N != len(item.Sources) {
			return fmt.Errorf("%s n = %d, sources = %d", item.RequestID, item.N, len(item.Sources))
		}
		if _, err := snapshotOf(item.Mode, item.Floor); err != nil {
			return fmt.Errorf("%s: %w", item.RequestID, err)
		}
		if item.Projection.DeletedFields == nil || len(item.Dimensions) == 0 {
			return fmt.Errorf("%s projection or dimensions are missing", item.RequestID)
		}
		for _, source := range item.Sources {
			if source.Faults == nil || source.ID == "" || source.Admission == "" {
				return fmt.Errorf("%s source %s is incomplete", item.RequestID, source.ID)
			}
		}
		for _, dim := range item.Dimensions {
			if dim.Name == "" || dim.Support == nil || dim.Reject == nil || dim.Abstain == nil {
				return fmt.Errorf("%s dimension %s is incomplete", item.RequestID, dim.Name)
			}
		}
	}
	return nil
}

func decodeExpect(raw []byte, file *expectFile) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(file); err != nil {
		return fmt.Errorf("expect.json: %w", err)
	}
	return nil
}

func canonicalExpect(file expectFile) (string, error) {
	for i := range file.Cases {
		if len(file.Cases[i].Request) == 0 {
			continue
		}
		var buf bytes.Buffer
		if err := json.Compact(&buf, file.Cases[i].Request); err != nil {
			return "", fmt.Errorf("%s request: %w", file.Cases[i].RequestID, err)
		}
		file.Cases[i].Request = append(json.RawMessage(nil), buf.Bytes()...)
	}
	raw, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return "", err
	}
	return string(raw) + "\n", nil
}

func groupRecords(records []rpcsample.Record) [][]rpcsample.Record {
	var order []string
	grouped := map[string][]rpcsample.Record{}
	for _, record := range records {
		if _, ok := grouped[record.RequestID]; !ok {
			order = append(order, record.RequestID)
		}
		grouped[record.RequestID] = append(grouped[record.RequestID], record)
	}
	out := make([][]rpcsample.Record, 0, len(order))
	for _, id := range order {
		out = append(out, grouped[id])
	}
	return out
}

func sameRequest(records []rpcsample.Record) (json.RawMessage, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(records[0].Request)); err != nil {
		return nil, fmt.Errorf("%s request: %w", records[0].RequestID, err)
	}
	first := buf.String()
	for _, record := range records[1:] {
		buf.Reset()
		if err := json.Compact(&buf, []byte(record.Request)); err != nil {
			return nil, err
		}
		if buf.String() != first {
			return nil, fmt.Errorf("%s request differs across sources", record.RequestID)
		}
	}
	return json.RawMessage(first), nil
}

func payloadMatches(t *testing.T, value json.RawMessage, payloads []json.RawMessage) bool {
	t.Helper()
	return projectionMatches(t, value, payloads, nil)
}

func projectionMatches(t *testing.T, value json.RawMessage, payloads []json.RawMessage, deleted []string) bool {
	t.Helper()
	if len(value) == 0 || containsFields(t, value, deleted) {
		return false
	}
	got := canonicalJSON(t, value)
	for _, payload := range payloads {
		if payload == nil {
			continue
		}
		reduced := payload
		if len(deleted) > 0 {
			var err error
			reduced, err = deleteFields(payload, deleted)
			if err != nil {
				continue
			}
		}
		if canonicalJSON(t, reduced) == got {
			return true
		}
	}
	return false
}

func containsFields(t *testing.T, raw json.RawMessage, fields []string) bool {
	t.Helper()
	if len(fields) == 0 {
		return false
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal(raw, &doc) != nil {
		return true
	}
	for _, field := range fields {
		if _, ok := doc[field]; ok {
			return true
		}
	}
	return false
}

func deleteFields(raw json.RawMessage, fields []string) (json.RawMessage, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil || doc == nil {
		return nil, err
	}
	for _, field := range fields {
		delete(doc, field)
	}
	return json.Marshal(doc)
}

func canonicalJSON(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("canonical %s: %v", raw, err)
	}
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func expectDimensions(dims []rpc.DimensionEvidence) []expectDimension {
	out := make([]expectDimension, 0, len(dims))
	for _, dim := range dims {
		out = append(out, expectDimension{
			Name:     dim.Name,
			Support:  nonNilNames(dim.Support),
			Reject:   nonNilNames(dim.Reject),
			Abstain:  nonNilNames(dim.Abstain),
			Returned: dim.Returned,
		})
	}
	return out
}

func nonNilNames(refs []rpc.SourceRef) []string {
	names := namesOf(refs)
	if names == nil {
		return []string{}
	}
	return names
}

func sameDimensions(left, right []rpc.DimensionEvidence) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].Name != right[i].Name || left[i].Returned != right[i].Returned {
			return false
		}
		if strings.Join(namesOf(left[i].Support), ",") != strings.Join(namesOf(right[i].Support), ",") {
			return false
		}
		if strings.Join(namesOf(left[i].Reject), ",") != strings.Join(namesOf(right[i].Reject), ",") {
			return false
		}
		if strings.Join(namesOf(left[i].Abstain), ",") != strings.Join(namesOf(right[i].Abstain), ",") {
			return false
		}
	}
	return true
}

func namesOf(refs []rpc.SourceRef) []string {
	out := make([]string, len(refs))
	for i, ref := range refs {
		out[i] = ref.Name
	}
	return out
}

func reviewableDiff(before, after string) string {
	a := strings.Split(strings.TrimSuffix(before, "\n"), "\n")
	b := strings.Split(strings.TrimSuffix(after, "\n"), "\n")
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
				continue
			}
			if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
				continue
			}
			dp[i][j] = dp[i][j+1]
		}
	}
	var buf strings.Builder
	buf.WriteString("--- expect\n+++ replay\n")
	i, j := 0, 0
	for i < n && j < m {
		if a[i] == b[j] {
			i++
			j++
			continue
		}
		if dp[i+1][j] >= dp[i][j+1] {
			buf.WriteString("- " + a[i] + "\n")
			i++
			continue
		}
		buf.WriteString("+ " + b[j] + "\n")
		j++
	}
	for ; i < n; i++ {
		buf.WriteString("- " + a[i] + "\n")
	}
	for ; j < m; j++ {
		buf.WriteString("+ " + b[j] + "\n")
	}
	return buf.String()
}

type expectFile struct {
	SchemaVersion string       `json:"schema_version"`
	BatchID       string       `json:"batch_id"`
	Feature       string       `json:"feature"`
	Transport     string       `json:"transport"`
	Cases         []expectCase `json:"cases"`
}

type expectCase struct {
	RequestID  string            `json:"request_id"`
	Method     string            `json:"method"`
	Request    json.RawMessage   `json:"request"`
	Mode       string            `json:"mode"`
	Floor      int               `json:"floor"`
	N          int               `json:"n"`
	Sources    []expectSource    `json:"sources"`
	Dimensions []expectDimension `json:"dimensions"`
	Projection expectProjection  `json:"projection"`
	Decision   expectDecision    `json:"decision"`
}

type expectSource struct {
	ID            string   `json:"id"`
	ComparisonKey string   `json:"comparison_key"`
	Admission     string   `json:"admission"`
	Faults        []string `json:"faults"`
}

type expectDimension struct {
	Name     string   `json:"name"`
	Support  []string `json:"support"`
	Reject   []string `json:"reject"`
	Abstain  []string `json:"abstain"`
	Returned bool     `json:"returned"`
}

type expectProjection struct {
	Reductive     bool     `json:"reductive"`
	DeletedFields []string `json:"deleted_fields"`
}

type expectDecision struct {
	Status string `json:"status"`
	Error  string `json:"error"`
}
