package replay

// Batch metadata reading. batch.json is EthGuard's own capture metadata
// (ethguard.batch/v1); historical batches lack reference_context, so every
// field here must stay optional exactly as spelled below. The reader keeps
// DisallowUnknownFields: a new batch field means updating this structure in
// the same change, per capture-golden-contract §2.1bis.

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/codermaybe/ethguard/internal/rpcsample"
)

const batchSchemaV1 = "ethguard.batch/v1"

// BatchMeta is the batch.json reader structure. ReferenceContext is absent
// (nil) for every pre-stage-0 batch; a group-3 block-reference case must
// carry one entry per business request id, and a missing or invalid entry is
// a replay failure, not a silent pass.
type BatchMeta struct {
	SchemaVersion     string  `json:"schema_version"`
	BatchID           string  `json:"batch_id"`
	Feature           string  `json:"feature"`
	CapturedAt        string  `json:"captured_at"`
	Network           Network `json:"network"`
	BlockHeight       *uint64 `json:"block_height"`
	Web3ClientVersion *string `json:"web3_client_version"`
	Note              string  `json:"note"`

	ReferenceContext map[string]BatchReferenceContext `json:"reference_context,omitempty"`
}

// Network is the chain the batch was captured against. Capture batching is
// per chain: replay nodes and pools are fixed at chain ID 1, and a
// non-mainnet capture must be its own batch, never merged into one vote.
type Network struct {
	ChainID uint64 `json:"chain_id"`
	Fork    string `json:"fork"`
}

// BatchReferenceContext is the anchor context of one business request,
// indexed by that request's id. Field semantics are frozen in
// capture-golden-contract §2.1bis:
//
//   - LogicalRequest is the anchoring input: the request before the moving
//     tag was rewritten.
//   - Tag is that moving tag (`safe` or `finalized`; `latest` is not an
//     anchor and is never accepted here).
//   - ReferenceRecords are the replay INPUT: the real requests/responses
//     that established the reference, each keyed to a stored record and
//     verified against its recorded sha256. Later confirmation requests stay
//     in this list too.
//   - ExpectedReference is the assertion TARGET: computed offline, never
//     injected into a resolver; the resolver recomputes it from the replayed
//     reference responses.
//   - Members must be able to locate actual reference responses, including
//     failed members.
type BatchReferenceContext struct {
	LogicalRequest    BatchLogicalRequest    `json:"logical_request"`
	Tag               string                 `json:"tag"`
	EstablishedAt     time.Time              `json:"established_at"`
	Members           []string               `json:"members"`
	ReferenceRecords  []BatchRecordRef       `json:"reference_records"`
	ExpectedReference BatchExpectedReference `json:"expected_reference"`
}

// BatchLogicalRequest holds the pre-anchor method and params. There is no
// JSON-RPC id here on purpose: the id is a transport correlation label.
type BatchLogicalRequest struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// BatchRecordRef points at one stored record the offline recovery path must
// replay to re-establish the reference. SHA256 is the record file digest
// recorded by index.json.
type BatchRecordRef struct {
	RequestID string `json:"request_id"`
	SourceID  string `json:"source_id"`
	SHA256    string `json:"sha256"`
}

// BatchExpectedReference is the offline-recomputed assertion target for the
// reference. Status records how the capture's anchor settled ("corroborated"
// is the only supported value today); Support names the members that backed
// that settlement.
type BatchExpectedReference struct {
	Height  uint64   `json:"height"`
	Hash    string   `json:"hash"`
	Status  string   `json:"status"`
	Support []string `json:"support"`
}

