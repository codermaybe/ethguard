package ethguard_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpctest"
)

func TestSingleSourceConstructorsAreLazyAndDoNotOwnCallerContext(t *testing.T) {
	for _, ws := range []bool{false, true} {
		for _, entry := range []string{"Dial", "DialContext", "Open", "OpenContext", "New", "LoadFile"} {
			t.Run(fmt.Sprintf("WSS=%v/%s", ws, entry), func(t *testing.T) {
				peer := newSourceWirePeer(t, ws, func(req sourceWireRequest) string {
					t.Errorf("unexpected construction/call request %s", req.Method)
					return `"result":null`
				})
				setup, cancelSetup := context.WithCancel(context.Background())
				defer cancelSetup()
				var client *ethguard.Client
				var err error
				switch entry {
				case "Dial":
					client, err = ethguard.Dial(peer.endpoint)
				case "DialContext":
					client, err = ethguard.DialContext(setup, peer.endpoint)
				case "Open":
					client, err = ethguard.Open([]string{peer.endpoint}, ethguard.WithChainID(1))
				case "OpenContext":
					client, err = ethguard.OpenContext(setup, []string{peer.endpoint}, ethguard.WithChainID(1))
				case "New":
					var cfg config.Config
					cfg, err = config.FromEndpoints(peer.endpoint)
					if err == nil {
						client, err = ethguard.New(cfg)
					}
				case "LoadFile":
					transport := "native-http"
					if ws {
						transport = "wss"
					}
					path := filepath.Join(t.TempDir(), "single.yaml")
					body := fmt.Sprintf("upstreams:\n  - id: one\n    endpoint: %q\n    transport: %s\n", peer.endpoint, transport)
					if err = os.WriteFile(path, []byte(body), 0o600); err == nil {
						client, err = ethguard.LoadFile(path)
					}
				}
				if err != nil {
					t.Fatalf("single-source %s: %v", entry, err)
				}
				t.Cleanup(client.Close)
				if client.Mode() != rpc.CorroborationForwarded || client.VotingMembers() != 1 {
					t.Fatalf("mode=%s members=%d", client.Mode(), client.VotingMembers())
				}
				if peer.requests.Load() != 0 || peer.connections.Load() != 0 || peer.httpConnections.Load() != 0 {
					t.Fatal("construction performed network I/O")
				}
				cancelSetup()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				id, result, err := client.ChainID(ctx)
				if err != nil || id == nil || id.Uint64() != 1 || result.Decision == nil || result.Decision.Status != rpc.Forwarded || result.IsCorroborated() {
					t.Fatalf("fresh call after construction context cancellation: id=%v result=%+v err=%v", id, result, err)
				}
			})
		}
	}
}

func TestOpenContextRejectsCanceledConstructionBeforeDial(t *testing.T) {
	peer := newSourceWirePeer(t, false, func(sourceWireRequest) string { return `"result":null` })
	for _, deadline := range []bool{false, true} {
		t.Run(fmt.Sprintf("deadline=%v", deadline), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			want := context.Canceled
			if deadline {
				cancel()
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				want = context.DeadlineExceeded
			} else {
				cancel()
			}
			defer cancel()
			for _, construct := range []func() (*ethguard.Client, error){
				func() (*ethguard.Client, error) {
					return ethguard.OpenContext(ctx, []string{peer.endpoint}, ethguard.WithChainID(1))
				},
				func() (*ethguard.Client, error) { return ethguard.DialContext(ctx, peer.endpoint) },
			} {
				client, err := construct()
				if client != nil {
					client.Close()
				}
				if client != nil || !errors.Is(err, want) {
					t.Fatalf("construction client=%v err=%v, want %v", client, err, want)
				}
			}
		})
	}
	if peer.requests.Load() != 0 || peer.httpConnections.Load() != 0 {
		t.Fatal("canceled construction contacted a source")
	}
}

