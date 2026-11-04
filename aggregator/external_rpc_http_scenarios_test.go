package aggregator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"
	"github.com/codermaybe/ethguard/pool"
	"github.com/codermaybe/ethguard/rpc"
)

type httpFaultScenario struct {
	name     string
	status   int
	truncate bool
	body     func(id json.RawMessage) string
}

func newHTTPFaultServer(t *testing.T, scenario httpFaultScenario) string {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID json.RawMessage `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		body := scenario.body(request.ID)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-EthGuard-Test-Scenario", scenario.name)
		if scenario.truncate {
			w.Header().Set("Content-Length", fmt.Sprint(len(body)+1))
		}
		if scenario.status != 0 {
			w.WriteHeader(scenario.status)
		}
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func httpScenarioAggregator(t *testing.T, endpoints ...string) *Aggregator {
	t.Helper()

	members := make([]pool.Member, len(endpoints))
	for i, endpoint := range endpoints {
		members[i] = pool.Member{
			Name: fmt.Sprintf("node-%c", 'a'+i),
			URL:  endpoint,
		}
	}
	p := fixedPoolWith(t, 0, []pool.PoolOption{pool.WithDialer(nativehttp.NewDialer())}, members...)
	return New(p,
		WithCollectTimeout(0),
		WithPlans(map[string]rpc.ReadPlan{
			"m": {
				Method:   "m",
				Resolver: eventPolicy(rpc.WithRPCErrorBodies(), rpc.WithQuorum(1)),
			},
		}),
	)
}

func assertSingleSourceFault(t *testing.T, decision rpc.Decision, source string) error {
	t.Helper()

	if decision.Status != rpc.Unknown {
		t.Fatalf("status = %s, want unknown", decision.Status)
	}
	if len(decision.SourceFaults) != 1 {
		t.Fatalf("source faults = %+v, want exactly one", decision.SourceFaults)
	}
	if decision.SourceFaults[0].Source.Name != source {
		t.Fatalf("fault source = %q, want %q", decision.SourceFaults[0].Source.Name, source)
	}
	if decision.SourceFaults[0].Err == nil {
		t.Fatal("source fault lost its cause")
	}
	return decision.SourceFaults[0].Err
}

func TestExternalRPCHTTPMalformedPayloadFaultAttribution(t *testing.T) {
	scenarios := []httpFaultScenario{
		{
			name: "http-malformed-json",
			body: func(id json.RawMessage) string {
				return fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":`, id)
			},
		},
		{
			name:     "http-body-truncated",
			truncate: true,
			body: func(id json.RawMessage) string {
				return fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":"partial`, id)
			},
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			agg := httpScenarioAggregator(t, newHTTPFaultServer(t, scenario))
			decision, err := agg.Aggregate(context.Background(), rpc.Request{Method: "m"})
			if err == nil {
				t.Fatal("malformed payload unexpectedly resolved")
			}
			fault := assertSingleSourceFault(t, decision, "node-a")
			if scenario.truncate && !errors.Is(fault, io.ErrUnexpectedEOF) {
				t.Fatalf("truncated response fault = %v, want transport unexpected EOF", fault)
			}
			var protocol rpc.ProtocolError
			if errors.As(fault, &protocol) {
				t.Fatalf("malformed transport payload became a JSON-RPC protocol error: %v", fault)
			}
		})
	}
}

func TestExternalRPCHTTPStatusRateLimitedFaultAttribution(t *testing.T) {
	endpoint := newHTTPFaultServer(t, httpFaultScenario{
		name:   "http-status-rate-limited",
		status: http.StatusTooManyRequests,
		body: func(json.RawMessage) string {
			return `{"error":"rate limited"}`
		},
	})
	agg := httpScenarioAggregator(t, endpoint)

	decision, err := agg.Aggregate(context.Background(), rpc.Request{Method: "m"})
	if err == nil {
		t.Fatal("HTTP 429 unexpectedly resolved")
	}
	fault := assertSingleSourceFault(t, decision, "node-a")

	var status rpc.HTTPStatusError
	if !errors.As(fault, &status) || status.HTTPStatusCode() != http.StatusTooManyRequests {
		t.Fatalf("fault = %v, want HTTP status 429 capability", fault)
	}
	var rateLimit rpc.RateLimitError
	if !errors.As(fault, &rateLimit) || !rateLimit.IsRateLimit() {
		t.Fatalf("fault = %v, want rate-limit capability", fault)
	}
	var protocol rpc.ProtocolError
	if errors.As(fault, &protocol) {
		t.Fatalf("HTTP 429 became a JSON-RPC protocol error: %v", fault)
	}
}

func TestExternalRPCHTTPErrorDataPresenceEvidence(t *testing.T) {
	scenarios := []struct {
		name      string
		data      string
		present   bool
		wantState rpc.ErrorDataPresenceState
		wantRaw   string
	}{
		{
			name:      "jsonrpc-error-data-missing",
			wantState: rpc.ErrorDataPresenceMissing,
		},
		{
			name:      "jsonrpc-error-data-null",
			data:      "null",
			present:   true,
			wantState: rpc.ErrorDataPresenceNull,
			wantRaw:   "null",
		},
		{
			name:      "jsonrpc-error-data-empty-hex",
			data:      `"0x"`,
			present:   true,
			wantState: rpc.ErrorDataPresenceValue,
			wantRaw:   `"0x"`,
		},
		{
			name:      "jsonrpc-error-data-value",
			data:      `"0xdeadbeef"`,
			present:   true,
			wantState: rpc.ErrorDataPresenceValue,
			wantRaw:   `"0xdeadbeef"`,
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			endpoint := newHTTPFaultServer(t, httpFaultScenario{
				name: scenario.name,
				body: func(id json.RawMessage) string {
					member := ""
					if scenario.present {
						member = `,"data":` + scenario.data
					}
					return fmt.Sprintf(
						`{"jsonrpc":"2.0","id":%s,"error":{"code":-32603,"message":"provider failure"%s}}`,
						id,
						member,
					)
				},
			})
			agg := httpScenarioAggregator(t, endpoint)

			decision, err := agg.Aggregate(context.Background(), rpc.Request{Method: "m"})
			if err == nil {
				t.Fatal("rejected protocol error unexpectedly resolved")
			}
			fault := assertSingleSourceFault(t, decision, "node-a")

			var presence rpc.ErrorDataPresence
			if !errors.As(fault, &presence) {
				t.Fatalf("fault = %v, lost error-data presence capability", fault)
			}
			raw, present := presence.ErrorDataJSON()
			if present != scenario.present {
				t.Fatalf("presence = %v, want %v", present, scenario.present)
			}
			if scenario.present && string(raw) != scenario.wantRaw {
				t.Fatalf("raw data = %s, want %s", raw, scenario.wantRaw)
			}

			observation := ObservationRPCError(fault)
			if observation == nil {
				t.Fatal("protocol fault did not produce structured RPC error evidence")
			}
			if observation.DataPresence != scenario.wantState {
				t.Fatalf("data presence = %s, want %s", observation.DataPresence, scenario.wantState)
			}
			if string(observation.Data) != scenario.wantRaw {
				t.Fatalf("evidence data = %s, want %s", observation.Data, scenario.wantRaw)
			}
		})
	}
}

func TestExternalRPCHTTPVendorExtensionDoesNotChangeDecision(t *testing.T) {
	endpoint := newHTTPFaultServer(t, httpFaultScenario{
		name: "http-vendor-extension",
		body: func(id json.RawMessage) string {
			return fmt.Sprintf(
				`{"jsonrpc":"2.0","id":%s,"result":"stable","vendor":{"region":"test","trace":"ignored"}}`,
				id,
			)
		},
	})
	agg := httpScenarioAggregator(t, endpoint)

	decision, err := agg.Aggregate(context.Background(), rpc.Request{Method: "m"})
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if decision.Status != rpc.Selected {
		t.Fatalf("status = %s, want selected", decision.Status)
	}
	if string(decision.Value) != `"stable"` {
		t.Fatalf("value = %s, want canonical result only", decision.Value)
	}
	if len(decision.Support) != 1 || decision.Support[0].Name != "node-a" {
		t.Fatalf("support = %+v, want node-a", decision.Support)
	}
	if len(decision.Faults) != 0 || len(decision.SourceFaults) != 0 || len(decision.CollectionFaults) != 0 {
		t.Fatalf(
			"vendor extension polluted fault evidence: faults=%+v source=%+v collection=%+v",
			decision.Faults,
			decision.SourceFaults,
			decision.CollectionFaults,
		)
	}
}

func TestExternalRPCHTTPCancellationAttribution(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
	}))
	t.Cleanup(endpoint.Close)

	agg := httpScenarioAggregator(t, endpoint.URL)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan struct {
		decision rpc.Decision
		err      error
	}, 1)
	go func() {
		decision, err := agg.Aggregate(ctx, rpc.Request{Method: "m"})
		result <- struct {
			decision rpc.Decision
			err      error
		}{decision: decision, err: err}
	}()

	<-started
	cancel()
	got := <-result
	close(release)
	if !errors.Is(got.err, context.Canceled) {
		t.Fatalf("err = %v, want context canceled", got.err)
	}
	fault := assertSingleSourceFault(t, got.decision, "node-a")
	if !errors.Is(fault, context.Canceled) {
		t.Fatalf("source fault = %v, want context canceled", fault)
	}
}
