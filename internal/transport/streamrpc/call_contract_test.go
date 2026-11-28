package streamrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"testing"

	"github.com/codermaybe/ethguard/internal/rpcwire"
	"github.com/codermaybe/ethguard/rpc"
)

func TestCallDeliversOneRequestAndTheExactResult(t *testing.T) {
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		request := <-peer.requests
		var wire struct {
			Version string            `json:"jsonrpc"`
			Method  string            `json:"method"`
			Params  []json.RawMessage `json:"params"`
			ID      json.RawMessage   `json:"id"`
		}
		if err := json.Unmarshal(request, &wire); err != nil {
			t.Errorf("request is not JSON: %v", err)
			return
		}
		if wire.Version != "2.0" || wire.Method != "eth_getBalance" || len(wire.Params) != 2 {
			t.Errorf("request = %s, want a two-parameter eth_getBalance call", request)
		}
		if len(wire.ID) == 0 || string(wire.ID) == "null" {
			t.Errorf("request id missing: %s", request)
		}
		peer.respond(request, `"0x64"`)
	})
	tr := dialTestTransport(t, peerURL, 0)
	var raw json.RawMessage
	if err := tr.Call(context.Background(), "eth_getBalance", "0x1", "latest").Decode(&raw); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if string(raw) != `"0x64"` {
		t.Fatalf("result = %s, want the raw result bytes", raw)
	}
	if pending := tr.pendingCount(); pending != 0 {
		t.Fatalf("pending after a completed call = %d, want 0", pending)
	}
}

func TestResultTargetContract(t *testing.T) {
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		for i := 0; i < 2; i++ {
			peer.respond(<-peer.requests, `{"a":1}`)
		}
	})
	tr := dialTestTransport(t, peerURL, 0)
	if err := tr.Call(context.Background(), "eth_test").Decode(nil); err != nil {
		t.Fatalf("nil result target: %v", err)
	}
	if err := tr.Call(context.Background(), "eth_test").Decode(map[string]any{}); err == nil {
		t.Fatal("non-pointer result target must fail")
	}
}

func TestProtocolErrorsKeepErrorDataPresence(t *testing.T) {
	cases := []struct {
		name string
		data string
		want rpc.ErrorDataPresenceState
		raw  string
	}{
		{name: "missing", data: ``, want: rpc.ErrorDataPresenceMissing},
		{name: "null", data: `,"data":null`, want: rpc.ErrorDataPresenceNull, raw: "null"},
		{name: "empty-data", data: `,"data":"0x"`, want: rpc.ErrorDataPresenceValue, raw: `"0x"`},
		{name: "concrete-data", data: `,"data":"0xdeadbeef"`, want: rpc.ErrorDataPresenceValue, raw: `"0xdeadbeef"`},
		{name: "object-data", data: `,"data":{"inner":1}`, want: rpc.ErrorDataPresenceValue, raw: `{"inner":1}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
				peer.respondError(<-peer.requests, 3, "execution reverted", tc.data)
			})
			tr := dialTestTransport(t, peerURL, 0)
			var raw json.RawMessage
			err := tr.Call(context.Background(), "eth_call").Decode(&raw)
			var protocol rpc.ProtocolError
			if !errors.As(err, &protocol) || protocol.ErrorCode() != 3 {
				t.Fatalf("err = %v, want the protocol error capability", err)
			}
			var dataErr rpc.DataError
			if !errors.As(err, &dataErr) {
				t.Fatalf("err = %v, lost the DataError capability", err)
			}
			var presence rpc.ErrorDataPresence
			if !errors.As(err, &presence) {
				t.Fatalf("err = %v, lost the presence capability", err)
			}
			rawData, present := presence.ErrorDataJSON()
			wanted := tc.want != rpc.ErrorDataPresenceMissing
			if present != wanted {
				t.Fatalf("present = %v, want %v", present, wanted)
			}
			if wanted && string(rawData) != tc.raw {
				t.Fatalf("raw data = %s, want %s", rawData, tc.raw)
			}
		})
	}
}

func TestMalformedFrameFailsTheCallAndKeepsTheConnection(t *testing.T) {
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		first := <-peer.requests
		peer.writeFrame([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":"ok"} trailing`, requestID(first))))
		second := <-peer.requests
		peer.respond(second, `"ok"`)
	})
	tr := dialTestTransport(t, peerURL, 0)
	var raw json.RawMessage
	if err := tr.Call(context.Background(), "eth_test").Decode(&raw); !errors.Is(err, rpcwire.ErrMalformedResponse) {
		t.Fatalf("err = %v, want the malformed-response sentinel", err)
	}
	if err := tr.Call(context.Background(), "eth_test").Decode(&raw); err != nil {
		t.Fatalf("call after malformed frame: %v", err)
	}
	if string(raw) != `"ok"` {
		t.Fatalf("second result = %s, want the frame's result", raw)
	}
}

