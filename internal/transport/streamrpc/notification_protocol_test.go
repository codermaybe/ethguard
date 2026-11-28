package streamrpc

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/rpcwire"
	"github.com/codermaybe/ethguard/internal/transport"
)

func TestInvalidNotificationVersionDrainsStreamAndPreservesUnary(t *testing.T) {
	for _, tc := range []struct {
		name, frame string
	}{
		{"missing", `{"method":"eth_subscription","params":{"subscription":"sub-1","result":"invalid"}}`},
		{"wrong", `{"jsonrpc":"1.0","method":"eth_subscription","params":{"subscription":"sub-1","result":"invalid"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
				request := <-peer.requests
				peer.writeFrame(notificationFrame("sub-1", `"admitted"`))
				peer.writeFrame([]byte(tc.frame))
				peer.respond(request, `"ok"`)
				for request := range peer.requests {
					peer.respond(request, `"ok"`)
				}
			})
			tr := dialTestTransport(t, peerURL, 0)
			budget := &notificationBudget{}
			stream, err := tr.openNotificationStreamPolicy(ctx, "source-a", 7, transport.NotificationPolicy{
				Messages: 2, Bytes: 4096, MaxMessageBytes: 1024, Reserve: budget.reserve,
			}, "")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(stream.Unsubscribe)
			var value string
			if err := tr.Call(ctx, "eth_test").Decode(&value); err != nil || value != "ok" {
				t.Fatalf("call during malformed notification = %q, %v", value, err)
			}
			select {
			case err := <-stream.Err():
				t.Fatalf("terminal error arrived before the admitted event drained: %v", err)
			default:
			}

			var admitted Notification
			select {
			case event, ok := <-stream.Events():
				if !ok || string(event.Result) != `"admitted"` || event.Release == nil {
					t.Fatalf("admitted event was not drained: event=%+v, open=%t", event, ok)
				}
				admitted = event
				t.Cleanup(admitted.Release)
			case <-ctx.Done():
				t.Fatal("admitted event did not drain")
			}
			select {
			case event, ok := <-stream.Events():
				if ok {
					event.Release()
					t.Fatalf("invalid notification reached Events: %s", event.Raw)
				}
			case <-ctx.Done():
				t.Fatal("malformed notification did not close Events after draining")
			}
			select {
			case err := <-stream.Err():
				if !errors.Is(err, rpcwire.ErrMalformedNotification) {
					t.Fatalf("malformed notification lost its wire error category: %v", err)
				}
			case <-ctx.Done():
				t.Fatal("malformed notification did not deliver its terminal error")
			}
			if budget.bytes() == 0 {
				t.Fatal("stream failure released the receiver's admitted event")
			}
			admitted.Release()
			admitted.Release()
			if used := budget.bytes(); used != 0 {
				t.Fatalf("stream failure retained %d bytes after receiver release", used)
			}
			if err := tr.Call(ctx, "eth_followup").Decode(&value); err != nil || value != "ok" {
				t.Fatalf("call after malformed notification = %q, %v", value, err)
			}
		})
	}
}

func TestNotificationPreservesNullAndOriginalBytes(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	const first = " {\n  \"jsonrpc\": \"2.0\", \"method\": \"eth_subscription\", \"params\": {\"subscription\": \"sub-1\", \"result\": null}\n} "
	const result = `{ "number" : "0x2", "extra" : [null, true] }`
	const second = `{"jsonrpc":"2.\u0030","method":"eth_subscription","params":{"subscription":"sub-1","result":` + result + `}}`
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		request := <-peer.requests
		peer.writeFrame([]byte(first))
		peer.respond(request, `"ok"`)
		request = <-peer.requests
		peer.writeFrame([]byte(second))
		peer.respond(request, `"ok"`)
		for range peer.requests {
		}
	})
	tr, stream := openNotificationTestStream(t, peerURL, ctx, 2)
	t.Cleanup(stream.Unsubscribe)
	var retained []Notification
	for i, want := range []struct{ raw, result string }{{first, "null"}, {second, result}} {
		if err := tr.Call(ctx, "eth_test").Decode(nil); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		select {
		case event, ok := <-stream.Events():
			if !ok {
				t.Fatal("valid notification terminated stream")
			}
			if event.Release == nil {
				t.Fatal("notification lacks its release lease")
			}
			t.Cleanup(event.Release)
			if string(event.Raw) != want.raw || string(event.Result) != want.result {
				t.Fatalf("notification %d changed raw bytes: Raw=%q Result=%q", i, event.Raw, event.Result)
			}
			if event.SourceID != "source-a" || event.Generation != 7 || event.SubscriptionID != "sub-1" || event.Method != "eth_subscription" {
				t.Fatalf("notification %d lost its identity: %+v", i, event)
			}
			retained = append(retained, event)
		case <-ctx.Done():
			t.Fatalf("notification %d was not delivered", i)
		}
	}
	if string(retained[0].Raw) != first || string(retained[0].Result) != "null" {
		t.Fatal("a later message overwrote retained notification bytes")
	}
	for i := range retained[1].Raw {
		retained[1].Raw[i] = ' '
	}
	if string(retained[1].Result) != result {
		t.Fatal("Result aliases the original frame buffer")
	}
}

func TestInvalidNotificationDuringSubscribeReleasesPending(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		request := <-peer.requests
		peer.writeFrame(notificationFrame("sub-1", `"before-bind"`))
		peer.writeFrame([]byte(`{"jsonrpc":"1.0","method":"eth_subscription","params":{"subscription":"sub-1","result":"invalid"}}`))
		peer.respond(request, `"sub-1"`)
		request = <-peer.requests
		var cleanup struct {
			Method string   `json:"method"`
			Params []string `json:"params"`
		}
		if err := json.Unmarshal(request, &cleanup); err != nil || cleanup.Method != "eth_unsubscribe" || len(cleanup.Params) != 1 || cleanup.Params[0] != "sub-1" {
			t.Errorf("failed setup did not retire its server subscription: request=%s err=%v", request, err)
			return
		}
		peer.respond(request, `true`)
		for request := range peer.requests {
			peer.respond(request, `"ok"`)
		}
	})
	tr := dialTestTransport(t, peerURL, 0)
	budget := &notificationBudget{}
	sub, err := tr.SubscribeBounded(ctx, transport.NotificationPolicy{
		Messages: 2, Bytes: 4096, MaxMessageBytes: 1024, Reserve: budget.reserve,
	}, "newHeads")
	if err == nil {
		if sub != nil {
			sub.Unsubscribe()
		}
		t.Fatalf("malformed notification allowed a successful setup: subscription=%v err=%v", sub, err)
	}
	if !errors.Is(err, rpcwire.ErrMalformedNotification) {
		t.Fatalf("failed setup lost its wire error category: %v", err)
	}
	tr.mu.Lock()
	messages, bytes, entries := tr.pendingMessages, tr.pendingBytes, len(tr.pendingNotifications)
	tr.mu.Unlock()
	if messages != 0 || bytes != 0 || entries != 0 || budget.bytes() != 0 {
		t.Fatalf("failed setup retained pending notifications: messages=%d bytes=%d entries=%d reserved=%d", messages, bytes, entries, budget.bytes())
	}
	var value string
	if err := tr.Call(ctx, "eth_followup").Decode(&value); err != nil || value != "ok" {
		t.Fatalf("call after failed setup = %q, %v", value, err)
	}
}
