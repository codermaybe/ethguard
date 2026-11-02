package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/codermaybe/ethguard/internal/transport/wss"
)

const captureSchema = "ethguard.wss-provider-evidence/v1"

const (
	usdcTransferMaxData = "0xa9059cbb" +
		"000000000000000000000000000000000000000000000000000000000000dead" +
		"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	deadAddress = "0x000000000000000000000000000000000000dEaD"
	usdcAddress = "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"
	wethAddress = "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2"
	// historicalBlockHash is mainnet block 18,000,000. The block-hash-position
	// probe records how a provider rejects or accepts an EIP-1898 object where
	// eth_getBlockByNumber expects a block parameter.
	historicalBlockHash = "0x95b198e154acbfc64109dfd22d8224fe927fd8dfdedfae01587674482ba4baf3"
)

type scenario struct {
	Name   string
	Method string
	Params []any
}

var scenarios = []scenario{
	{Name: "chain-id", Method: "eth_chainId", Params: []any{}},
	{Name: "unknown-method", Method: "eth_unknownMethodProbe", Params: []any{}},
	{Name: "revert-with-reason", Method: "eth_call", Params: []any{
		map[string]string{"from": deadAddress, "to": usdcAddress, "data": usdcTransferMaxData},
		"latest",
	}},
	{Name: "revert-without-reason", Method: "eth_call", Params: []any{
		map[string]string{"from": deadAddress, "to": wethAddress, "data": usdcTransferMaxData},
		"latest",
	}},
	{Name: "invalid-params", Method: "eth_getBalance", Params: []any{"not-an-address", "latest"}},
	{Name: "block-hash-position", Method: "eth_getBlockByNumber", Params: []any{
		map[string]any{"blockHash": historicalBlockHash, "requireCanonical": false}, false,
	}},
}

type captureConfig struct {
	Timeout       time.Duration
	Pause         time.Duration
	ChainID       uint64
	ProviderClass string
	RedactEnv     []string
}