// LoadBatchMeta reads and strictly decodes dir/batch.json. Unknown fields are
// rejected: the reader structure and the capture format move together
// (contract §2.1bis, DisallowUnknownFields note).
func LoadBatchMeta(dir string) (BatchMeta, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "batch.json"))
	if err != nil {
		return BatchMeta{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var meta BatchMeta
	if err := dec.Decode(&meta); err != nil {
		return BatchMeta{}, fmt.Errorf("batch.json: %w", err)
	}
	if meta.SchemaVersion != batchSchemaV1 {
		return BatchMeta{}, fmt.Errorf("batch.json schema_version = %s", meta.SchemaVersion)
	}
	return meta, nil
}

// ValidateReferenceContext checks every reference_context entry against the
// stored records. It verifies that:
//
//   - each entry keys an existing business request id,
//   - the tag is a real moving anchor tag (safe or finalized),
//   - members are ordered, unique, and all present in the batch,
//   - every reference record resolves to a stored record whose recorded
//     sha256 matches, and its anchor queries kept the original tag (none
//     collapsed to latest),
//   - expected_reference is populated and its support is a subset of members.
//
// It does NOT verify that the recomputed height matches the recorded
// business request: that is MatchesPreparedRecord, and it runs before any
// resolver executes (contract: expected_reference must not be injected).
func ValidateReferenceContext(meta BatchMeta, index rpcsample.Index, records []rpcsample.Record) error {
	for id, entry := range meta.ReferenceContext {
		if err := validateReferenceEntry(id, entry, index, records); err != nil {
			return err
		}
	}
	return nil
}

func validateReferenceEntry(id string, entry BatchReferenceContext, index rpcsample.Index, records []rpcsample.Record) error {
	if entry.LogicalRequest.Method == "" {
		return fmt.Errorf("reference_context %s: logical_request.method is empty", id)
	}
	if !json.Valid(entry.LogicalRequest.Params) {
		return fmt.Errorf("reference_context %s: logical_request.params is not valid JSON", id)
	}
	if entry.Tag != "safe" && entry.Tag != "finalized" {
		return fmt.Errorf("reference_context %s tag = %s, want safe or finalized", id, entry.Tag)
	}
	if entry.EstablishedAt.IsZero() {
		return fmt.Errorf("reference_context %s: established_at is empty", id)
	}
	if len(entry.Members) == 0 {
		return fmt.Errorf("reference_context %s: no members", id)
	}
	present := map[string]bool{}
	for _, member := range entry.Members {
		if member == "" {
			return fmt.Errorf("reference_context %s member is empty", id)
		}
		if present[member] {
			return fmt.Errorf("reference_context %s: duplicate member %s", id, member)
		}
		present[member] = true
		if !batchHasSource(member, records) {
			return fmt.Errorf("reference_context %s member %s has no stored record", id, member)
		}
	}
	if len(entry.ReferenceRecords) == 0 {
		return fmt.Errorf("reference_context %s: no reference_records", id)
	}
	if entry.ExpectedReference.Height == 0 {
		return fmt.Errorf("reference_context %s: expected height is not set", id)
	}
	if err := validateReferenceHash(id, entry.ExpectedReference.Hash); err != nil {
		return err
	}
	if entry.ExpectedReference.Status == "" {
		return fmt.Errorf("reference_context %s: expected status is empty", id)
	}
	if len(entry.ExpectedReference.Support) == 0 {
		return fmt.Errorf("reference_context %s: expected support is empty", id)
	}
	for _, name := range entry.ExpectedReference.Support {
		if !present[name] {
			return fmt.Errorf("reference_context %s: expected support %s is not a member", id, name)
		}
	}
	keyed := false
	for _, ref := range entry.ReferenceRecords {
		record := sourceRecord(ref.RequestID, ref.SourceID, records)
		if record == nil {
			return fmt.Errorf("reference_context %s: reference record %s/%s has no stored record", id, ref.RequestID, ref.SourceID)
		}
		if !present[ref.SourceID] {
			return fmt.Errorf("reference_context %s: reference record %s source %s is not a member", id, ref.RequestID, ref.SourceID)
		}
		if got := IndexSHA(index, ref.RequestID, ref.SourceID); got == "" || got != ref.SHA256 {
			return fmt.Errorf("reference_context %s: reference record %s/%s sha256 %s does not match the index (%s)",
				id, ref.RequestID, ref.SourceID, ref.SHA256, got)
		}
		tag, found := requestTag(record)
		if found && !strings.EqualFold(tag, entry.Tag) {
			return fmt.Errorf("reference_context %s: reference record %s anchored at %s, not %s", id, ref.RequestID, tag, entry.Tag)
		}
		if found {
			keyed = true
		}
	}
	if !keyed {
		return fmt.Errorf("reference_context %s: no reference record carries a %s anchor query", id, entry.Tag)
	}
	return nil
}

// validateReferenceHash requires a 32-byte 0x hex block hash.
func validateReferenceHash(id, hash string) error {
	if len(hash) != 66 {
		return fmt.Errorf("reference_context %s: expected hash %q is not 64 hex bytes", id, hash)
	}
	if _, err := hex.DecodeString(hash[2:]); err != nil {
		return fmt.Errorf("reference_context %s: expected hash is not hex: %w", id, err)
	}
	return nil
}

// IndexSHA returns the recorded sha256 for one record, or "" when the index
// has no such entry.
func IndexSHA(index rpcsample.Index, requestID, sourceID string) string {
	for _, item := range index.Records {
		if item.RequestID == requestID && item.SourceID == sourceID {
			return item.SHA256
		}
	}
	return ""
}

func sourceRecord(requestID, sourceID string, records []rpcsample.Record) *rpcsample.Record {
	for i := range records {
		record := records[i]
		if record.RequestID == requestID && record.Source.ID == sourceID {
			return &record
		}
	}
	return nil
}

// batchHasSource reports whether any stored record came from sourceID.
func batchHasSource(sourceID string, records []rpcsample.Record) bool {
	for _, record := range records {
		if record.Source.ID == sourceID {
			return true
		}
	}
	return false
}

// requestTag extracts the moving tag from a record's wire request when at
// least one position anchors at a string tag. found is false for hashes and
// numbers, so a hash-anchored reference record never pretends to be tagged.
func requestTag(record *rpcsample.Record) (string, bool) {
	var wire struct {
		Params []json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal([]byte(record.Request), &wire); err != nil {
		return "", false
	}
	for _, param := range wire.Params {
		var tag string
		if json.Unmarshal(param, &tag) == nil {
			if isMovingTag(tag) {
				return strings.ToLower(tag), true
			}
			continue
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(param, &object); err != nil {
			continue
		}
		for _, field := range [...]string{"blockNumber", "blockHash"} {
			var value string
			if json.Unmarshal(object[field], &value) != nil {
				continue
			}
			if isMovingTag(value) {
				return strings.ToLower(value), true
			}
		}
	}
	return "", false
}

func isMovingTag(tag string) bool {
	switch strings.ToLower(tag) {
	case "latest", "safe", "finalized":
		return true
	}
	return false
}

// ReferenceRequestIDs returns every request id named as reference input by
// any reference_context entry. Those records are anchor inputs, never
// business golden cases.
func ReferenceRequestIDs(meta BatchMeta) map[string]bool {
	refs := map[string]bool{}
	for _, entry := range meta.ReferenceContext {
		for _, ref := range entry.ReferenceRecords {
			refs[ref.RequestID] = true
		}
	}
	return refs
}

// BusinessRecords filters reference-input records out of the batch so the
// generic grouper never turns an auxiliary anchor request into a business
// golden case. Without reference_context the records are returned unchanged.
func BusinessRecords(meta BatchMeta, index rpcsample.Index, records []rpcsample.Record) ([]rpcsample.Record, error) {
	if len(meta.ReferenceContext) == 0 {
		return records, nil
	}
	if err := ValidateReferenceContext(meta, index, records); err != nil {
		return nil, err
	}
	refs := ReferenceRequestIDs(meta)
	out := records[:0:0]
	var refCount int
	for _, record := range records {
		if refs[record.RequestID] {
			refCount++
			continue
		}
		out = append(out, record)
	}
	if refCount == 0 {
		return nil, fmt.Errorf("batch declares reference_context but no record is named by it")
	}
	return out, nil
}

// PreparedBusinessRequest rebuilds the prepared (anchored) wire request the
// capture must have produced for one reference_context entry: the logical
// params with the entry's moving tag rewritten to the expected reference
// height, spelled as a hex quantity (contract §2.2). It is the comparison
// oracle for the recorded request; nothing here feeds a resolver.
func PreparedBusinessRequest(entry BatchReferenceContext) (method string, params json.RawMessage, err error) {
	if entry.LogicalRequest.Method == "" || !json.Valid(entry.LogicalRequest.Params) {
		return "", nil, fmt.Errorf("reference_context logical request is incomplete")
	}
	var logical any
	if err := json.Unmarshal(entry.LogicalRequest.Params, &logical); err != nil {
		return "", nil, fmt.Errorf("reference_context logical params: %w", err)
	}
	rewritten := rewriteMovingTags(logical, entry.Tag, entry.ExpectedReference.Height)
	raw, err := json.Marshal(rewritten)
	if err != nil {
		return "", nil, fmt.Errorf("reference_context prepared params: %w", err)
	}
	return entry.LogicalRequest.Method, json.RawMessage(raw), nil
}

// rewriteMovingTags replaces every string value equal (case-insensitive) to
// tag with the hex spelling of height, whether as a whole param (feeHistory's
// number at position 1) or as filter-object fields (getLogs' fromBlock,
// toBlock). Other tags, hashes, numbers, and booleans pass through.
func rewriteMovingTags(value any, tag string, height uint64) any {
	rewritten := "0x" + strconv.FormatUint(height, 16)
	switch concrete := value.(type) {
	case string:
		if strings.EqualFold(concrete, tag) {
			return rewritten
		}
		return concrete
	case map[string]any:
		out := make(map[string]any, len(concrete))
		for key, item := range concrete {
			out[key] = rewriteMovingTags(item, tag, height)
		}
		return out
	case []any:
		out := make([]any, len(concrete))
		for i, item := range concrete {
			out[i] = rewriteMovingTags(item, tag, height)
		}
		return out
	default:
		return value
	}
}

// MatchesPreparedRecord asserts that one stored wire request equals the
// prepared request built from the reference context. Comparison covers the
// method and the params; the JSON-RPC id is a transport correlation label
// and is deliberately not compared (contract §2.1bis).
func MatchesPreparedRecord(entry BatchReferenceContext, requestRaw json.RawMessage) error {
	method, params, err := PreparedBusinessRequest(entry)
	if err != nil {
		return err
	}
	var wire struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(requestRaw, &wire); err != nil {
		return fmt.Errorf("recorded request: %w", err)
	}
	if wire.Method != method {
		return fmt.Errorf("recorded method %s, context method %s", wire.Method, method)
	}
	if len(wire.Params) == 0 {
		return fmt.Errorf("recorded request has no params")
	}
	var recorded any
	if err := json.Unmarshal(wire.Params, &recorded); err != nil {
		return fmt.Errorf("recorded params: %w", err)
	}
	recordedRaw, err := json.Marshal(recorded)
	if err != nil {
		return fmt.Errorf("recorded params: %w", err)
	}
	if !bytes.Equal(recordedRaw, params) {
		return fmt.Errorf("recorded params %s, prepared from context %s", recordedRaw, params)
	}
	return nil
}