func TestOutOfOrderResponsesAreDemultiplexed(t *testing.T) {
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		first := <-peer.requests
		second := <-peer.requests
		peer.respond(second, `"second"`)
		peer.respond(first, `"first"`)
	})
	tr := dialTestTransport(t, peerURL, 0)
	type outcome struct {
		value string
		err   error
	}
	results := make(chan outcome, 2)
	for i := 0; i < 2; i++ {
		go func() {
			var value string
			err := tr.Call(context.Background(), "eth_test").Decode(&value)
			results <- outcome{value: value, err: err}
		}()
	}
	seen := make(map[string]bool)
	for i := 0; i < 2; i++ {
		result := <-results
		if result.err != nil {
			t.Fatalf("call failed: %v", result.err)
		}
		seen[result.value] = true
	}
	if !seen["first"] || !seen["second"] {
		t.Fatalf("results = %v, want both answers", seen)
	}
}

func TestNotificationsAreIgnored(t *testing.T) {
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		request := <-peer.requests
		peer.writeFrame([]byte(`{"jsonrpc":"2.0","method":"eth_subscription","params":{}}`))
		peer.respond(request, `"ok"`)
	})
	tr := dialTestTransport(t, peerURL, 0)
	var value string
	if err := tr.Call(context.Background(), "eth_test").Decode(&value); err != nil {
		t.Fatalf("call after notification: %v", err)
	}
	if value != "ok" {
		t.Fatalf("value = %q, want ok", value)
	}
}

func TestChainID(t *testing.T) {
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		peer.respond(<-peer.requests, `"0x1"`)
	})
	tr := dialTestTransport(t, peerURL, 0)
	id, err := tr.ChainID(context.Background())
	if err != nil {
		t.Fatalf("ChainID: %v", err)
	}
	if id.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("chain id = %s, want 1", id)
	}
}

func TestCanceledContextSendsNoRequest(t *testing.T) {
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		request := <-peer.requests
		if method := requestMethod(request); method != "eth_followup" {
			t.Errorf("request method = %q, want only eth_followup", method)
		}
		peer.respond(request, `"ok"`)
	})
	tr := dialTestTransport(t, peerURL, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var value string
	if err := tr.Call(ctx, "eth_canceled").Decode(&value); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if pending := tr.pendingCount(); pending != 0 {
		t.Fatalf("pending after a rejected call = %d, want 0", pending)
	}
	if err := tr.Call(context.Background(), "eth_followup").Decode(&value); err != nil {
		t.Fatalf("call after cancellation: %v", err)
	}
}

func TestUnknownResponseIDIsIgnoredAndConnectionStaysUsable(t *testing.T) {
	t.Run("never-issued id", func(t *testing.T) {
		// The peer holds the connection open until the test releases it:
		// a serve function that returns mid-assertion closes the socket, and
		// the read loop's abort then terminates the stream — the documented
		// disconnect behavior, not a response-frame misroute.
		releasePeer := make(chan struct{})
		defer close(releasePeer)
		peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
			// Push a response for an ID the transport never registered,
			// before any call exists.
			peer.writeFrame([]byte(`{"jsonrpc":"2.0","id":4242,"result":"0xstray"}`))
			peer.respond(<-peer.requests, `"0x01"`)
			select {
			case <-peer.requests:
			case <-releasePeer:
			}
		})
		tr := dialTestTransport(t, peerURL, 0)
		stream, err := tr.OpenNotificationStream(context.Background(), "src", 1, 8)
		if err != nil {
			t.Fatalf("open stream: %v", err)
		}
		defer stream.Unsubscribe()

		var raw json.RawMessage
		if err := tr.Call(context.Background(), "eth_blockNumber").Decode(&raw); err != nil {
			t.Fatalf("call after stray response: %v", err)
		}
		if string(raw) != `"0x01"` {
			t.Fatalf("result = %s, want the real call's answer", raw)
		}
		if pending := tr.pendingCount(); pending != 0 {
			t.Fatalf("pending = %d, want 0", pending)
		}
		select {
		case notification := <-stream.Events():
			t.Fatalf("a response frame leaked into the notification stream: %+v", notification)
		default:
		}
	})

	t.Run("duplicate reply for a completed call", func(t *testing.T) {
		releasePeer := make(chan struct{})
		defer close(releasePeer)
		peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
			first := <-peer.requests
			peer.respond(first, `"first"`)
			// Re-send the same ID's answer after the call already consumed it.
			peer.respond(first, `"duplicate"`)
			peer.respond(<-peer.requests, `"second"`)
			select {
			case <-peer.requests:
			case <-releasePeer:
			}
		})
		tr := dialTestTransport(t, peerURL, 0)
		var first json.RawMessage
		if err := tr.Call(context.Background(), "eth_test").Decode(&first); err != nil {
			t.Fatalf("first call: %v", err)
		}
		if string(first) != `"first"` {
			t.Fatalf("first result = %s", first)
		}
		var second json.RawMessage
		if err := tr.Call(context.Background(), "eth_test").Decode(&second); err != nil {
			t.Fatalf("call after duplicate reply: %v", err)
		}
		if string(second) != `"second"` {
			t.Fatalf("second result = %s, want the second call's own answer", second)
		}
		if pending := tr.pendingCount(); pending != 0 {
			t.Fatalf("pending = %d, want 0", pending)
		}
	})
}
