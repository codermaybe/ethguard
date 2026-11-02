package ethguard_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/internal/testkit/wstest"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

type sourceWireRequest struct {
	ID     json.RawMessage   `json:"id"`
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
}

type sourceWirePeer struct {
	endpoint    string
	connections atomic.Int32
	writes      atomic.Int32
}

func newSourceWirePeer(t *testing.T, ws bool, answer func(sourceWireRequest) string) *sourceWirePeer {
	t.Helper()
	peer := &sourceWirePeer{}
	respond := func(body []byte) []byte {
		var req sourceWireRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("decode: %v", err)
			return nil
		}
		var result string
		if req.Method == "eth_chainId" {
			result = `"result":"0x1"`
		} else {
			if req.Method == "eth_sendRawTransaction" {
				peer.writes.Add(1)
			}
			result = answer(req)
		}
		if result == "" {
			return nil
		}
		return []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,%s}`, req.ID, result))
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ws {
			conn, err := wstest.Upgrade(t, w, r)
			if err != nil {
				t.Errorf("upgrade: %v", err)
				return
			}
			defer conn.Close()
			peer.connections.Add(1)
			for {
				body, err := conn.ReadMessage()
				if err != nil {
					return
				}
				response := respond(body)
				if response == nil {
					return
				}
				if err := conn.WriteText(response); err != nil {
					return
				}
			}
		}
		var body json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("request: %v", err)
			return
		}
		response := respond(body)
		if response == nil {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(response)
	}))
	t.Cleanup(srv.Close)
	peer.endpoint = srv.URL
	if ws {
		peer.endpoint = "ws" + strings.TrimPrefix(srv.URL, "http")
	}
	return peer
}

func unifiedSignedTransaction(t *testing.T) ([]byte, string) {
	t.Helper()
	key, err := crypto.HexToECDSA(strings.Repeat("01", 32))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := types.SignTx(types.NewTransaction(7, common.HexToAddress("0x01"), big.NewInt(1), 21000, big.NewInt(1), nil), types.HomesteadSigner{}, key)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := tx.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return raw, tx.Hash().Hex()
}

func TestUnifiedSourceWriteOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name         string
		httpReject   bool
		wsCode       int
		disconnect   bool
		want         rpc.WriteStatus
		wantAccepted int
		wantRejected int
		wantSilent   int
		wantFailure  uint64
	}{
		{name: "mixed acceptance", want: rpc.WriteAccepted, wantAccepted: 2},
		{name: "unsupported reduces support not basis", wsCode: -32601, want: rpc.WriteDisputed, wantAccepted: 1, wantRejected: 1},
		{name: "server rejection affects health", wsCode: -32603, want: rpc.WriteDisputed, wantAccepted: 1, wantRejected: 1, wantFailure: 1},
		{name: "rate limit affects health", wsCode: -32005, want: rpc.WriteDisputed, wantAccepted: 1, wantRejected: 1, wantFailure: 1},
		{name: "rejection coexists with unknown submission", httpReject: true, disconnect: true, want: rpc.WriteRejected, wantRejected: 1, wantSilent: 1, wantFailure: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, hash := unifiedSignedTransaction(t)
			httpPeer := newSourceWirePeer(t, false, func(req sourceWireRequest) string {
				if tc.httpReject {
					return `"error":{"code":-32601,"message":"method not found"}`
				}
				return `"result":"` + hash + `"`
			})
			var wsRequests atomic.Int32
			wsPeer := newSourceWirePeer(t, true, func(req sourceWireRequest) string {
				if req.Method != "eth_sendRawTransaction" {
					t.Errorf("unexpected method %q", req.Method)
				}
				if len(req.Params) != 1 || string(req.Params[0]) != `"0x`+hex.EncodeToString(raw)+`"` {
					t.Errorf("signed bytes changed: %s", req.Params)
				}
				if wsRequests.Add(1) == 1 && tc.disconnect {
					return ""
				}
				if tc.wsCode != 0 {
					return fmt.Sprintf(`"error":{"code":%d,"message":"request unavailable"}`, tc.wsCode)
				}
				return `"result":"` + hash + `"`
			})
			client, err := ethguard.New(config.Config{ChainID: 1, Upstreams: []config.Upstream{
				{ID: "http", Endpoint: httpPeer.endpoint},
				{ID: "wss", Endpoint: wsPeer.endpoint, Transport: config.TransportWSS},
			}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(client.Close)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result, err := client.SendRawTransaction(ctx, raw)
			d := result.Decision
			if d.Status != tc.want || d.Basis != 2 || d.Required != 2 || len(d.Accepted) != tc.wantAccepted || len(d.Rejected) != tc.wantRejected || len(d.Silent) != tc.wantSilent {
				t.Fatalf("decision=%+v err=%v", d, err)
			}
			if tc.want == rpc.WriteAccepted {
				if err != nil || !result.Accepted() || d.Hash != hash {
					t.Fatalf("accepted result=%+v err=%v", d, err)
				}
			} else if err == nil || result.Accepted() {
				t.Fatalf("non-accepted result=%+v err=%v", d, err)
			}
			if tc.want == rpc.WriteDisputed && !errors.Is(err, rpc.ErrConflict) {
				t.Fatalf("disputed err=%v", err)
			}
			if tc.want == rpc.WriteRejected && !errors.Is(err, rpc.ErrNoValidResponse) {
				t.Fatalf("rejected err=%v", err)
			}
			if httpPeer.writes.Load() != 1 || wsPeer.writes.Load() != 1 || wsPeer.connections.Load() != 1 {
				t.Fatalf("broadcast skipped or replayed source: http=%d ws=%d sockets=%d", httpPeer.writes.Load(), wsPeer.writes.Load(), wsPeer.connections.Load())
			}
			members := client.Pool().VotingMembers()
			if len(members) != 2 || members[1].Name() != "wss" {
				t.Fatalf("membership=%+v", members)
			}
			h := members[1].Health()
			if h.Failures != tc.wantFailure || h.Successes != 1-tc.wantFailure {
				t.Fatalf("write health=%+v", h)
			}
			if tc.wsCode == -32005 && h.RateLimited != 1 {
				t.Fatalf("rate limit=%+v", h)
			}
			if tc.disconnect {
				if len(d.Faults) != 1 || d.Silent[0].Name != "wss" {
					t.Fatalf("unknown delivery evidence lost: %+v", d)
				}
				// The peer received the signed frame. Rejected cannot mean never submitted.
				next, nextErr := client.SendRawTransaction(ctx, raw)
				if next.Decision.Status != rpc.WriteDisputed || !errors.Is(nextErr, rpc.ErrConflict) || wsPeer.writes.Load() != 2 || wsPeer.connections.Load() != 2 {
					t.Fatalf("independent invocation did not redial: %+v err=%v writes=%d sockets=%d", next.Decision, nextErr, wsPeer.writes.Load(), wsPeer.connections.Load())
				}
			}
		})
	}
}

func TestSingleWSSSourceUsesForwardedWriteBasis(t *testing.T) {
	raw, hash := unifiedSignedTransaction(t)
	peer := newSourceWirePeer(t, true, func(req sourceWireRequest) string {
		return `"result":"` + hash + `"`
	})
	client, err := ethguard.New(config.Config{ChainID: 1, Upstreams: []config.Upstream{
		{ID: "wss", Endpoint: peer.endpoint, Transport: config.TransportWSS},
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := client.SendRawTransaction(ctx, raw)
	if err != nil || !result.Accepted() || result.Decision.Hash != hash || result.Decision.Basis != 1 || result.Decision.Required != 1 || client.Mode() != rpc.CorroborationForwarded {
		t.Fatalf("single WSS write=%+v err=%v mode=%s", result.Decision, err, client.Mode())
	}
	if peer.writes.Load() != 1 || peer.connections.Load() != 1 {
		t.Fatalf("single source replay: sends=%d sockets=%d", peer.writes.Load(), peer.connections.Load())
	}
}
