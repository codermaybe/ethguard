package ethguard_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/internal/testkit/wstest"
	"github.com/codermaybe/ethguard/rpc"
)

type sourceWireRequest struct {
	ID     json.RawMessage   `json:"id"`
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
}

type sourceWirePeer struct {
	endpoint        string
	connections     atomic.Int32 // accepted WebSocket connections
	httpConnections atomic.Int32 // accepted native HTTP TCP connections
	requests        atomic.Int32 // all decoded JSON-RPC requests/frames
	chainIDRequests atomic.Int32 // eth_chainId requests, including identity probes
	headerRequests  atomic.Int32 // eth_getBlockByNumber requests
	writes          atomic.Int32
}

func newSourceWirePeer(t *testing.T, ws bool, answer func(sourceWireRequest) string) *sourceWirePeer {
	t.Helper()
	peer := &sourceWirePeer{}
	respond := func(body []byte) []byte {
		peer.requests.Add(1)
		var req sourceWireRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("decode: %v", err)
			return nil
		}
		switch req.Method {
		case "eth_chainId":
			peer.chainIDRequests.Add(1)
		case "eth_getBlockByNumber":
			peer.headerRequests.Add(1)
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
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ws {
			conn, err := wstest.Upgrade(t, w, r)
			if err != nil {
				t.Errorf("upgrade: %v", err)
				return
			}
			defer conn.Close() //nolint:errcheck // Socket cleanup has no pending output; disconnects are expected at fixture teardown.
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
	// ConnState observes accepted TCP connections, rather than pretending each
	// HTTP handler invocation is a new connection. Native HTTP keep-alive can
	// serve several requests on one connection, so request and connection
	// evidence stay in separate counters.
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if !ws && state == http.StateNew {
			peer.httpConnections.Add(1)
		}
	}
	srv.Start()
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
		name            string
		httpReject      bool
		wsCode          int
		disconnect      bool
		want            rpc.WriteStatus
		wantAccepted    int
		wantRejected    int
		wantUnavailable int
		wantFailure     uint64
	}{
		{name: "mixed acceptance", want: rpc.WriteAccepted, wantAccepted: 2},
		{name: "unsupported reduces support not basis", wsCode: -32601, want: rpc.WriteUnconfirmed, wantAccepted: 1, wantRejected: 1},
		{name: "server rejection affects health", wsCode: -32603, want: rpc.WriteUnconfirmed, wantAccepted: 1, wantRejected: 1, wantFailure: 1},
		{name: "rate limit affects health", wsCode: -32005, want: rpc.WriteUnconfirmed, wantAccepted: 1, wantRejected: 1, wantFailure: 1},
		{name: "rejection coexists with unknown submission", httpReject: true, disconnect: true, want: rpc.WriteUnconfirmed, wantRejected: 1, wantUnavailable: 1, wantFailure: 1},
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
			d := result.WriteDecision
			if d.Status != tc.want || d.Adjudication.Basis != 2 || d.Adjudication.Required != 2 || len(writeRelations(d.Sources, rpc.VerdictSupported)) != tc.wantAccepted || len(writeRejections(d.Sources)) != tc.wantRejected || len(writeUnavailable(d.Sources)) != tc.wantUnavailable {
				t.Fatalf("decision=%+v err=%v", d, err)
			}
			if tc.want == rpc.WriteAccepted {
				if err != nil || !result.IsWriteAccepted() || d.Hash != hash {
					t.Fatalf("accepted result=%+v err=%v", d, err)
				}
			} else if err == nil || result.IsWriteAccepted() {
				t.Fatalf("non-accepted result=%+v err=%v", d, err)
			}
			if tc.want == rpc.WriteUnconfirmed {
				wantErr, wantReason := rpc.ErrInsufficientSupport, rpc.ReasonInsufficientSupport
				if tc.httpReject {
					wantErr, wantReason = rpc.ErrNoValidResponse, rpc.ReasonRejected
				}
				if !errors.Is(err, wantErr) || d.Adjudication.Reason != wantReason {
					t.Fatalf("unconfirmed reason=%v err=%v", d.Adjudication.Reason, err)
				}
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
				if len(d.Faults) != 0 || writeUnavailable(d.Sources)[0].Exchange.Cause == nil || writeUnavailable(d.Sources)[0].Source.Name != "wss" {
					t.Fatalf("unknown delivery evidence lost: %+v", d)
				}
				// The peer received the signed frame. Rejected cannot mean never submitted.
				next, nextErr := client.SendRawTransaction(ctx, raw)
				if next.WriteDecision.Status != rpc.WriteUnconfirmed || !errors.Is(nextErr, rpc.ErrInsufficientSupport) || wsPeer.writes.Load() != 2 || wsPeer.connections.Load() != 2 {
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
	if err != nil || !result.IsWriteAccepted() || result.WriteDecision.Hash != hash || result.WriteDecision.Adjudication.Basis != 1 || result.WriteDecision.Adjudication.Required != 1 || client.Mode() != rpc.CorroborationForwarded {
		t.Fatalf("single WSS write=%+v err=%v mode=%s", result.Decision, err, client.Mode())
	}
	if peer.writes.Load() != 1 || peer.connections.Load() != 1 {
		t.Fatalf("single source replay: sends=%d sockets=%d", peer.writes.Load(), peer.connections.Load())
	}
}

func TestWriteRejectionDataAcrossEntrypoints(t *testing.T) {
	for _, tc := range []struct {
		name     string
		data     string
		presence rpc.ErrorDataPresenceState
	}{
		{"missing", "", rpc.ErrorDataPresenceMissing},
		{"null", "null", rpc.ErrorDataPresenceNull},
		{"empty string", `""`, rpc.ErrorDataPresenceValue},
		{"empty bytes", `"0x"`, rpc.ErrorDataPresenceValue},
		{"empty object", `{}`, rpc.ErrorDataPresenceValue},
		{"empty array", `[]`, rpc.ErrorDataPresenceValue},
		{"value", `"0xdead"`, rpc.ErrorDataPresenceValue},
		{"large integer", `{ "n": 9007199254740993, "v": [1e+09, 123456789012345678901234567890] }`, rpc.ErrorDataPresenceValue},
	} {
		for _, entry := range []string{"root raw", "root typed", "view typed"} {
			t.Run(tc.name+"/"+entry, func(t *testing.T) {
				raw, _ := unifiedSignedTransaction(t)
				tx := new(types.Transaction)
				if err := tx.UnmarshalBinary(raw); err != nil {
					t.Fatal(err)
				}
				answer := func(req sourceWireRequest) string {
					if req.Method != "eth_sendRawTransaction" || len(req.Params) != 1 || string(req.Params[0]) != `"0x`+hex.EncodeToString(raw)+`"` {
						t.Errorf("unexpected write: %+v", req)
					}
					data := ""
					if tc.data != "" {
						data = `,"data":` + tc.data
					}
					return `"error":{"code":-32601,"message":"already known"` + data + `}`
				}
				httpPeer := newSourceWirePeer(t, false, answer)
				wsPeer := newSourceWirePeer(t, true, answer)
				client, err := ethguard.New(config.Config{ChainID: 1, Upstreams: []config.Upstream{
					{ID: "http", Endpoint: httpPeer.endpoint},
					{ID: "wss", Endpoint: wsPeer.endpoint, Transport: config.TransportWSS},
				}})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(client.Close)
				base, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				ctx, capture := ethguard.WithResult(base)
				var result ethguard.CallResult
				switch entry {
				case "root raw":
					result, err = client.SendRawTransaction(ctx, raw)
				case "root typed":
					result, err = client.SendTransaction(ctx, tx)
				case "view typed":
					err = client.EthClient().SendTransaction(ctx, tx)
				}
				if !errors.Is(err, rpc.ErrNoValidResponse) {
					t.Fatalf("error=%v, want rejected", err)
				}
				check := func(result ethguard.CallResult) {
					t.Helper()
					d := result.WriteDecision
					if result.Decision != nil || result.Err != err || d == nil { //nolint:errorlint // The result must retain the exact error returned to the caller.
						t.Fatalf("result/error contract changed: %+v, err=%v", result, err)
					}
					if d.Status != rpc.WriteUnconfirmed || d.Adjudication.Basis != 2 || d.Adjudication.Required != 2 || d.Hash != "" || len(writeRelations(d.Sources, rpc.VerdictSupported)) != 0 || len(writeRelations(d.Sources, rpc.VerdictDissented)) != 0 || len(writeUnavailable(d.Sources)) != 0 || len(d.Faults) != 0 || len(writeRejections(d.Sources)) != 2 {
						t.Fatalf("decision=%+v", d)
					}
					seen := make(map[string]bool)
					for _, rejected := range writeRejections(d.Sources) {
						seen[rejected.Source.Name] = true
						r := rejected.Rejection
						if rejected.Hash != "" || r == nil || r.Code != -32601 || r.Message != "already known" {
							t.Fatalf("rejection=%+v", rejected)
						}
						if r.DataPresence != tc.presence {
							t.Errorf("source %s presence=%s, want %s", rejected.Source.Name, r.DataPresence, tc.presence)
						}
						if string(r.Data) != tc.data {
							t.Errorf("source %s data=%q, want %q", rejected.Source.Name, r.Data, tc.data)
						}
					}
					if !seen["http"] || !seen["wss"] {
						t.Fatalf("source identities=%v", seen)
					}
				}
				captured, published, captureErr := capture.Result()
				if !published || captureErr != nil {
					t.Fatalf("capture=%t, %v", published, captureErr)
				}
				check(captured)
				if t.Failed() {
					return
				}
				if entry != "view typed" {
					check(result)
					writeRejections(result.WriteDecision.Sources)[0].Rejection.Message = "mutated"
					if tc.data != "" {
						writeRejections(result.WriteDecision.Sources)[0].Rejection.Data[0] = '!'
					}
					check(captured)
				}
				if tc.data != "" {
					writeRejections(captured.WriteDecision.Sources)[0].Rejection.Data[0] = '!'
					if string(writeRejections(captured.WriteDecision.Sources)[1].Rejection.Data) != tc.data {
						t.Fatal("sources share data storage")
					}
				}
				writeRejections(captured.WriteDecision.Sources)[0].Rejection.Message = "mutated"
				again, published, captureErr := capture.Result()
				if !published || captureErr != nil {
					t.Fatalf("repeat capture=%t, %v", published, captureErr)
				}
				check(again)
				if httpPeer.writes.Load() != 1 || wsPeer.writes.Load() != 1 || wsPeer.connections.Load() != 1 {
					t.Fatalf("write skipped or replayed: http=%d ws=%d sockets=%d", httpPeer.writes.Load(), wsPeer.writes.Load(), wsPeer.connections.Load())
				}
				for _, source := range client.Pool().VotingMembers() {
					if h := source.Health(); h.Failures != 0 || h.Successes != 1 {
						t.Fatalf("source %s health=%+v", source.Name(), h)
					}
				}
			})
		}
	}
}

// These assertion helpers query the authoritative source evidence without
// storing a second classification in the decision.
func writeRelations(sources []rpc.SourceAnswer, relation rpc.VerdictKind) []rpc.SourceAnswer {
	var matches []rpc.SourceAnswer
	for _, source := range sources {
		if source.Relation == relation {
			matches = append(matches, source)
		}
	}
	return matches
}
func writeRejections(sources []rpc.SourceAnswer) []rpc.SourceAnswer {
	var matches []rpc.SourceAnswer
	for _, source := range sources {
		if source.Rejection != nil {
			matches = append(matches, source)
		}
	}
	return matches
}
func writeUnavailable(sources []rpc.SourceAnswer) []rpc.SourceAnswer {
	var matches []rpc.SourceAnswer
	for _, source := range sources {
		if source.Answer.Kind == rpc.AnswerUnassessed {
			matches = append(matches, source)
		}
	}
	return matches
}
