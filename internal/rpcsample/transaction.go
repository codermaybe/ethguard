package rpcsample

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/codermaybe/ethguard/internal/spec"
	"github.com/codermaybe/ethguard/internal/spec/conformance"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/projection"
)

type TransactionTarget struct {
	Source          string `json:"source"`
	Height          string `json:"height"`
	BlockHash       string `json:"block_hash"`
	TransactionHash string `json:"transaction_hash"`
	Index           string `json:"index"`
}

type TransactionReport struct {
	Schema        string             `json:"schema"`
	Compatibility string             `json:"compatibility"`
	Findings      []string           `json:"findings"`
	Observations  []TransactionShape `json:"observations"`
}

type TransactionShape struct {
	Request             string                     `json:"request"`
	Source              string                     `json:"source"`
	ShapeError          string                     `json:"shape_error,omitempty"`
	Official            map[string]json.RawMessage `json:"official"`
	External            map[string]json.RawMessage `json:"external"`
	OfficialDifferences []string                   `json:"official_differences,omitempty"`
	ExternalDifferences []string                   `json:"external_differences,omitempty"`
}

// CollectTransaction records discovery before freezing a target. Failures remain
// in the bundle, including a run in which no discovery response is usable.
func (c Collector) CollectTransaction(ctx context.Context, p Plan, out string) (Index, error) {
	height, err := validateTransactionPlan(p)
	if err != nil {
		return Index{}, err
	}
	redact, err := newRedactor(p)
	if err != nil {
		return Index{}, err
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Client == nil {
		c.Client = http.DefaultClient
	}
	// Atomically reserve a never-before-used bundle, including empty directories.
	if err := os.Mkdir(out, 0o700); err != nil {
		return Index{}, fmt.Errorf("reserve new scenario directory: %w", err)
	}
	if err := os.Mkdir(filepath.Join(out, "records"), 0o700); err != nil {
		return Index{}, err
	}
	persist := func(name string, value any) error {
		body, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		body = []byte(redact.text(string(append(body, '\n'))))
		if err := ensureBytesContainNoSecrets(name, body, redact.scan); err != nil {
			return err
		}
		return writeFile(filepath.Join(out, name), body)
	}
	if err := persist("plan.json", p); err != nil {
		return Index{}, err
	}
	type frozenSource struct {
		source   Source
		endpoint string
		headers  map[string]string
		err      error
	}
	sources := make([]frozenSource, 0, len(p.Sources))
	for _, source := range p.Sources {
		endpoint, headers, err := resolveSource(source)
		sources = append(sources, frozenSource{source, endpoint, headers, err})
	}
	idx := Index{Schema: IndexSchema, PlanSchema: p.Schema, GeneratedAt: c.Now().UTC(), Network: p.Network}
	var records []Record
	capture := func(request Request) error {
		for _, source := range sources {
			record := c.collectResolved(ctx, p.Network, source.source, request, len(records)+1, redact, c.Now().UTC(), source.endpoint, source.headers, source.err)
			name := filepath.ToSlash(filepath.Join("records", request.ID+"--"+source.source.ID+".json"))
			if err := persist(name, record); err != nil {
				return err
			}
			digest, err := transactionFileDigest(filepath.Join(out, name))
			if err != nil {
				return err
			}
			entry := IndexEntry{RequestID: request.ID, Method: request.Method, SourceID: source.source.ID, ObservedAt: record.ObservedAt, File: name, SHA256: digest}
			for _, recordedError := range record.Errors {
				entry.ErrorKinds = append(entry.ErrorKinds, recordedError.Kind)
			}
			idx.Records = append(idx.Records, entry)
			records = append(records, record)
		}
		return persist("index.json", idx)
	}
	if err := capture(p.Requests[0]); err != nil {
		return idx, err
	}
	for _, request := range transactionCaptureRequests(TransactionTarget{}) {
		if err := capture(request); err != nil {
			return idx, err
		}
	}
	target := chooseTransactionTarget(records, height, p.Network.ChainID)
	requests := transactionCaptureRequests(target)
	// Keep the established batch format readable by replay.LoadBatchMeta.
	blockHeight, _ := strconv.ParseUint(height[2:], 16, 64)
	batch := map[string]any{
		"schema_version":      "ethguard.batch/v1",
		"batch_id":            filepath.Base(out),
		"feature":             "transaction",
		"captured_at":         idx.GeneratedAt.Format(time.RFC3339Nano),
		"network":             p.Network,
		"block_height":        blockHeight,
		"web3_client_version": nil,
		"note":                "Provider observations only; compatibility unassessed. Per-source client versions remain in records.",
	}
	if err := persist("batch.json", batch); err != nil {
		return idx, err
	}
	// Publish frozen requests before capture so interrupted runs are auditable.
	scenario := struct {
		Target   TransactionTarget `json:"target"`
		Requests []Request         `json:"requests"`
	}{target, requests}
	if err := persist("scenario.json", scenario); err != nil {
		return idx, err
	}
	for _, request := range requests[3:] {
		if err := capture(request); err != nil {
			return idx, err
		}
	}
	if err := persist("report.json", transactionReport(records, p.Network, target)); err != nil {
		return idx, err
	}
	provenance, err := transactionProvenance(out, idx.GeneratedAt, c.Now().UTC())
	if err != nil {
		return idx, err
	}
	if err := persist("provenance.json", provenance); err != nil {
		return idx, err
	}
	if err := ensureNoSecrets(out, redact.scan); err != nil {
		return idx, err
	}
	if target.TransactionHash == "" {
		return idx, errors.New("no valid discovery target; failure records and report retained")
	}
	return idx, nil
}

func chooseTransactionTarget(records []Record, height string, chainID uint64) TransactionTarget {
	matchingIdentity := make(map[string]bool)
	for _, record := range records {
		if record.Method != "eth_chainId" {
			continue
		}
		raw, err := recordResult(record)
		matchingIdentity[record.Source.ID] = err == nil && string(raw) == fmt.Sprintf(`"0x%x"`, chainID)
	}
	for _, record := range records {
		if record.Method != "eth_getBlockByNumber" || !matchingIdentity[record.Source.ID] {
			continue
		}
		if target, err := discoverTransaction(record, height); err == nil {
			return target
		}
	}
	return TransactionTarget{Height: height}
}

func validateTransactionPlan(p Plan) (string, error) {
	if p.Schema != PlanSchema {
		return "", errors.New("transaction scenario requires a v1 plan")
	}
	if err := validatePlan(p); err != nil {
		return "", err
	}
	if len(p.Requests) != 1 || p.Requests[0].Method != "eth_getBlockByNumber" {
		return "", errors.New("transaction plan requires exactly one discovery eth_getBlockByNumber request")
	}
	for _, request := range transactionCaptureRequests(TransactionTarget{TransactionHash: "reserved"}) {
		if p.Requests[0].ID == request.ID {
			return "", errors.New("discovery request id collides with a reserved capture id")
		}
	}
	var params []json.RawMessage
	if json.Unmarshal(p.Requests[0].Params, &params) != nil || len(params) != 2 || string(params[1]) != "false" {
		return "", errors.New("discovery requires [concrete historical height, false]")
	}
	var height string
	if json.Unmarshal(params[0], &height) != nil || !canonicalQuantity(height) {
		return "", errors.New("discovery height must be a canonical hex quantity")
	}
	if _, err := strconv.ParseUint(height[2:], 16, 64); err != nil {
		return "", errors.New("discovery height exceeds uint64")
	}
	return height, nil
}

func transactionCaptureRequests(target TransactionTarget) []Request {
	requests := []Request{
		{ID: "identity-chain", Method: "eth_chainId", Params: json.RawMessage(`[]`)},
		{ID: "identity-network", Method: "net_version", Params: json.RawMessage(`[]`)},
		{ID: "client-version", Method: "web3_clientVersion", Params: json.RawMessage(`[]`)},
	}
	if target.TransactionHash == "" {
		return requests
	}
	makeRequest := func(id, method string, args ...any) Request {
		params, _ := json.Marshal(args)
		return Request{ID: id, Method: method, Params: params}
	}
	return append(requests,
		makeRequest("transaction-by-hash", "eth_getTransactionByHash", target.TransactionHash),
		makeRequest("transaction-by-block-index", "eth_getTransactionByBlockHashAndIndex", target.BlockHash, target.Index),
		makeRequest("frozen-block", "eth_getBlockByHash", target.BlockHash, false),
		makeRequest("transaction-receipt", "eth_getTransactionReceipt", target.TransactionHash),
	)
}

func transactionFileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	digest := sha256.New()
	_, copyErr := io.Copy(digest, file)
	closeErr := file.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func transactionProvenance(out string, started, finished time.Time) (map[string]any, error) {
	provenance := map[string]any{
		"schema":                "ethguard.transaction-provenance/v1",
		"tool":                  "rpcsample -scenario transaction",
		"go_version":            runtime.Version(),
		"spec_version":          spec.Version(),
		"started_at":            started,
		"finished_at":           finished,
		"compatibility":         "unassessed",
		"commit":                "unknown",
		"vcs.modified":          "unknown",
		"original_digest_scope": "exact request bytes and response bytes read before redaction; truncated responses cover only retained prefix",
		"vcs_identity_scope":    "HEAD plus modified flag is not a complete source snapshot; executable_sha256 identifies the running binary",
	}
	specSum := sha256.Sum256(spec.MethodsJSON())
	provenance["spec_sha256"] = hex.EncodeToString(specSum[:])
	if info, ok := debug.ReadBuildInfo(); ok {
		provenance["build_main"] = info.Main
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				provenance[setting.Key] = setting.Value
				provenance["commit"] = setting.Value
			case "vcs.modified":
				provenance[setting.Key] = setting.Value
			}
		}
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	for key, path := range map[string]string{
		"plan_sha256":       filepath.Join(out, "plan.json"),
		"executable_sha256": executable,
	} {
		digest, err := transactionFileDigest(path)
		if err != nil {
			return nil, err
		}
		provenance[key] = digest
	}
	return provenance, nil
}

