// Package rpcsample records reproducible JSON-RPC observations without
// interpreting them as compatibility evidence.
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
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	PlanSchema   = "ethguard.rpc-sample-plan/v1"
	RecordSchema = "ethguard.rpc-sample-record/v1"
	IndexSchema  = "ethguard.rpc-sample-index/v1"
)

var safeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type Plan struct {
	Schema   string    `json:"schema"`
	Network  Network   `json:"network"`
	Sources  []Source  `json:"sources"`
	Requests []Request `json:"requests"`
	// RedactEnv lists additional environment variables whose values must be
	// removed from all persisted request, response, and error text.
	RedactEnv []string `json:"redact_env,omitempty"`
}

type Network struct {
	ChainID uint64 `json:"chain_id"`
	Fork    string `json:"fork"`
}

type Source struct {
	ID          string            `json:"id"`
	Endpoint    string            `json:"endpoint,omitempty"`
	EndpointEnv string            `json:"endpoint_env,omitempty"`
	HeaderEnv   map[string]string `json:"header_env,omitempty"`
}

type Request struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type sourceRecord struct {
	ID       string `json:"id"`
	Endpoint string `json:"endpoint"`
}

type WireRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type WireResponse struct {
	HTTPStatus  int    `json:"http_status,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Body        string `json:"body,omitempty"`
	Truncated   bool   `json:"truncated,omitempty"`
}

type RecordedError struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

type Record struct {
	Schema     string          `json:"schema"`
	ObservedAt time.Time       `json:"observed_at"`
	Network    Network         `json:"network"`
	Source     sourceRecord    `json:"source"`
	RequestID  string          `json:"request_id"`
	Method     string          `json:"method"`
	Request    string          `json:"request"`
	Response   WireResponse    `json:"response"`
	Errors     []RecordedError `json:"errors,omitempty"`
}

type Index struct {
	Schema      string       `json:"schema"`
	PlanSchema  string       `json:"plan_schema"`
	GeneratedAt time.Time    `json:"generated_at"`
	Network     Network      `json:"network"`
	Records     []IndexEntry `json:"records"`
}

type IndexEntry struct {
	RequestID  string    `json:"request_id"`
	Method     string    `json:"method"`
	SourceID   string    `json:"source_id"`
	ObservedAt time.Time `json:"observed_at"`
	File       string    `json:"file"`
	SHA256     string    `json:"sha256"`
	ErrorKinds []string  `json:"error_kinds,omitempty"`
}

type Collector struct {
	Client           *http.Client
	Now              func() time.Time
	MaxResponseBytes int64
}

func LoadPlan(path string) (Plan, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Plan{}, err
	}
	var p Plan
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return Plan{}, fmt.Errorf("decode plan: %w", err)
	}
	if p.Schema != PlanSchema {
		return Plan{}, fmt.Errorf("unsupported plan schema %q", p.Schema)
	}
	if err := validatePlan(p); err != nil {
		return Plan{}, err
	}
	return p, nil
}

func validatePlan(p Plan) error {
	if p.Network.ChainID == 0 || strings.TrimSpace(p.Network.Fork) == "" {
		return errors.New("network.chain_id and network.fork are required")
	}
	seenSources := make(map[string]bool)
	for _, s := range p.Sources {
		if !safeID.MatchString(s.ID) || seenSources[s.ID] {
			return fmt.Errorf("source id %q is invalid or duplicated", s.ID)
		}
		seenSources[s.ID] = true
		if (s.Endpoint == "") == (s.EndpointEnv == "") {
			return fmt.Errorf("source %q must set exactly one of endpoint or endpoint_env", s.ID)
		}
	}
	seenRequests := make(map[string]bool)
	for _, r := range p.Requests {
		if !safeID.MatchString(r.ID) || seenRequests[r.ID] {
			return fmt.Errorf("request id %q is invalid or duplicated", r.ID)
		}
		seenRequests[r.ID] = true
		if strings.TrimSpace(r.Method) == "" || !json.Valid(r.Params) {
			return fmt.Errorf("request %q must have a method and valid JSON params", r.ID)
		}
	}
	if len(p.Sources) == 0 || len(p.Requests) == 0 {
		return errors.New("at least one source and request are required")
	}
	return nil
}

func (c Collector) Collect(ctx context.Context, p Plan, outDir string) (Index, error) {
	if err := validatePlan(p); err != nil {
		return Index{}, err
	}
	if c.Client == nil {
		c.Client = http.DefaultClient
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if err := os.MkdirAll(filepath.Join(outDir, "records"), 0o700); err != nil {
		return Index{}, err
	}
	redactor, err := newRedactor(p)
	if err != nil {
		return Index{}, err
	}
	idx := Index{Schema: IndexSchema, PlanSchema: p.Schema, GeneratedAt: c.Now().UTC(), Network: p.Network}
	for requestNumber, spec := range p.Requests {
		for _, source := range p.Sources {
			record := c.collectOne(ctx, p.Network, source, spec, requestNumber+1, redactor)
			rel := filepath.ToSlash(filepath.Join("records", spec.ID+"--"+source.ID+".json"))
			encoded, err := json.MarshalIndent(record, "", "  ")
			if err != nil {
				return Index{}, err
			}
			encoded = append(encoded, '\n')
			if err := writeFile(filepath.Join(outDir, filepath.FromSlash(rel)), encoded); err != nil {
				return Index{}, err
			}
			sum := sha256.Sum256(encoded)
			entry := IndexEntry{RequestID: spec.ID, Method: spec.Method, SourceID: source.ID,
				ObservedAt: record.ObservedAt, File: rel, SHA256: hex.EncodeToString(sum[:])}
			for _, recordedErr := range record.Errors {
				entry.ErrorKinds = append(entry.ErrorKinds, recordedErr.Kind)
			}
			idx.Records = append(idx.Records, entry)
		}
	}
	encoded, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return Index{}, err
	}
	if err := writeFile(filepath.Join(outDir, "index.json"), append(encoded, '\n')); err != nil {
		return Index{}, err
	}
	return idx, nil
}

func (c Collector) collectOne(ctx context.Context, network Network, source Source, spec Request, wireID int, redact *redactor) Record {
	observed := c.Now().UTC()
	endpoint, headers, endpointErr := resolveSource(source)
	wire := WireRequest{JSONRPC: "2.0", ID: wireID, Method: spec.Method, Params: spec.Params}
	requestBytes, _ := json.Marshal(wire)
	record := Record{Schema: RecordSchema, ObservedAt: observed, Network: network,
		Source: sourceRecord{ID: source.ID, Endpoint: sanitizedEndpoint(endpoint)}, RequestID: spec.ID,
		Method: spec.Method, Request: redact.text(string(requestBytes))}
	if endpointErr != nil {
		record.Errors = append(record.Errors, RecordedError{Kind: "configuration", Message: redact.text(endpointErr.Error())})
		return record
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(requestBytes))
	if err != nil {
		record.Errors = append(record.Errors, RecordedError{Kind: "configuration", Message: redact.text(err.Error())})
		return record
	}
	req.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		record.Errors = append(record.Errors, RecordedError{Kind: "transport", Message: redact.text(err.Error())})
		return record
	}
	defer resp.Body.Close()
	record.Response.HTTPStatus = resp.StatusCode
	record.Response.ContentType = redact.text(resp.Header.Get("Content-Type"))
	body, truncated, readErr := readBody(resp.Body, c.MaxResponseBytes)
	record.Response.Body = redact.text(string(body))
	record.Response.Truncated = truncated
	if readErr != nil {
		record.Errors = append(record.Errors, RecordedError{Kind: "transport", Message: redact.text(readErr.Error())})
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		record.Errors = append(record.Errors, RecordedError{Kind: "http", Message: redact.text(resp.Status)})
	}
	if truncated {
		record.Errors = append(record.Errors, RecordedError{Kind: "truncated", Message: "response exceeded configured byte limit"})
	} else {
		classifyJSONRPC(body, &record)
	}
	return record
}

func readBody(r io.Reader, max int64) ([]byte, bool, error) {
	if max <= 0 {
		b, err := io.ReadAll(r)
		return b, false, err
	}
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if int64(len(b)) > max {
		return b[:max], true, err
	}
	return b, false, err
}

func classifyJSONRPC(body []byte, record *Record) {
	var envelope struct {
		JSONRPC string          `json:"jsonrpc"`
		Error   json.RawMessage `json:"error"`
	}
	if len(body) == 0 || json.Unmarshal(body, &envelope) != nil {
		record.Errors = append(record.Errors, RecordedError{Kind: "malformed_json", Message: "response is not a JSON object"})
		return
	}
	if envelope.JSONRPC != "2.0" {
		record.Errors = append(record.Errors, RecordedError{Kind: "protocol", Message: "missing or unsupported jsonrpc version"})
	}
	if len(envelope.Error) > 0 && string(envelope.Error) != "null" {
		record.Errors = append(record.Errors, RecordedError{Kind: "jsonrpc", Message: "response contains a JSON-RPC error"})
	}
}

func resolveSource(source Source) (string, map[string]string, error) {
	endpoint := source.Endpoint
	if source.EndpointEnv != "" {
		endpoint = os.Getenv(source.EndpointEnv)
		if endpoint == "" {
			return "", nil, fmt.Errorf("environment variable %s is empty", source.EndpointEnv)
		}
	}
	if u, err := url.Parse(endpoint); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return endpoint, nil, errors.New("endpoint must be an absolute http(s) URL")
	}
	headers := make(map[string]string, len(source.HeaderEnv))
	for name, envName := range source.HeaderEnv {
		value := os.Getenv(envName)
		if value == "" {
			return endpoint, nil, fmt.Errorf("environment variable %s is empty", envName)
		}
		headers[name] = value
	}
	return endpoint, headers, nil
}

func sanitizedEndpoint(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "[invalid endpoint]"
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host}).String()
}

type redactor struct{ secrets []string }

func newRedactor(p Plan) (*redactor, error) {
	var secrets []string
	for _, name := range p.RedactEnv {
		if value := os.Getenv(name); value != "" {
			secrets = append(secrets, value)
		}
	}
	for _, source := range p.Sources {
		endpoint := source.Endpoint
		if source.EndpointEnv != "" {
			endpoint = os.Getenv(source.EndpointEnv)
		}
		if endpoint != "" {
			secrets = append(secrets, endpoint)
			if u, err := url.Parse(endpoint); err == nil {
				if u.User != nil {
					secrets = append(secrets, u.User.Username())
					if password, ok := u.User.Password(); ok {
						secrets = append(secrets, password)
					}
				}
				for _, values := range u.Query() {
					secrets = append(secrets, values...)
				}
				for _, segment := range strings.Split(strings.Trim(u.Path, "/"), "/") {
					if len(segment) >= 8 {
						secrets = append(secrets, segment)
					}
				}
			}
		}
		for _, envName := range source.HeaderEnv {
			if value := os.Getenv(envName); value != "" {
				secrets = append(secrets, value)
			}
		}
	}
	seen := make(map[string]bool)
	filtered := secrets[:0]
	for _, secret := range secrets {
		if secret != "" && !seen[secret] {
			seen[secret] = true
			filtered = append(filtered, secret)
		}
	}
	sort.Slice(filtered, func(i, j int) bool { return len(filtered[i]) > len(filtered[j]) })
	return &redactor{secrets: filtered}, nil
}

func (r *redactor) text(s string) string {
	for _, secret := range r.secrets {
		s = strings.ReplaceAll(s, secret, "[REDACTED]")
	}
	return s
}

func writeFile(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