type toolInfo struct {
	Name      string `json:"name"`
	Commit    string `json:"commit,omitempty"`
	GoVersion string `json:"go_version"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	WebSocket string `json:"websocket_module,omitempty"`
}

type captureIndex struct {
	Schema        string         `json:"schema"`
	ObservedAt    time.Time      `json:"observed_at"`
	ChainID       uint64         `json:"chain_id"`
	ProviderClass string         `json:"provider_class"`
	EvidenceLevel string         `json:"evidence_level"`
	Tool          toolInfo       `json:"tool"`
	Records       []indexEntry   `json:"records"`
	NotExercised  []notExercised `json:"not_exercised"`
}

type indexEntry struct {
	Provider   string `json:"provider"`
	Endpoint   string `json:"endpoint"`
	Scenario   string `json:"scenario"`
	Method     string `json:"method"`
	RequestID  int    `json:"request_id"`
	DataState  string `json:"data_state"`
	LatencyMS  int64  `json:"latency_ms"`
	CloseCode  int    `json:"close_code,omitempty"`
	File       string `json:"file"`
	SHA256     string `json:"sha256"`
	ResultSeen bool   `json:"result_seen,omitempty"`
}

type notExercised struct {
	Scenario string `json:"scenario"`
	Reason   string `json:"reason"`
}

type followUp struct {
	Method    string `json:"method"`
	OK        bool   `json:"ok"`
	Result    string `json:"result,omitempty"`
	Error     string `json:"error,omitempty"`
	LatencyMS int64  `json:"latency_ms"`
	CloseCode int    `json:"close_code,omitempty"`
	CloseText string `json:"close_text,omitempty"`
}

type captureRecord struct {
	Schema        string    `json:"schema"`
	ObservedAt    time.Time `json:"observed_at"`
	Provider      string    `json:"provider"`
	ProviderClass string    `json:"provider_class"`
	Endpoint      string    `json:"endpoint"`
	Scenario      string    `json:"scenario"`
	Method        string    `json:"method"`
	RequestID     int       `json:"request_id"`
	Request       string    `json:"request"`
	ResponseFrame string    `json:"response_frame,omitempty"`
	ResponseBytes int       `json:"response_bytes,omitempty"`
	Result        string    `json:"result,omitempty"`
	DataState     string    `json:"data_state"`
	DataPresent   bool      `json:"data_present"`
	DataRaw       string    `json:"data_raw,omitempty"`
	LatencyMS     int64     `json:"latency_ms"`
	HTTPStatus    int       `json:"http_status,omitempty"`
	ReadError     string    `json:"read_error,omitempty"`
	CloseCode     int       `json:"close_code,omitempty"`
	CloseText     string    `json:"close_text,omitempty"`
	FollowUp      *followUp `json:"follow_up,omitempty"`
	EvidenceLevel string    `json:"evidence_level"`
	Note          string    `json:"note"`
}

type wireRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type redactor struct {
	secrets []string
	scan    []string
}

func capture(ctx context.Context, urls []string, outDir string, cfg captureConfig) error {
	if err := os.MkdirAll(filepath.Join(outDir, "records"), 0o700); err != nil {
		return err
	}
	redact := newRedactor(urls, cfg.RedactEnv)
	index := captureIndex{
		Schema:        captureSchema,
		ObservedAt:    time.Now().UTC(),
		ChainID:       cfg.ChainID,
		ProviderClass: cfg.ProviderClass,
		EvidenceLevel: "live-observation",
		Tool:          readToolInfo(),
		NotExercised:  notExercisedScenarios(),
	}
	for i, rawURL := range urls {
		label := fmt.Sprintf("p%02d", i+1)
		endpoint := sanitizeEndpoint(rawURL)
		for j, sc := range scenarios {
			record := probeScenario(ctx, rawURL, label, endpoint, sc, cfg, redact)
			encoded, err := json.MarshalIndent(record, "", "  ")
			if err != nil {
				return err
			}
			encoded = append(encoded, '\n')
			rel := filepath.ToSlash(filepath.Join("records", record.Scenario+"--"+label+".json"))
			if err := writeFile(filepath.Join(outDir, filepath.FromSlash(rel)), encoded); err != nil {
				return err
			}
			sum := sha256.Sum256(encoded)
			index.Records = append(index.Records, indexEntry{
				Provider:   record.Provider,
				Endpoint:   record.Endpoint,
				Scenario:   record.Scenario,
				Method:     record.Method,
				RequestID:  record.RequestID,
				DataState:  record.DataState,
				LatencyMS:  record.LatencyMS,
				CloseCode:  record.CloseCode,
				File:       rel,
				SHA256:     hex.EncodeToString(sum[:]),
				ResultSeen: record.Result != "",
			})
			if cfg.Pause > 0 && j < len(scenarios)-1 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(cfg.Pause):
				}
			}
		}
	}
	encoded, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFile(filepath.Join(outDir, "index.json"), append(encoded, '\n')); err != nil {
		return err
	}
	if err := ensureNoSecrets(outDir, redact.scan); err != nil {
		return err
	}
	fmt.Printf("captured %d providers x %d scenarios in %s; evidence is a live observation, not a compatibility conclusion\n",
		len(urls), len(scenarios), outDir)
	fmt.Println("provider\tendpoint\tscenario\tstate\tclose")
	for _, entry := range index.Records {
		fmt.Printf("%s\t%s\t%s\t%s\t%d\n", entry.Provider, redactedEndpointFor(index, entry.Provider), entry.Scenario, entry.DataState, entry.CloseCode)
	}
	return nil
}

func probeScenario(ctx context.Context, rawURL, label, endpoint string, sc scenario, cfg captureConfig, redact *redactor) captureRecord {
	record := captureRecord{
		Schema:        captureSchema,
		ObservedAt:    time.Now().UTC(),
		Provider:      label,
		ProviderClass: cfg.ProviderClass,
		Endpoint:      endpoint,
		Scenario:      sc.Name,
		Method:        sc.Method,
		RequestID:     1,
		EvidenceLevel: "live-observation",
		Note:          "single read-only JSON-RPC request on a fresh connection; no retry",
	}
	request, err := json.Marshal(wireRequest{JSONRPC: "2.0", ID: record.RequestID, Method: sc.Method, Params: sc.Params})
	if err != nil {
		record.DataState = "request-error"
		record.ReadError = redact.text(err.Error())
		return record
	}
	record.Request = redact.text(string(request))

	dialStart := time.Now()
	conn, err := wss.DialRaw(ctx, rawURL, cfg.Timeout, 0)
	if err != nil {
		record.DataState = "dial-error"
		record.LatencyMS = time.Since(dialStart).Milliseconds()
		record.ReadError = redact.text(err.Error())
		var handshakeErr *wss.HandshakeError
		if errors.As(err, &handshakeErr) && handshakeErr.StatusCode != 0 {
			record.HTTPStatus = handshakeErr.StatusCode
		}
		return record
	}
	defer conn.Close()

	writeCtx, cancelWrite := context.WithTimeout(ctx, cfg.Timeout)
	if err := conn.WriteMessage(writeCtx, request); err != nil {
		cancelWrite()
		record.DataState = "write-error"
		record.ReadError = redact.text(err.Error())
		record.CloseCode, record.CloseText = closeInfo(err)
		return record
	}
	cancelWrite()

	readStart := time.Now()
	_ = conn.SetReadDeadline(time.Now().Add(cfg.Timeout))
	frame, err := conn.ReadMessage()
	record.LatencyMS = time.Since(readStart).Milliseconds()
	if err != nil {
		record.DataState = "read-error"
		record.ReadError = redact.text(err.Error())
		record.CloseCode, record.CloseText = closeInfo(err)
		return record
	}
	record.ResponseFrame = redact.text(string(frame))
	record.ResponseBytes = len(frame)
	record.DataState, record.DataPresent, record.DataRaw, record.Result = classifyResponse(frame, record.RequestID, redact)
	record.FollowUp = probeFollowUp(ctx, conn, cfg, redact)
	return record
}

func probeFollowUp(ctx context.Context, conn *wss.Conn, cfg captureConfig, redact *redactor) *followUp {
	const followUpID = 2
	request, _ := json.Marshal(wireRequest{JSONRPC: "2.0", ID: followUpID, Method: "eth_chainId", Params: []any{}})
	result := &followUp{Method: "eth_chainId"}
	writeCtx, cancelWrite := context.WithTimeout(ctx, cfg.Timeout)
	defer cancelWrite()
	if err := conn.WriteMessage(writeCtx, request); err != nil {
		result.Error = redact.text(err.Error())
		result.CloseCode, result.CloseText = closeInfo(err)
		return result
	}
	start := time.Now()
	_ = conn.SetReadDeadline(time.Now().Add(cfg.Timeout))
	frame, err := conn.ReadMessage()
	result.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		result.Error = redact.text(err.Error())
		result.CloseCode, result.CloseText = closeInfo(err)
		return result
	}
	var envelope struct {
		ID     json.RawMessage `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(frame, &envelope); err != nil {
		result.Error = redact.text(err.Error())
		return result
	}
	if string(envelope.ID) == "2" && len(envelope.Result) > 0 && (len(envelope.Error) == 0 || string(envelope.Error) == "null") {
		result.OK = true
		result.Result = redact.text(string(envelope.Result))
		return result
	}
	result.Error = redact.text(string(frame))
	return result
}