func canonicalQuantity(s string) bool {
	if len(s) < 3 || !strings.HasPrefix(s, "0x") || (len(s) > 3 && s[2] == '0') {
		return false
	}
	for _, c := range s[2:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func hashString(s string) bool {
	if len(s) != 66 || !strings.HasPrefix(s, "0x") {
		return false
	}
	_, err := hex.DecodeString(s[2:])
	return err == nil
}

func recordResult(record Record) (json.RawMessage, error) {
	if len(record.Errors) > 0 || record.Response.Truncated {
		return nil, errors.New("failed observation")
	}
	var envelope struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal([]byte(record.Response.Body), &envelope); err != nil {
		return nil, err
	}
	var request WireRequest
	hasError := len(envelope.Error) > 0 && !bytes.Equal(bytes.TrimSpace(envelope.Error), []byte("null"))
	if json.Unmarshal([]byte(record.Request), &request) != nil || envelope.JSONRPC != "2.0" || string(envelope.ID) != strconv.Itoa(request.ID) || hasError || len(envelope.Result) == 0 {
		return nil, errors.New("invalid JSON-RPC envelope")
	}
	return envelope.Result, nil
}

func discoverTransaction(record Record, height string) (TransactionTarget, error) {
	raw, err := recordResult(record)
	if err != nil {
		return TransactionTarget{}, err
	}
	var block struct {
		Hash         string   `json:"hash"`
		Number       string   `json:"number"`
		Transactions []string `json:"transactions"`
	}
	if json.Unmarshal(raw, &block) != nil || block.Number != height || !hashString(block.Hash) || len(block.Transactions) == 0 || !hashString(block.Transactions[0]) {
		return TransactionTarget{}, errors.New("discovery block must match height and contain a transaction hash")
	}
	return TransactionTarget{Source: record.Source.ID, Height: height, BlockHash: block.Hash, TransactionHash: block.Transactions[0], Index: "0x0"}, nil
}

// Both transaction methods use the same pinned transaction result contract.
func transactionSchemaMethod(method string) string {
	if method == "eth_getTransactionByBlockHashAndIndex" {
		return "eth_getTransactionByHash"
	}
	return method
}

func transactionReport(records []Record, network Network, target TransactionTarget) TransactionReport {
	report := TransactionReport{Schema: "ethguard.transaction-report/v1", Compatibility: "unassessed", Findings: []string{}, Observations: []TransactionShape{}}
	var bundle struct {
		Results map[string]any `json:"results"`
	}
	if err := json.Unmarshal(spec.MethodsJSON(), &bundle); err != nil {
		report.Findings = append(report.Findings, "pinned schema bundle: "+err.Error())
		return report
	}
	fieldSets := make(map[string][]projection.StandardField)
	baselines := make(map[string]TransactionShape)
	for _, record := range records {
		raw, err := recordResult(record)
		if err != nil {
			report.Findings = append(report.Findings, record.RequestID+"/"+record.Source.ID+": "+err.Error())
			continue
		}
		report.Findings = append(report.Findings, transactionIdentityFindings(record, raw, network)...)
		method := transactionSchemaMethod(record.Method)
		schema, covered := bundle.Results[method]
		if !covered {
			continue
		}
		fields, cached := fieldSets[method]
		if !cached {
			fields, err = projection.ExecutionAPIStandardFields(method)
			if err != nil {
				report.Findings = append(report.Findings, err.Error())
			}
			fieldSets[method] = fields
		}
		shape := TransactionShape{Request: record.RequestID, Source: record.Source.ID, Official: map[string]json.RawMessage{}, External: map[string]json.RawMessage{}}
		if err := conformance.ValidateResult(schema, raw); err != nil {
			shape.ShapeError = err.Error()
		}
		// Threshold one recovers one source's structural inventory, not
		// cross-provider corroboration. Original raw bytes remain in records.
		evaluation, err := rpc.AggregateFields([]rpc.Observation{{Source: rpc.SourceRef{ID: record.Source.ID}, Value: raw}}, 1)
		if err != nil {
			report.Findings = append(report.Findings, record.RequestID+"/"+record.Source.ID+": field inventory: "+err.Error())
		} else {
			projected := projection.ProjectStandard(evaluation, fields)
			for _, field := range projected.Core {
				shape.Official[field.Path] = field.Value
			}
			for _, field := range projected.AcceptedExtensions {
				shape.External[field.Path] = field.Value
			}
			// Required is a union across branches; only conformance decides
			// required-field violations, never projected.Missing.
		}
		var object map[string]json.RawMessage
		_ = json.Unmarshal(raw, &object)
		report.Findings = append(report.Findings, transactionReferenceFindings(record, raw, object, target)...)
		// Raw comparison only; no new equivalence or normalization rules.
		if baseline, ok := baselines[method]; ok {
			shape.OfficialDifferences = rawDifferences(baseline.Official, shape.Official)
			shape.ExternalDifferences = rawDifferences(baseline.External, shape.External)
		} else {
			baselines[method] = shape
		}
		report.Observations = append(report.Observations, shape)
	}
	if target.TransactionHash == "" {
		report.Findings = append(report.Findings, "no valid discovery target")
	}
	return report
}

func transactionIdentityFindings(record Record, raw json.RawMessage, network Network) []string {
	if record.Method != "eth_chainId" {
		return nil
	}
	expected := fmt.Sprintf(`"0x%x"`, network.ChainID)
	if string(raw) != expected {
		return []string{record.Source.ID + ": " + record.Method + " identity differs from plan"}
	}
	return nil
}

func transactionReferenceFindings(record Record, raw json.RawMessage, object map[string]json.RawMessage, target TransactionTarget) []string {
	if target.TransactionHash == "" {
		return nil
	}
	var checks map[string]string
	switch record.Method {
	case "eth_getTransactionByHash", "eth_getTransactionByBlockHashAndIndex":
		checks = map[string]string{"hash": target.TransactionHash, "blockHash": target.BlockHash, "blockNumber": target.Height, "transactionIndex": target.Index}
	case "eth_getTransactionReceipt":
		checks = map[string]string{"transactionHash": target.TransactionHash, "blockHash": target.BlockHash, "blockNumber": target.Height, "transactionIndex": target.Index}
	case "eth_getBlockByHash":
		checks = map[string]string{"hash": target.BlockHash, "number": target.Height}
	default:
		return nil
	}
	prefix := record.RequestID + "/" + record.Source.ID + ": "
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return []string{prefix + "frozen object unavailable (null)"}
	}
	keys := make([]string, 0, len(checks))
	for key := range checks {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var findings []string
	for _, key := range keys {
		var actual string
		if json.Unmarshal(object[key], &actual) != nil || actual != checks[key] {
			findings = append(findings, prefix+"frozen "+key+" differs or missing")
		}
	}
	return findings
}

func rawDifferences(a, b map[string]json.RawMessage) []string {
	keys := make(map[string]bool)
	for key := range a {
		keys[key] = true
	}
	for key := range b {
		keys[key] = true
	}
	result := []string{}
	for key := range keys {
		var left, right bytes.Buffer
		_ = json.Compact(&left, a[key])
		_ = json.Compact(&right, b[key])
		if !bytes.Equal(left.Bytes(), right.Bytes()) {
			result = append(result, key)
		}
	}
	sort.Strings(result)
	return result
}