func TestOpenPreservesOptionsAndDeclaredMembership(t *testing.T) {
	for _, useContext := range []bool{false, true} {
		for _, count := range []int{1, 2} {
			t.Run(fmt.Sprintf("context=%v/sources=%d", useContext, count), func(t *testing.T) {
				var endpoints []string
				for range count {
					node := rpctest.NewNode(1)
					t.Cleanup(node.Close)
					endpoints = append(endpoints, node.URL())
				}
				var client *ethguard.Client
				var err error
				if useContext {
					client, err = ethguard.OpenContext(context.Background(), endpoints, ethguard.WithChainID(999))
				} else {
					client, err = ethguard.Open(endpoints, ethguard.WithChainID(999))
				}
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(client.Close)
				wantMode := rpc.CorroborationForwarded
				if count > 1 {
					wantMode = rpc.CorroborationMajority
				}
				if client.VotingMembers() != count || client.Mode() != wantMode {
					t.Fatalf("members=%d mode=%s", client.VotingMembers(), client.Mode())
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				id, result, err := client.ChainID(ctx)
				if err == nil || id != nil || result.IsCorroborated() {
					t.Fatalf("WithChainID was lost: id=%v result=%+v err=%v", id, result, err)
				}
				if client.Mode() != wantMode || client.VotingMembers() != count {
					t.Fatal("identity failures changed the constructed voting basis")
				}
			})
		}
	}
}

func TestOpenRejectsEmptyAndEquivalentEndpoints(t *testing.T) {
	for _, endpoints := range [][]string{
		nil,
		{"https://node.example", "https://node.example"},
		{"https://node.example", "HTTPS://NODE.EXAMPLE:443/"},
	} {
		for _, useContext := range []bool{false, true} {
			var client *ethguard.Client
			var err error
			if useContext {
				client, err = ethguard.OpenContext(context.Background(), endpoints)
			} else {
				client, err = ethguard.Open(endpoints)
			}
			if client != nil {
				client.Close()
			}
			if err == nil || client != nil {
				t.Fatalf("Open context=%v endpoints=%v returned client=%v err=%v", useContext, endpoints, client, err)
			}
		}
	}
}

func TestSingleSourceFinalizedAnchoringRejectedAfterConfigurationMerge(t *testing.T) {
	for _, entry := range []string{"Open-option", "OpenContext-option", "New-config", "New-option-over-strict", "LoadFile-config", "LoadFile-option-over-strict"} {
		t.Run(entry, func(t *testing.T) {
			node := rpctest.NewNode(1)
			t.Cleanup(node.Close)
			cfg, err := config.FromEndpoints(node.URL())
			if err != nil {
				t.Fatal(err)
			}
			var client *ethguard.Client
			switch entry {
			case "Open-option":
				client, err = ethguard.Open([]string{node.URL()}, ethguard.WithFinalizedAnchoring())
			case "OpenContext-option":
				client, err = ethguard.OpenContext(context.Background(), []string{node.URL()}, ethguard.WithFinalizedAnchoring())
			case "New-config":
				cfg.Anchoring = &config.AnchoringConfig{Preset: " FiNaLiZeD "}
				client, err = ethguard.New(cfg)
			case "New-option-over-strict":
				cfg.Anchoring = &config.AnchoringConfig{Preset: "strict"}
				client, err = ethguard.New(cfg, ethguard.WithFinalizedAnchoring())
			case "LoadFile-config", "LoadFile-option-over-strict":
				preset := "finalized"
				var opts []ethguard.Option
				if entry == "LoadFile-option-over-strict" {
					preset, opts = "strict", []ethguard.Option{ethguard.WithFinalizedAnchoring()}
				}
				path := filepath.Join(t.TempDir(), "anchoring.yaml")
				body := fmt.Sprintf("upstreams:\n  - id: one\n    endpoint: %q\nanchoring:\n  preset: %s\n", node.URL(), preset)
				if writeErr := os.WriteFile(path, []byte(body), 0o600); writeErr != nil {
					t.Fatal(writeErr)
				}
				client, err = ethguard.LoadFile(path, opts...)
			}
			if client != nil {
				client.Close()
			}
			if err == nil || client != nil {
				t.Fatalf("single-source finalized configuration reached runtime: client=%v err=%v", client, err)
			}
			if len(node.Calls()) != 0 {
				t.Fatal("incompatible anchoring configuration performed network I/O")
			}
		})
	}
}
