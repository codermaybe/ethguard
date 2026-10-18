package replay

// Offline tests of the batch.json reference_context reading contract
// (capture-golden-contract §2.1bis). Fixtures are synthesized in temporary
// directories: this exercises the reader and validator, it does not invent
// provider captures, and no test touches the network.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/rpcsample"
)

type fixtureRecordSpec struct {
	RequestID string
	SourceID  string
	Method    string
	Request   string
	Response  string
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	return parseTime(value)
}

func parseTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return parsed
}

func establishTime() time.Time { return parseTime("2026-10-18T11:59:59Z") }

// writeFixtureRecords lays records/ and index.json under dir. The index
// sha256 is computed over the written record bytes exactly like the
// collector does.
func writeFixtureRecords(t *testing.T, dir string, records []fixtureRecordSpec) rpcsample.Index {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "records"), 0o700); err != nil {
		t.Fatal(err)
	}
	var index rpcsample.Index
	index.Schema = rpcsample.IndexSchema
	index.PlanSchema = rpcsample.PlanSchema
	index.GeneratedAt = mustTime(t, "2026-10-18T12:00:00Z")
	index.Network = rpcsample.Network{ChainID: 1, Fork: "unspecified"}
	for _, spec := range records {
		record := map[string]any{
			"schema":      rpcsample.RecordSchema,
			"observed_at": "2026-10-18T12:00:00Z",
			"network":     map[string]any{"chain_id": 1, "fork": "unspecified"},
			"source":      map[string]any{"id": spec.SourceID, "endpoint": "[REDACTED]"},
			"request_id":  spec.RequestID,
			"method":      spec.Method,
			"request":     spec.Request,
			"response":    map[string]any{"http_status": 200, "content_type": "application/json", "body": spec.Response},
		}
		encoded, err := json.MarshalIndent(record, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		encoded = append(encoded, '\n')
		name := filepath.Join("records", spec.RequestID+"--"+spec.SourceID+".json")
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), encoded, 0o600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(encoded)
		index.Records = append(index.Records, rpcsample.IndexEntry{
			RequestID: spec.RequestID, Method: spec.Method, SourceID: spec.SourceID,
			ObservedAt: index.GeneratedAt, File: name, SHA256: hex.EncodeToString(sum[:]),
		})
	}
	raw, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.json"), append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	index.Schema = rpcsample.IndexSchema
	return index
}

// fillReferenceSHAs copies the recorded sha256 of every referenced record
// into the context, so a valid fixture needs no hand-computed digest.
func fillReferenceSHAs(contexts map[string]BatchReferenceContext, index rpcsample.Index) map[string]BatchReferenceContext {
	out := map[string]BatchReferenceContext{}
	for key, entry := range contexts {
		for i, ref := range entry.ReferenceRecords {
			entry.ReferenceRecords[i].SHA256 = IndexSHA(index, ref.RequestID, ref.SourceID)
		}
		out[key] = entry
	}
	return out
}

