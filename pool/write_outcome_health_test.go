package pool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/testkit/wstest"
	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/internal/transport/wss"
)

func TestSharedDisconnectCountsAttemptedEmissionsAndRedials(t *testing.T) {
	var sockets atomic.Int32
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wstest.Upgrade(t, w, r)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		generation := sockets.Add(1)
		for {
			body, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req struct {
				ID json.RawMessage `json:"id"`
			}
			if err := json.Unmarshal(body, &req); err != nil {
				t.Errorf("request: %v", err)
				return
			}
			n := requests.Add(1)
			if generation == 1 {
				if n == 2 {
					return
				}
				continue
			}
			if err := conn.WriteText([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":"0x1"}`, req.ID))); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	p, err := NewWithMembers(0, []Member{{Name: "shared", URL: "ws" + strings.TrimPrefix(srv.URL, "http"), Dialer: wss.NewDialer()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type outcome struct {
		emissions []Emission
		err       error
	}
	results := make(chan outcome, 2)
	for range 2 {
		go func() {
			emissions, err := p.Broadcast(ctx, "eth_sendRawTransaction", []any{"0x01"})
			results <- outcome{emissions, err}
		}()
	}
	for range 2 {
		result := <-results
		if !errors.Is(result.err, ErrAllSourcesDown) || len(result.emissions) != 1 || !result.emissions[0].Attempted || !errors.Is(result.emissions[0].Err, transport.ErrConnectionClosed) {
			t.Fatalf("abort outcome=%+v", result)
		}
	}
	src := p.VotingMembers()[0]
	if got := src.Health(); got.Failures != 2 || got.ConsecutiveFailures != 2 {
		t.Fatalf("one disconnect must account two attempted emissions: %+v", got)
	}
	if sockets.Load() != 1 || requests.Load() != 2 {
		t.Fatalf("invocation replay: sockets=%d requests=%d", sockets.Load(), requests.Load())
	}
	emissions, err := p.CollectVoting(ctx, "eth_blockNumber", nil, CollectOptions{})
	if err != nil || len(emissions) != 1 || string(emissions[0].Val) != `"0x1"` || sockets.Load() != 2 || requests.Load() != 3 {
		t.Fatalf("independent redial: emissions=%+v err=%v sockets=%d requests=%d", emissions, err, sockets.Load(), requests.Load())
	}
	if got := src.Health(); got.Failures != 2 || got.Successes != 1 || got.ConsecutiveFailures != 0 {
		t.Fatalf("recovery health=%+v", got)
	}
}

func TestReadLimitDiscardsClosedTransportBeforeNextInvocation(t *testing.T) {
	for _, chainID := range []uint64{0, 1} {
		t.Run(fmt.Sprintf("chainID=%d", chainID), func(t *testing.T) {
			checkReadLimitRedial(t, chainID)
		})
	}
}

func checkReadLimitRedial(t *testing.T, chainID uint64) {
	t.Helper()
	var sockets atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wstest.Upgrade(t, w, r)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		generation := sockets.Add(1)
		for {
			body, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req struct {
				ID json.RawMessage `json:"id"`
			}
			if err := json.Unmarshal(body, &req); err != nil {
				t.Errorf("request: %v", err)
				return
			}
			value := `"0x1"`
			if generation == 1 {
				value = `"` + strings.Repeat("a", 512) + `"`
			}
			if err := conn.WriteText([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":%s}`, req.ID, value))); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	p, err := NewWithMembers(chainID, []Member{{Name: "limited", URL: "ws" + strings.TrimPrefix(srv.URL, "http"), Dialer: wss.Dialer{ReadLimit: 128}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	emissions, _ := p.CollectVoting(ctx, "eth_blockNumber", nil, CollectOptions{})
	if len(emissions) != 1 || !errors.Is(emissions[0].Err, transport.ErrBodyTooLarge) || !errors.Is(emissions[0].Err, transport.ErrConnectionClosed) {
		t.Fatalf("read limit=%+v", emissions)
	}
	emissions, err = p.CollectVoting(ctx, "eth_blockNumber", nil, CollectOptions{})
	if err != nil || len(emissions) != 1 || string(emissions[0].Val) != `"0x1"` || sockets.Load() != 2 {
		t.Fatalf("next invocation reused dead transport: %+v err=%v sockets=%d", emissions, err, sockets.Load())
	}
	if h := p.VotingMembers()[0].Health(); h.Failures != 1 || h.Successes != 1 {
		t.Fatalf("dead socket caused avoidable extra failure: %+v", h)
	}
}
