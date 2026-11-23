package ethguard_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/ethereum/go-ethereum/core/types"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/internal/testkit/wstest"
	"github.com/codermaybe/ethguard/rpc"
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

	mu           sync.Mutex
	requests     []subscriptionPeerRequest
	pending      [][]byte
	seq          int
	writeMu      sync.Mutex
	push         func([]byte) error
	headResponse []byte
}

// newSubscriptionPeer starts a WebSocket peer that answers the scripted
// calls by method name. The notification payloads are queued before the
// subscription is established and pushed right after the registration answer.
func newSubscriptionPeer(t *testing.T, scripts map[string][]byte) *subscriptionPeer {
	t.Helper()
	peer := &subscriptionPeer{headResponse: scripts["eth_getBlockByNumber"]}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wstest.Upgrade(t, w, r)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		write := func(raw []byte) error { peer.writeMu.Lock(); defer peer.writeMu.Unlock(); return conn.WriteText(raw) }
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
			if req.Method == "eth_getBlockByNumber" {
				peer.mu.Lock()
				script = peer.headResponse
				scripted = len(script) > 0
				peer.mu.Unlock()
			}
			if req.Method == "eth_subscribe" {
				peer.mu.Lock()
				queued := peer.pending
				peer.pending = nil
				peer.mu.Unlock()
				id := fmt.Sprintf("sub-%d", seq)
				peer.mu.Lock()
				peer.push = func(raw []byte) error {
					peer.writeMu.Lock()
					defer peer.writeMu.Unlock()
					return conn.WriteText([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":%q,"result":%s}}`, id, raw)))
				}
				peer.mu.Unlock()
				if err := write([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":"%s"}`, req.ID, id))); err != nil {
					return
				}
				for _, result := range queued {
					notification := []byte(fmt.Sprintf(`{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":%q,"result":%s}}`, id, result))
					if err := write(notification); err != nil {
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
			if err := write([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":%s}`, req.ID, script))); err != nil {
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

// Both peers witness the current tips; one independently valid hash lookup
// supplies a missing object without creating another source vote.
func TestSubscribeNewHeadPublicEntrypointsUsePathWitnessesAndHashRecovery(t *testing.T) {
	for _, view := range []bool{false, true} {
		t.Run(fmt.Sprintf("view=%t", view), func(t *testing.T) {
			raw := fixtureBlock(t, 17)
			a := newSubscriptionPeer(t, map[string][]byte{"eth_chainId": []byte(`"0x1"`)})
			var birth types.Header
			if err := json.Unmarshal(raw, &birth); err != nil {
				t.Fatal(err)
			}
			missing := birth
			missing.ParentHash = birth.Hash()
			missing.Number = big.NewInt(18)
			middleFixture, err := json.Marshal(&missing)
			if err != nil {
				t.Fatal(err)
			}
			b := newSubscriptionPeer(t, map[string][]byte{"eth_chainId": []byte(`"0x1"`), "eth_getBlockByHash": middleFixture})
			a.queueNotification(raw)
			b.queueNotification(raw)
			client := subscriptionClient(t, a, b)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			var sub *ethguard.HeadSubscription
			if view {
				sub, err = client.EthClient().SubscribeNewHead(ctx, ethguard.HeadSubscriptionPolicy{MemoryBytes: 8 << 20, RetainedNodes: 64, MaxConcurrentRequests: 8, RecoveryTimeout: 5 * time.Second, WireMessages: 8, WireBytes: 1 << 16, MaxMessageBytes: 1 << 14})
			} else {
				sub, err = client.SubscribeNewHead(ctx, ethguard.HeadSubscriptionPolicy{MemoryBytes: 8 << 20, RetainedNodes: 64, MaxConcurrentRequests: 8, RecoveryTimeout: 5 * time.Second, WireMessages: 8, WireBytes: 1 << 16, MaxMessageBytes: 1 << 14})
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = sub.Close() })
			event, nextErr := sub.Next(ctx)
			if nextErr != nil || len(event.Path.Appended) != 1 {
				t.Fatalf("initial event %+v err=%v", event, nextErr)
			}
			header := event.Path.Appended[0].Header
			height, heightErr := rpc.ParseHexQuantity(string(header.Number))
			if heightErr != nil || height != 17 || string(header.Hash) != blockHashOf(raw) {
				t.Fatalf("header %+v", header)
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
				if !registered || pulled {
					t.Fatalf("registration/Pull attribution wrong: %v", requests)
				}
			}
			var alternate types.Header
			if err := json.Unmarshal(raw, &alternate); err != nil {
				t.Fatal(err)
			}
			birthHash := alternate.Hash()
			alternate.ParentHash = birthHash
			alternate.Number = big.NewInt(18)
			forward, err := json.Marshal(&alternate)
			if err != nil {
				t.Fatal(err)
			}
			middle := forward
			alternate.ParentHash = alternate.Hash()
			alternate.Number = big.NewInt(19)
			forward, err = json.Marshal(&alternate)
			if err != nil {
				t.Fatal(err)
			}
			for _, peer := range []*subscriptionPeer{a, b} {
				peer.mu.Lock()
				peer.headResponse = forward
				push := peer.push
				peer.mu.Unlock()
				if err := push(forward); err != nil {
					t.Fatal(err)
				}
			}
			forwardEvent, nextErr := sub.Next(ctx)
			if nextErr != nil {
				t.Fatal(nextErr)
			}
			if len(forwardEvent.Path.Appended) != 2 || len(forwardEvent.Evaluation.Witnesses) != 2 {
				t.Fatalf("gap %+v", forwardEvent)
			}
			b.mu.Lock()
			requests := append([]subscriptionPeerRequest(nil), b.requests...)
			b.mu.Unlock()
			found := false
			for _, request := range requests {
				if request.Method == "eth_getBlockByHash" {
					found = true
					if len(request.Params) != 2 || string(request.Params[0]) != fmt.Sprintf("%q", blockHashOf(middle)) || string(request.Params[1]) != `false` {
						t.Fatalf("hash request %s", request.Params)
					}
				}
			}
			if !found {
				t.Fatal("missing same-member hash recovery")
			}
			alternate.Extra = append(alternate.Extra, byte(77))
			changed, err := json.Marshal(&alternate)
			if err != nil {
				t.Fatal(err)
			}
			// Geth's Header marshal includes its recomputed hash.
			for _, peer := range []*subscriptionPeer{a, b} {
				peer.mu.Lock()
				peer.headResponse = changed
				push := peer.push
				peer.mu.Unlock()
				if push == nil {
					t.Fatal("no live notification stream")
				}
				if err := push(changed); err != nil {
					t.Fatal(err)
				}
			}
			replacement, nextErr := sub.Next(ctx)
			if nextErr != nil || len(replacement.Path.Retracted) != 1 || len(replacement.Path.Appended) != 1 || replacement.Path.BaseVersion != forwardEvent.Path.NewVersion {
				t.Fatalf("public reorg %+v err=%v", replacement, nextErr)
			}
			if string(replacement.Path.Appended[0].Header.Hash) != alternate.Hash().Hex() {
				t.Fatal("public reorg delivered wrong payload")
			}
			stopped := make(chan struct{})
			go func() { _ = sub.Close(); close(stopped) }()
			select {
			case <-stopped:
			case <-ctx.Done():
				t.Fatal("Unsubscribe did not finish")
			}
		})
	}
}