func writeBatchJSON(t *testing.T, dir string, contexts map[string]BatchReferenceContext) {
	t.Helper()
	document := map[string]any{
		"schema_version": batchSchemaV1,
		"batch_id":       "2026-10-18",
		"feature":        "read-blockref",
		"captured_at":    "2026-10-18T12:00:00Z",
		"network":        map[string]any{"chain_id": 1, "fork": "unspecified"},
		"note":           "fixture",
	}
	if contexts != nil {
		document["reference_context"] = contexts
	}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "batch.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeFixtureBatch lays out a complete batch directory and loads it back.
// Every referenced record's sha256 is filled in from the written index.
func writeFixtureBatch(t *testing.T, contexts map[string]BatchReferenceContext, records []fixtureRecordSpec) (string, BatchMeta, rpcsample.Index, []rpcsample.Record) {
	t.Helper()
	dir := t.TempDir()
	index := writeFixtureRecords(t, dir, records)
	writeBatchJSON(t, dir, fillReferenceSHAs(contexts, index))
	meta, err := LoadBatchMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	loaded, recordsLoaded, err := LoadBatch(dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir, meta, loaded, recordsLoaded
}

func fixtureAnchorRecord(sourceID, tag string) fixtureRecordSpec {
	return fixtureRecordSpec{
		RequestID: "anchor-" + tag,
		SourceID:  sourceID,
		Method:    "eth_getBlockByNumber",
		Request:   fmt.Sprintf(`{"jsonrpc":"2.0","id":4,"method":"eth_getBlockByNumber","params":[%q,false]}`, tag),
		Response:  fmt.Sprintf(`{"jsonrpc":"2.0","id":4,"result":{"number":"%s","hash":"0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}`, heightHex(0xe)),
	}
}

func heightHex(height uint64) string {
	return fmt.Sprintf("0x%x", height)
}

func fixtureLogsRecord(sourceID, requestID, request string) fixtureRecordSpec {
	return fixtureRecordSpec{
		RequestID: requestID,
		SourceID:  sourceID,
		Method:    "eth_getLogs",
		Request:   request,
		Response:  `{"jsonrpc":"2.0","id":5,"result":[]}`,
	}
}

var logsLogical = `{"jsonrpc":"2.0","id":5,"method":"eth_getLogs","params":[{"fromBlock":"safe","toBlock":"safe","topics":null}]}`

func referenceContext(tag string) map[string]BatchReferenceContext {
	quoted := fmt.Sprintf("%q", tag)
	return map[string]BatchReferenceContext{
		"logs-safe": {
			LogicalRequest: BatchLogicalRequest{
				Method: "eth_getLogs",
				Params: json.RawMessage(`[{"fromBlock":` + quoted + `,"toBlock":` + quoted + `,"topics":null}]`),
			},
			Tag:           tag,
			EstablishedAt: establishTime(),
			Members:       []string{"s1"},
			ReferenceRecords: []BatchRecordRef{{
				RequestID: "anchor-" + tag,
				SourceID:  "s1",
			}},
			ExpectedReference: BatchExpectedReference{
				Height:  0xe,
				Hash:    "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				Status:  "corroborated",
				Support: []string{"s1"},
			},
		},
	}
}

func TestBatchMetaHistoryCompatibility(t *testing.T) {
	for _, feature := range []string{"revert-data", "block-receipt-tx", "eip1898-object"} {
		t.Run(feature, func(t *testing.T) {
			meta, err := LoadBatchMeta(batchDir(feature))
			if err != nil {
				t.Fatal(err)
			}
			if meta.SchemaVersion != batchSchemaV1 || meta.Feature != feature {
				t.Fatalf("meta = %+v", meta)
			}
			if len(meta.ReferenceContext) != 0 {
				t.Fatalf("historical batch invented reference context: %+v", meta.ReferenceContext)
			}
			// DisallowUnknownFields holds: a file with an unknown field is
			// rejected, so capture format and reader stay in lockstep.
			raw, err := os.ReadFile(filepath.Join(batchDir(feature), "batch.json"))
			if err != nil {
				t.Fatal(err)
			}
			var doc map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			doc["surprise_field"] = "no"
			broken, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "batch.json"), broken, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadBatchMeta(dir); err == nil {
				t.Fatal("unknown field was accepted")
			}
		})
	}
}

func TestBatchReferenceContextRoundTrip(t *testing.T) {
	_, meta, _, _ := writeFixtureBatch(t, referenceContext("safe"), []fixtureRecordSpec{
		fixtureAnchorRecord("s1", "safe"),
		fixtureLogsRecord("s1", "logs-safe", logsLogical),
	})
	entry, ok := meta.ReferenceContext["logs-safe"]
	if !ok {
		t.Fatalf("reference context lost: %+v", meta)
	}
	if entry.Tag != "safe" || entry.LogicalRequest.Method != "eth_getLogs" {
		t.Fatalf("entry = %+v", entry)
	}
	var params []map[string]any
	if err := json.Unmarshal(entry.LogicalRequest.Params, &params); err != nil {
		t.Fatalf("logical params: %v", err)
	}
	if params[0]["fromBlock"] != "safe" {
		t.Fatalf("logical request lost the raw tag: %+v", params)
	}
	if len(entry.Members) != 1 || entry.Members[0] != "s1" {
		t.Fatalf("members = %v", entry.Members)
	}
	if entry.ExpectedReference.Height != 0xe || entry.ExpectedReference.Status != "corroborated" {
		t.Fatalf("expected = %+v", entry.ExpectedReference)
	}
	if len(entry.ReferenceRecords) != 1 || entry.ReferenceRecords[0].RequestID != "anchor-safe" {
		t.Fatalf("reference records = %+v", entry.ReferenceRecords)
	}
}

func TestValidateReferenceContextRejects(t *testing.T) {
	validBase := []fixtureRecordSpec{
		fixtureAnchorRecord("s1", "safe"),
		fixtureLogsRecord("s1", "logs-safe", logsLogical),
	}

	cases := []struct {
		name   string
		mutate func(entry *BatchReferenceContext)
	}{
		{name: "tag latest", mutate: func(entry *BatchReferenceContext) { entry.Tag = "latest" }},
		{name: "empty logical method", mutate: func(entry *BatchReferenceContext) { entry.LogicalRequest.Method = "" }},
		{name: "no members", mutate: func(entry *BatchReferenceContext) { entry.Members = nil }},
		{name: "duplicate members", mutate: func(entry *BatchReferenceContext) { entry.Members = []string{"s1", "s1"} }},
		{name: "member without record", mutate: func(entry *BatchReferenceContext) { entry.Members = []string{"s1", "s9"} }},
		{name: "no reference records", mutate: func(entry *BatchReferenceContext) { entry.ReferenceRecords = nil }},
		{name: "reference sha mismatch", mutate: func(entry *BatchReferenceContext) { entry.ReferenceRecords[0].SHA256 = "0bad" }},
		{name: "missing reference record", mutate: func(entry *BatchReferenceContext) { entry.ReferenceRecords[0].RequestID = "anchor-safe-other" }},
		{name: "reference source not a member", mutate: func(entry *BatchReferenceContext) { entry.ReferenceRecords[0].SourceID = "s9" }},
		{name: "expected height not set", mutate: func(entry *BatchReferenceContext) { entry.ExpectedReference.Height = 0 }},
		{name: "expected hash truncated", mutate: func(entry *BatchReferenceContext) { entry.ExpectedReference.Hash = "0x11" }},
		{name: "expected status empty", mutate: func(entry *BatchReferenceContext) { entry.ExpectedReference.Status = "" }},
		{name: "expected support outside members", mutate: func(entry *BatchReferenceContext) { entry.ExpectedReference.Support = []string{"s9"} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, meta, index, records := writeFixtureBatch(t, referenceContext("safe"), validBase)
			entry := meta.ReferenceContext["logs-safe"]
			tc.mutate(&entry)
			meta.ReferenceContext = map[string]BatchReferenceContext{"logs-safe": entry}
			if err := ValidateReferenceContext(meta, index, records); err == nil {
				t.Fatal("invalid reference context was accepted")
			}
		})
	}

	t.Run("no anchor collapsed to latest", func(t *testing.T) {
		latest := fixtureAnchorRecord("s1", "latest")
		latest.RequestID = "anchor-safe"
		_, meta, index, records := writeFixtureBatch(t, referenceContext("safe"), []fixtureRecordSpec{
			latest,
			fixtureLogsRecord("s1", "logs-safe", logsLogical),
		})
		if err := ValidateReferenceContext(meta, index, records); err == nil {
			t.Fatal("an anchor query collapsed to latest was accepted")
		}
	})
}

func TestBusinessRecordsFilterReferenceInputs(t *testing.T) {
	_, meta, index, records := writeFixtureBatch(t, referenceContext("safe"), []fixtureRecordSpec{
		fixtureAnchorRecord("s1", "safe"),
		fixtureLogsRecord("s1", "logs-safe", logsLogical),
		fixtureLogsRecord("s2", "logs-safe", logsLogical),
	})
	business, err := BusinessRecords(meta, index, records)
	if err != nil {
		t.Fatal(err)
	}
	if len(business) != 2 {
		t.Fatalf("business records = %d, want the two logs records only", len(business))
	}
	if business[0].RequestID != "logs-safe" || business[0].Source.ID != "s1" || business[1].Source.ID != "s2" {
		t.Fatalf("business order lost: %+v", business)
	}
	refs := ReferenceRequestIDs(meta)
	if !refs["anchor-safe"] || len(refs) != 1 {
		t.Fatalf("reference ids = %v", refs)
	}
}

func TestPreparedBusinessRequestRewritesMovingTags(t *testing.T) {
	t.Run("getLogs filter fields", func(t *testing.T) {
		method, params, err := PreparedBusinessRequest(referenceContext("safe")["logs-safe"])
		if err != nil {
			t.Fatal(err)
		}
		if method != "eth_getLogs" {
			t.Fatalf("method = %s", method)
		}
		want := `[{"fromBlock":"0xe","toBlock":"0xe","topics":null}]`
		if string(params) != want {
			t.Fatalf("params = %s, want %s", params, want)
		}
	})
	t.Run("feeHistory position 1", func(t *testing.T) {
		entry := BatchReferenceContext{
			LogicalRequest: BatchLogicalRequest{
				Method: "eth_feeHistory",
				Params: json.RawMessage(`[4,"safe",[]]`),
			},
			Tag:               "safe",
			ExpectedReference: BatchExpectedReference{Height: 0xe},
		}
		method, params, err := PreparedBusinessRequest(entry)
		if err != nil {
			t.Fatal(err)
		}
		if method != "eth_feeHistory" {
			t.Fatalf("method = %s", method)
		}
		if string(params) != `[4,"0xe",[]]` {
			t.Fatalf("params = %s", params)
		}
	})
	t.Run("other tags and shapes pass through", func(t *testing.T) {
		entry := referenceContext("safe")["logs-safe"]
		entry.LogicalRequest.Params = json.RawMessage(`[{"fromBlock":"earliest","toBlock":"finalized"}]`)
		_, params, err := PreparedBusinessRequest(entry)
		if err != nil {
			t.Fatal(err)
		}
		want := `[{"fromBlock":"earliest","toBlock":"finalized"}]`
		if string(params) != want {
			t.Fatalf("params = %s, want %s", params, want)
		}
	})
}

func TestMatchesPreparedRecordComparesMethodAndParams(t *testing.T) {
	entry := referenceContext("safe")["logs-safe"]
	entry.ReferenceRecords[0].SHA256 = "any"

	match := `{"jsonrpc":"2.0","id":41,"method":"eth_getLogs","params":[{"fromBlock":"0xe","toBlock":"0xe","topics":null}]}`
	if err := MatchesPreparedRecord(entry, json.RawMessage(match)); err != nil {
		t.Fatalf("a matching record with a different id was rejected: %v", err)
	}
	wrongMethod := `{"jsonrpc":"2.0","id":41,"method":"eth_getBalance","params":[{"fromBlock":"0xe","toBlock":"0xe","topics":null}]}`
	if err := MatchesPreparedRecord(entry, json.RawMessage(wrongMethod)); err == nil {
		t.Fatal("wrong method was accepted")
	}
	wrongHeight := `{"jsonrpc":"2.0","id":41,"method":"eth_getLogs","params":[{"fromBlock":"0xf","toBlock":"0xe","topics":null}]}`
	if err := MatchesPreparedRecord(entry, json.RawMessage(wrongHeight)); err == nil {
		t.Fatal("mismatched height was accepted")
	}
	bareTag := `{"jsonrpc":"2.0","id":41,"method":"eth_getLogs","params":[{"fromBlock":"safe","toBlock":"safe","topics":null}]}`
	if err := MatchesPreparedRecord(entry, json.RawMessage(bareTag)); err == nil {
		t.Fatal("a bare tag passed as prepared")
	}
}
