package ethguard_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/rpc"
)

// Passing config validation must mean the production composition can use the
// same endpoints, including WebSocket transport inferred from its URL.
func TestValidatedYAMLWithInferredTransportBuildsWorkingClient(t *testing.T) {
	for _, explicitIDs := range []bool{true, false} {
		t.Run(fmt.Sprintf("explicitIDs=%v", explicitIDs), func(t *testing.T) {
			http := newSourceWirePeer(t, false, func(sourceWireRequest) string { return `"result":null` })
			ws := newSourceWirePeer(t, true, func(sourceWireRequest) string { return `"result":null` })
			body := fmt.Sprintf("upstreams:\n  - endpoint: %q\n  - endpoint: %q\n", http.endpoint, ws.endpoint)
			if explicitIDs {
				body = fmt.Sprintf("upstreams:\n  - id: first\n    endpoint: %q\n  - id: second\n    endpoint: %q\n", http.endpoint, ws.endpoint)
			}
			cfg, err := config.ParseYAML([]byte(body))
			if err != nil {
				t.Fatalf("infer YAML configuration: %v", err)
			}
			if err := cfg.Validate(); err != nil {
				t.Fatalf("parsed config does not validate: %v", err)
			}
			client, err := ethguard.New(cfg)
			if err != nil {
				t.Fatalf("validated inferred transports cannot compose: %v", err)
			}
			t.Cleanup(client.Close)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			id, result, err := client.ChainID(ctx)
			if err != nil || id == nil || id.Uint64() != 1 || !result.IsCorroborated() {
				t.Fatalf("inferred mixed-transport call: id=%v result=%+v err=%v", id, result, err)
			}
			if http.httpConnections.Load() == 0 || ws.connections.Load() == 0 {
				t.Fatal("mixed configuration did not use both real transports")
			}
		})
	}
}

func TestManualConfigurationInfersSourcesWithoutMutatingCaller(t *testing.T) {
	http := newSourceWirePeer(t, false, func(sourceWireRequest) string { return `"result":null` })
	ws := newSourceWirePeer(t, true, func(sourceWireRequest) string { return `"result":null` })
	input := config.Config{Upstreams: []config.Upstream{{Endpoint: http.endpoint}, {Endpoint: ws.endpoint}}}
	client, err := ethguard.New(input)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	for _, source := range input.Upstreams {
		if source.ID != "" || source.Transport != "" {
			t.Fatal("New rewrote the caller-owned configuration")
		}
	}
	sources := client.Sources()
	if len(sources) != 2 || sources[0].Source.Name != "rpc-1" || sources[1].Source.Name != "rpc-2" {
		t.Fatalf("inferred diagnostic source identities=%+v", sources)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	id, result, err := client.ChainID(ctx)
	if err != nil || id == nil || id.Uint64() != 1 || !result.IsCorroborated() {
		t.Fatalf("manual inferred configuration: id=%v result=%+v err=%v", id, result, err)
	}
}

func TestInferredMixedTransportsKeepStrictSubscriptionDenominator(t *testing.T) {
	http := newSourceWirePeer(t, false, func(sourceWireRequest) string { return `"result":null` })
	ws := newSourceWirePeer(t, true, func(sourceWireRequest) string { return `"result":null` })
	body := fmt.Sprintf("upstreams:\n  - endpoint: %q\n  - endpoint: %q\n", http.endpoint, ws.endpoint)
	cfg, err := config.ParseYAML([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	client, err := ethguard.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	for _, view := range []bool{false, true} {
		for _, method := range []string{"heads", "logs", "receipts"} {
			t.Run(fmt.Sprintf("view=%v/%s", view, method), func(t *testing.T) {
				if err := subscriptionRejectionCall(t, client, method, view, false); !errors.Is(err, ethguard.ErrInvalidRequest) {
					t.Fatalf("one subscription-capable source in a majority client: %v", err)
				}
			})
		}
	}
	if client.Mode() != rpc.CorroborationMajority || client.VotingMembers() != 2 {
		t.Fatal("subscription eligibility changed the client mode or unary basis")
	}
	if http.requests.Load() != 0 || ws.requests.Load() != 0 || http.httpConnections.Load() != 0 || ws.connections.Load() != 0 {
		t.Fatal("local subscription rejection performed network I/O")
	}
}
