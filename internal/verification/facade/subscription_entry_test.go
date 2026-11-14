package ethguard_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/internal/testkit/wstest"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
)

// subscriptionPeerRequest is one JSON-RPC request the peer received.
type subscriptionPeerRequest struct {
	Method string
	Params []json.RawMessage
}

// subscriptionPeer is one scripted WSS peer. It answers the identity probe
// and the eth_subscribe registration on the subscription socket and pushes the
// scripted notifications once the registration is answered.
type subscriptionPeer struct {
	endpoint string

	mu       sync.Mutex
	requests []subscriptionPeerRequest
	pending  [][]byte
	seq      int
}

// newSubscriptionPeer starts a WebSocket peer that answers the scripted
// calls by method name. The notification payloads are queued before the
// subscription is established and pushed right after the registration answer.
func newSubscriptionPeer(t *testing.T, scripts map[string][]byte) *subscriptionPeer {
	t.Helper()
	peer := &subscriptionPeer{}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wstest.Upgrade(t, w, r)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		for {
			body, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req struct {
				ID     json.RawMessage   `json:"id"`
				Method string            `json:"method"`
				Params []json.RawMessage `json:"params"`
			}
			if err := json.Unmarshal(body, &req); err != nil {
				t.Errorf("decode request: %v", err)
				return
			}
			peer.mu.Lock()
			peer.requests = append(peer.requests, subscriptionPeerRequest{Method: req.Method, Params: req.Params})
			peer.seq++
			seq := peer.seq
			peer.mu.Unlock()

			script, scripted := scripts[req.Method]
			if req.Method == "eth_subscribe" {
				peer.mu.Lock()
				queued := peer.pending
				peer.pending = nil
				peer.mu.Unlock()
				id := fmt.Sprintf("sub-%d", seq)
				if err := conn.WriteText([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":"%s"}`, req.ID, id))); err != nil {
					return
				}
				for _, result := range queued {
					notification := []byte(fmt.Sprintf(`{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":%q,"result":%s}}`, id, result))
					if err := conn.WriteText(notification); err != nil {
						return
					}
				}
				continue
			}
			if req.Method == "eth_unsubscribe" {
				script, scripted = []byte(`true`), true
			}
			if !scripted {
				continue
			}
			if err := conn.WriteText([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":%s}`, req.ID, script))); err != nil {
				return
			}
		}
	}))
	srv.Start()
	t.Cleanup(srv.Close)
	peer.endpoint = "ws" + strings.TrimPrefix(srv.URL, "http")
	return peer
}

// queueNotification records a subscription notification frame the peer pushes
// after the subscription registration is answered.
func (p *subscriptionPeer) queueNotification(result []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pending = append(p.pending, append([]byte(nil), result...))
}

// requestedMethods reports the methods the peer saw, in order.
func (p *subscriptionPeer) requestedMethods() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.requests))
	for _, req := range p.requests {
		out = append(out, req.Method)
	}
	return out
}

// subscriptionClient wires a client to the given WebSocket peers. Two peers
// are the minimum for the strict subscription corroboration mode.
func subscriptionClient(t *testing.T, peers ...*subscriptionPeer) *ethguard.Client {
	t.Helper()
	cfg := config.Config{ChainID: 1}
	for i, peer := range peers {
		cfg.Upstreams = append(cfg.Upstreams, config.Upstream{
			ID:        fmt.Sprintf("ws-%c", 'a'+i),
			Endpoint:  peer.endpoint,
			Transport: config.TransportWSS,
		})
	}
	client, err := ethguard.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client
}

// Only one peer pushes the event. The other must independently return the
// same height through its own unary connection before either public facade
// can deliver it. This exercises the real WSS registration, notification ID,
// quorum, same-source Pull, typed delivery, and explicit unsubscribe boundary.
func TestSubscribeNewHeadPublicEntrypointsUseSameSourcePull(t *testing.T) {
	for _, view := range []bool{false, true} {
		t.Run(fmt.Sprintf("view=%t", view), func(t *testing.T) {
			raw := fixtureBlock(t, 17)
			a := newSubscriptionPeer(t, map[string][]byte{"eth_chainId": []byte(`"0x1"`)})
			b := newSubscriptionPeer(t, map[string][]byte{"eth_chainId": []byte(`"0x1"`), "eth_getBlockByNumber": raw})
			a.queueNotification(raw)
			client := subscriptionClient(t, a, b)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out := make(chan *types.Header, 1)
			var sub ethereum.Subscription
			var err error
			if view {
				sub, err = client.EthClient().SubscribeNewHead(ctx, out)
			} else {
				sub, err = client.SubscribeNewHead(ctx, out)
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(sub.Unsubscribe)
			select {
			case header := <-out:
				if header == nil || header.Number == nil || header.Number.Uint64() != 17 || header.Hash().Hex() != blockHashOf(raw) {
					t.Fatalf("delivered header = %+v", header)
				}
			case err := <-sub.Err():
				t.Fatalf("subscription ended before delivery: %v", err)
			case <-ctx.Done():
				t.Fatalf("no corroborated head: %v; A=%v B=%v", ctx.Err(), a.requestedMethods(), b.requestedMethods())
			}
			for _, peer := range []*subscriptionPeer{a, b} {
				peer.mu.Lock()
				requests := append([]subscriptionPeerRequest(nil), peer.requests...)
				peer.mu.Unlock()
				registered := false
				pulled := false
				for _, req := range requests {
					switch req.Method {
					case "eth_subscribe":
						if len(req.Params) != 1 || string(req.Params[0]) != `"newHeads"` {
							t.Fatalf("registration params = %s", req.Params)
						}
						registered = true
					case "eth_getBlockByNumber":
						if len(req.Params) != 2 || string(req.Params[0]) != `"0x11"` || string(req.Params[1]) != `false` {
							t.Fatalf("Pull params = %s", req.Params)
						}
						pulled = true
					}
				}
				if !registered || (peer == b && !pulled) || (peer == a && pulled) {
					t.Fatalf("registration/Pull attribution wrong: %v", requests)
				}
			}
			stopped := make(chan struct{})
			go func() { sub.Unsubscribe(); close(stopped) }()
			select {
			case <-stopped:
			case <-ctx.Done():
				t.Fatal("Unsubscribe did not finish")
			}
			select {
			case err, ok := <-sub.Err():
				if ok {
					t.Fatalf("unexpected terminal error: %v", err)
				}
			default:
				t.Fatal("error stream remains open after Unsubscribe")
			}
		})
	}
}
