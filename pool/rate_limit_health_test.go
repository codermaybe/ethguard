package pool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"
	"github.com/codermaybe/ethguard/internal/transport/wss"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/gorilla/websocket"
)

// healthRPCPeer exercises the same provider responses through both adapters.
func healthRPCPeer(t *testing.T, websocketMode bool, code int) (string, dialer) {
	t.Helper()
	respond := func(body []byte) []byte {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("decode request: %v", err)
			return nil
		}
		switch req.Method {
		case "eth_chainId", "eth_ok":
			return []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":"0x1"}`, req.ID))
		case "eth_unsupported":
			return []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"method not found"}}`, req.ID))
		default:
			// Deliberately neutral text: classification must use the code.
			return []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"error":{"code":%d,"message":"request unavailable"}}`, req.ID, code))
		}
	}
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if websocketMode {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Errorf("upgrade: %v", err)
				return
			}
			defer conn.Close()
			for {
				_, body, err := conn.ReadMessage()
				if err != nil {
					return
				}
				if err := conn.WriteMessage(websocket.TextMessage, respond(body)); err != nil {
					return
				}
			}
		}
		var body json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("request: %v", err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(respond(body))
	}))
	t.Cleanup(server.Close)
	if websocketMode {
		return "ws" + strings.TrimPrefix(server.URL, "http"), wss.NewDialer()
	}
	return server.URL, nativehttp.NewDialer()
}

func TestRateLimitHealthAcrossTransports(t *testing.T) {
	for _, websocketMode := range []bool{false, true} {
		for _, code := range []int{-32005, -32016, 429, -32603} {
			t.Run(fmt.Sprintf("websocket=%t/code=%d", websocketMode, code), func(t *testing.T) {
				endpoint, adapter := healthRPCPeer(t, websocketMode, code)
				p, err := NewWithMembers(1, []Member{{Name: "provider", URL: endpoint}}, WithDialer(adapter))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(p.Close)
				src := p.VotingMembers()[0]
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if _, err := p.CollectVoting(ctx, "eth_ok", nil, CollectOptions{}); err != nil {
					t.Fatal(err)
				}
				before := src.Health()
				emissions, err := p.Broadcast(ctx, "eth_sendRawTransaction", []any{"0x01"})
				if err != nil {
					t.Fatalf("protocol answer lost: %v", err)
				}
				var protocol rpc.ProtocolError
				if len(emissions) != 1 || !emissions[0].Attempted || !errors.As(emissions[0].Err, &protocol) || protocol.ErrorCode() != code {
					t.Fatalf("emissions = %+v, want attempted protocol error %d", emissions, code)
				}
				after := src.Health()
				if after.Failures != before.Failures+1 || after.ConsecutiveFailures != 1 || after.Successes != before.Successes || after.RequestPace <= before.RequestPace {
					t.Fatalf("failure did not slow source: before=%+v after=%+v", before, after)
				}
				if code == -32603 {
					if after.ProtocolServerErrors != 1 || after.RateLimited != 0 {
						t.Fatalf("server classification = %+v", after)
					}
				} else if after.RateLimited != 1 {
					t.Fatalf("missing rate-limit count: %+v", after)
				}
				if _, err := p.CollectVoting(ctx, "eth_unsupported", nil, CollectOptions{}); err != nil {
					t.Fatal(err)
				}
				recovered := src.Health()
				if recovered.ConsecutiveFailures != 0 || recovered.Successes != before.Successes+1 || recovered.Failures != after.Failures || src.State() != SourceReady {
					t.Fatalf("unsupported method must be healthy: %+v", recovered)
				}
			})
		}
	}
}

func TestRateLimitCooldownRetainsReadWriteMembership(t *testing.T) {
	for _, websocketMode := range []bool{false, true} {
		t.Run(fmt.Sprintf("websocket=%t", websocketMode), func(t *testing.T) {
			endpoint, adapter := healthRPCPeer(t, websocketMode, -32005)
			p, err := NewWithMembers(1, []Member{{Name: "provider", URL: endpoint}}, WithDialer(adapter))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(p.Close)
			members := p.VotingMembers()
			src := members[0]
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for range eligibleFailureThreshold {
				if _, err := p.Broadcast(ctx, "eth_sendRawTransaction", []any{"0x01"}); err != nil {
					t.Fatal(err)
				}
			}
			before := src.Health()
			if before.ConsecutiveFailures != 3 || src.requestable(time.Now()) {
				t.Fatalf("circuit did not open: %+v", before)
			}
			for _, write := range []bool{false, true} {
				var emissions []Emission
				if write {
					emissions, err = p.Broadcast(ctx, "eth_sendRawTransaction", []any{"0x01"})
				} else {
					emissions, err = p.CollectVoting(ctx, "eth_ok", nil, CollectOptions{})
				}
				if !errors.Is(err, ErrNoEligibleSource) || len(emissions) != 1 || emissions[0].Src != src || emissions[0].Attempted || !errors.Is(emissions[0].Err, ErrSourceGated) {
					t.Fatalf("write=%t gated emission=%+v err=%v", write, emissions, err)
				}
			}
			if got := src.Health(); got.Failures != before.Failures || got.ConsecutiveFailures != before.ConsecutiveFailures {
				t.Fatalf("gate penalized provider: %+v", got)
			}
			if got := p.VotingMembers(); len(got) != len(members) || got[0] != src {
				t.Fatalf("membership changed: %+v", got)
			}
			// Advance only the test's circuit timestamp, not a production clock seam.
			src.stats.mu.Lock()
			src.stats.lastFailure = time.Now().Add(-eligibleFailureCooldown - time.Second)
			src.stats.mu.Unlock()
			if _, err := p.CollectVoting(ctx, "eth_ok", nil, CollectOptions{}); err != nil {
				t.Fatal(err)
			}
			if got := src.Health(); got.ConsecutiveFailures != 0 || got.Successes != 1 {
				t.Fatalf("source did not recover: %+v", got)
			}
		})
	}
}