func classifyResponse(frame []byte, requestID int, redact *redactor) (state string, present bool, dataRaw, result string) {
	var envelope struct {
		ID     json.RawMessage `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(frame, &envelope); err != nil {
		return "malformed", false, "", ""
	}
	if string(envelope.ID) != fmt.Sprintf("%d", requestID) {
		return "id-mismatch", false, "", ""
	}
	if len(envelope.Error) > 0 && string(envelope.Error) != "null" {
		state, present, dataRaw = classifyErrorData(envelope.Error)
		if redact != nil {
			dataRaw = redact.text(dataRaw)
		}
		return state, present, dataRaw, ""
	}
	if envelope.Result == nil {
		return "malformed", false, "", ""
	}
	if redact != nil {
		result = redact.text(string(envelope.Result))
	} else {
		result = string(envelope.Result)
	}
	return "no-error", false, "", result
}

func classifyErrorData(raw json.RawMessage) (state string, present bool, dataRaw string) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return "malformed", false, ""
	}
	data, ok := fields["data"]
	if !ok {
		return "missing", false, ""
	}
	trimmed := strings.TrimSpace(string(data))
	switch trimmed {
	case "null":
		return "null", true, trimmed
	case `"0x"`:
		return "empty-hex", true, trimmed
	default:
		return "value", true, trimmed
	}
}

func closeInfo(err error) (int, string) {
	var closeErr wss.CloseError
	if errors.As(err, &closeErr) {
		return closeErr.Code, closeErr.Reason
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		// 1006 is a synthesized state, never a wire value: the connection
		// ended without a close frame.
		return 1006, "abnormal closure (connection ended without a close frame)"
	}
	return 0, ""
}

func sanitizeEndpoint(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "[invalid endpoint]"
	}
	return (&url.URL{Scheme: parsed.Scheme, Host: parsed.Host}).String()
}

func newRedactor(urls []string, envNames []string) *redactor {
	var secrets []string
	var scan []string
	add := func(value string, scanToo bool) {
		if value == "" {
			return
		}
		secrets = append(secrets, value)
		if scanToo {
			scan = append(scan, value)
		}
	}
	for _, raw := range urls {
		parsed, err := url.Parse(raw)
		if err != nil {
			continue
		}
		if urlHasSecret(parsed) {
			add(raw, true)
		}
		if parsed.User != nil {
			add(parsed.User.Username(), true)
			if password, ok := parsed.User.Password(); ok {
				add(password, true)
			}
		}
		for _, values := range parsed.Query() {
			for _, value := range values {
				add(value, true)
			}
		}
		for _, segment := range strings.Split(strings.Trim(parsed.Path, "/"), "/") {
			if len(segment) >= 8 {
				add(segment, true)
			}
		}
	}
	for _, name := range envNames {
		add(os.Getenv(name), true)
	}
	return &redactor{secrets: dedupeLongestFirst(secrets), scan: dedupeLongestFirst(scan)}
}

func urlHasSecret(parsed *url.URL) bool {
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return true
	}
	return parsed.Path != "" && parsed.Path != "/"
}

func dedupeLongestFirst(values []string) []string {
	seen := make(map[string]bool)
	out := values[:0]
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

func (r *redactor) text(value string) string {
	for _, secret := range r.secrets {
		value = strings.ReplaceAll(value, secret, "[REDACTED]")
	}
	return value
}

func ensureNoSecrets(dir string, secrets []string) error {
	return filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(body)
		for _, secret := range secrets {
			if len(secret) >= 4 && strings.Contains(text, secret) {
				return fmt.Errorf("capture output %s still contains a redacted secret", filepath.Base(path))
			}
		}
		return nil
	})
}

func writeFile(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readToolInfo() toolInfo {
	info := toolInfo{
		Name:      "wsscapture",
		GoVersion: runtime.Version(),
		GOOS:      runtime.GOOS,
		GOARCH:    runtime.GOARCH,
	}
	build, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}
	for _, setting := range build.Settings {
		if setting.Key == "vcs.revision" {
			info.Commit = setting.Value
		}
	}
	for _, dep := range build.Deps {
		if dep.Path == "github.com/gorilla/websocket" {
			info.WebSocket = dep.Version
		}
	}
	return info
}

func notExercisedScenarios() []notExercised {
	return []notExercised{
		{Scenario: "rate-limit", Reason: "recorded only if a handshake returns HTTP 429; this probe sends no load beyond one call per scenario"},
		{Scenario: "read-limit", Reason: "would require a provider frame above the adapter bound; not induced against a public endpoint"},
		{Scenario: "response-timeout", Reason: "would require a stalled provider; not induced"},
		{Scenario: "late-response", Reason: "would require a provider answer after cancellation; not induced"},
		{Scenario: "auto-reconnect", Reason: "this capture client and the WSS adapter do not reconnect by design; provider-side reconnect was not observed"},
		{Scenario: "null-data", Reason: "recorded only if a provider actually returns error.data: null"},
	}
}

func redactedEndpointFor(index captureIndex, provider string) string {
	for _, record := range index.Records {
		if record.Provider == provider {
			return record.Endpoint
		}
	}
	return ""
}
