package rpcsample

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/internal/limits"
	"time"
)

func TestCollectWritesIndexedRedactedWireRecords(t *testing.T) {
	const secret = "super-secret-token"
	if err := os.Setenv("RPC_SAMPLE_SECRET", secret); err != nil {
		t.Fatal(err)
	}
	defer os.Unsetenv("RPC_SAMPLE_SECRET")
	if err := os.Setenv("RPC_SAMPLE_AUTH", "Bearer "+secret); err != nil {
		t.Fatal(err)
	}
	defer os.Unsetenv("RPC_SAMPLE_AUTH")
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+secret {
			t.Errorf("authorization = %q", got)
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":1,"result":{"note":"` + secret + `"}}`)), Request: r}, nil
	})
	plan := Plan{Schema: PlanSchema, Network: Network{ChainID: 1, Fork: "shanghai"}, Sources: []Source{{ID: "main", Endpoint: "https://rpc.example/v3/" + secret, HeaderEnv: map[string]string{"Authorization": "RPC_SAMPLE_AUTH"}}}, Requests: []Request{{ID: "head", Method: "eth_blockNumber", Params: json.RawMessage("[]")}}, RedactEnv: []string{"RPC_SAMPLE_SECRET"}}
	out := t.TempDir()
	c := Collector{Client: &http.Client{Transport: transport}, Now: func() time.Time { return time.Unix(10, 0).UTC() }}
	idx, err := c.Collect(context.Background(), plan, out)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Records) != 1 || idx.Records[0].ErrorKinds != nil {
		t.Fatalf("index = %+v", idx)
	}
	b, err := os.ReadFile(filepath.Join(out, "records", "head--main.json"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if strings.Contains(text, secret) {
		t.Fatalf("secret persisted: %s", text)
	}
	if !strings.Contains(text, `"endpoint":`) || !strings.Contains(text, `"body":`) || !strings.Contains(text, "[REDACTED]") {
		t.Fatalf("missing endpoint/body/redaction: %s", text)
	}
}

func TestCollectRedactsCredentialsFromErrorsAndMetadata(t *testing.T) {
	const secret = "x7"
	if err := os.Setenv("RPC_SAMPLE_SHORT_SECRET", secret); err != nil {
		t.Fatal(err)
	}
	defer os.Unsetenv("RPC_SAMPLE_SHORT_SECRET")
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Status:     "502 " + secret,
			Header:     http.Header{"Content-Type": []string{"application/" + secret}},
			Body:       io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":1,"error":{"message":"` + secret + `"}}`)),
			Request:    r,
		}, nil
	})
	plan := Plan{
		Schema: PlanSchema, Network: Network{ChainID: 1, Fork: "x"},
		Sources: []Source{{ID: "s", Endpoint: "https://rpc.example/path/" + secret + "?key=" + secret,
			HeaderEnv: map[string]string{"Authorization": "RPC_SAMPLE_SHORT_SECRET"}}},
		Requests:  []Request{{ID: "x", Method: "eth_chainId", Params: json.RawMessage("[]")}},
		RedactEnv: []string{"RPC_SAMPLE_SHORT_SECRET"},
	}
	out := t.TempDir()
	if _, err := (Collector{Client: &http.Client{Transport: transport}}).Collect(context.Background(), plan, out); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(out, "records", "x--s.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), secret) {
		t.Fatalf("short credential persisted: %s", b)
	}
}

func TestCollectAllowsShortEndpointQueryValues(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`)),
			Request:    r,
		}, nil
	})
	plan := Plan{
		Schema:  PlanSchema,
		Network: Network{ChainID: 1, Fork: "x"},
		Sources: []Source{{ID: "s", Endpoint: "https://rpc.example?key=x7"}},
		Requests: []Request{{
			ID:     "chain",
			Method: "eth_chainId",
			Params: json.RawMessage("[]"),
		}},
	}
	idx, err := (Collector{Client: &http.Client{Transport: transport}}).Collect(context.Background(), plan, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Records) != 1 || idx.Records[0].ErrorKinds != nil {
		t.Fatalf("index = %+v", idx)
	}
}

func TestCollectRecordsJSONRPCAndHTTPErrors(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadGateway, Status: "502 Bad Gateway", Body: io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"nope"}}`)), Request: r}, nil
	})
	plan := Plan{Schema: PlanSchema, Network: Network{ChainID: 17000, Fork: "pectra"}, Sources: []Source{{ID: "s", Endpoint: "https://rpc.example"}}, Requests: []Request{{ID: "x", Method: "eth_chainId", Params: json.RawMessage("[]")}}}
	idx, err := (Collector{Client: &http.Client{Transport: transport}}).Collect(context.Background(), plan, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Records[0].ErrorKinds) != 2 || idx.Records[0].ErrorKinds[0] != "http" || idx.Records[0].ErrorKinds[1] != "jsonrpc" {
		t.Fatalf("error kinds = %#v", idx.Records[0].ErrorKinds)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestLoadPlanRejectsUnsafeIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(path, []byte(`{"schema":"`+PlanSchema+`","network":{"chain_id":1,"fork":"x"},"sources":[{"id":"../escape","endpoint":"http://localhost"}],"requests":[{"id":"r","method":"x","params":[]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPlan(path); err == nil {
		t.Fatal("expected unsafe id error")
	}
}

// countingReader yields n bytes of 'x' one chunk at a time without
// materializing them, so a bound test can exercise multi-megabyte limits
// cheaply.
type countingReader struct{ remaining int64 }

func (r *countingReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, errEOF
	}
	n := int64(len(p))
	if n > r.remaining {
		n = r.remaining
	}
	for i := int64(0); i < n; i++ {
		p[i] = 'x'
	}
	r.remaining -= n
	return int(n), nil
}

var errEOF = errors.New("eof")

// TestReadBodyBoundsUnconfiguredLimit pins the capture-safety contract: a
// non-positive MaxResponseBytes means "unconfigured", not "unbounded" — the
// read falls back to the cross-package body bound so a hostile endpoint
// cannot stream an unbounded response into memory.
func TestReadBodyBoundsUnconfiguredLimit(t *testing.T) {
	body, truncated, err := readBody(&countingReader{remaining: limits.MaxBodyBytes + 1}, 0)
	if err != nil && err != errEOF {
		t.Fatalf("readBody: %v", err)
	}
	if int64(len(body)) != limits.MaxBodyBytes {
		t.Fatalf("unconfigured read = %d bytes, want the %d cross-package bound", len(body), limits.MaxBodyBytes)
	}
	if !truncated {
		t.Fatal("body exceeding the bound must be reported truncated")
	}
	// An explicit limit keeps its exact behavior: over the limit means
	// truncated with the capped bytes, under it means untouched.
	small, truncated, err := readBody(strings.NewReader("hello capture"), 4)
	if err != nil || !truncated || string(small) != "hell" {
		t.Fatalf("explicit over-limit read changed: %q truncated=%v err=%v", small, truncated, err)
	}
	whole, truncated, err := readBody(strings.NewReader("tiny"), 4)
	if err != nil || truncated || string(whole) != "tiny" {
		t.Fatalf("explicit under-limit read changed: %q truncated=%v err=%v", whole, truncated, err)
	}
}
