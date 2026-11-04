package ethguard

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpctest"
)

// dialFleet spins up count scripted nodes and dials them the way an
// application would — through the endpoint-list entries, not a hand-built
// Config.
func dialFleet(t *testing.T, count int) (*Client, []*rpctest.Node) {
	t.Helper()
	urls := make([]string, 0, count)
	nodes := make([]*rpctest.Node, 0, count)
	for range count {
		node := rpctest.NewNode(1)
		t.Cleanup(node.Close)
		nodes = append(nodes, node)
		urls = append(urls, node.URL())
	}
	client, err := Dial(urls...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client, nodes
}

func TestDialRequiresTwoDistinctEndpoints(t *testing.T) {
	if _, err := Dial("https://a.example"); err == nil || !strings.Contains(err.Error(), "DialUncorroborated") {
		t.Fatalf("single-endpoint dial must fail with the explicit-entry hint, got %v", err)
	}
	if _, err := Dial(); err == nil || !strings.Contains(err.Error(), "at least two distinct endpoints") {
		t.Fatalf("empty dial must fail, got %v", err)
	}
	_, err := Dial("https://a.example", "https://a.example")
	if err == nil || !strings.Contains(err.Error(), "duplicates an earlier entry") {
		t.Fatalf("duplicate endpoints must fail, got %v", err)
	}
}

func TestDialRejectsUnsupportedSchemeWithFixHint(t *testing.T) {
	_, err := Dial("ftp://a.example", "https://b.example")
	if err == nil {
		t.Fatal("expected scheme rejection")
	}
	for _, want := range []string{"upstreams[0]", `"ftp"`, "use http, https, ws, or wss"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error must carry field path, index, and fix hint; missing %q in %v", want, err)
		}
	}
}

func TestDialBuildsCorroboratedClient(t *testing.T) {
	client, _ := dialFleet(t, 3)
	if client.Mode() != rpc.CorroborationMajority {
		t.Fatalf("mode = %v, want majority from the default configuration", client.Mode())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	id, _, err := client.ChainID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if id.Int64() != 1 {
		t.Fatalf("chain id = %v, want 1", id)
	}
}

func TestDialDoesNotImplicitlyPinChainID(t *testing.T) {
	// The dial path never sets ChainID: the derived config's zero value must
	// reach composition, so a fleet on any chain answers without a mismatch.
	urls := []string{resourceUnavailableURL(t), "https://b.example"}
	cfg, err := config.FromEndpoints(urls...)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ChainID != 0 {
		t.Fatalf("FromEndpoints set chain_id = %d; dial must not pin a chain implicitly", cfg.ChainID)
	}
}

func TestDialUncorroboratedIsExplicitForwardedMode(t *testing.T) {
	node := rpctest.NewNode(1)
	t.Cleanup(node.Close)
	client, err := DialUncorroborated(node.URL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	if client.Mode() != rpc.CorroborationForwarded {
		t.Fatalf("mode = %v, want forwarded", client.Mode())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	id, _, err := client.ChainID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if id.Int64() != 1 {
		t.Fatalf("chain id = %v, want 1", id)
	}
}

// TestDialConstructionIsLazy pins the construction contract: no endpoint is
// contacted while building, so unreachable endpoints still yield a usable
// Client object (its first call will classify the failure).
func TestDialConstructionIsLazy(t *testing.T) {
	client, err := Dial("http://127.0.0.1:1", "http://127.0.0.1:2")
	if err != nil {
		t.Fatalf("lazy construction must not dial: %v", err)
	}
	t.Cleanup(client.Close)
	if client.VotingMembers() != 2 {
		t.Fatalf("voting members = %d, want the two configured endpoints", client.VotingMembers())
	}
}

func TestDialContextRespectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := DialContext(ctx, "https://a.example", "https://b.example")
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled construction must fail with context.Canceled, got %v", err)
	}
}

func TestLoadFileSurfacesFileErrors(t *testing.T) {
	_, err := LoadFile(t.TempDir() + "/missing.yaml")
	if err == nil || !strings.Contains(err.Error(), "missing.yaml") {
		t.Fatalf("file-path errors must surface the path, got %v", err)
	}
}

// TestWithChainIDOptionWinsOverConfig pins the Go-option precedence over the
// configuration value in both directions.
func TestWithChainIDOptionWinsOverConfig(t *testing.T) {
	node := rpctest.NewNode(1)
	t.Cleanup(node.Close)

	// Config pins the wrong chain: the option overrides it and reads work.
	cfg, err := config.FromEndpoints(node.URL())
	if err != nil {
		t.Fatal(err)
	}
	cfg.ChainID = 999
	client, err := New(cfg, WithChainID(1))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if id, _, err := client.ChainID(ctx); err != nil || id.Int64() != 1 {
		t.Fatalf("WithChainID must override config chain_id, got id=%v err=%v", id, err)
	}

	// The option pins the wrong chain: sources on the real chain are rejected.
	cfg, err = config.FromEndpoints(node.URL())
	if err != nil {
		t.Fatal(err)
	}
	mismatched, err := New(cfg, WithChainID(999))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mismatched.Close)
	if _, _, err := mismatched.ChainID(ctx); err == nil {
		t.Fatal("pinned foreign chain must fail the read")
	}
}

// resourceUnavailableURL returns a legal but uncontactable endpoint for
// config-only assertions.
func resourceUnavailableURL(t *testing.T) string {
	t.Helper()
	return "http://127.0.0.1:1"
}
