package wss

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/transport"
)

func openNotificationTestStream(t *testing.T, peerURL string, ctx context.Context, buffer int) (*Transport, *NotificationStream) {
	t.Helper()
	tr := dialTestTransport(t, peerURL, 0)
	stream, err := tr.OpenNotificationStream(ctx, "source-a", 7, buffer)
	if err != nil {
		t.Fatalf("open notification stream: %v", err)
	}
	return tr, stream
}

func notificationFrame(subscription, result string) []byte {
	return []byte(`{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":"` + subscription + `","result":` + result + `}}`)
}

func TestNotificationStreamSeparatesNotificationsFromResponses(t *testing.T) {
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		request := <-peer.requests
		peer.writeFrame(notificationFrame("sub-1", `{"number":"0x1"}`))
		peer.respond(request, `"ok"`)
	})
	tr, stream := openNotificationTestStream(t, peerURL, context.Background(), 2)
	var value string
	if err := tr.CallContext(context.Background(), &value, "eth_test"); err != nil || value != "ok" {
		t.Fatalf("call = %q, %v", value, err)
	}
	select {
	case event := <-stream.Events():
		if event.SourceID != "source-a" || event.Generation != 7 || event.Method != "eth_subscription" || event.SubscriptionID != "sub-1" || string(event.Result) != `{"number":"0x1"}` {
			t.Fatalf("event = %+v", event)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("notification was not delivered")
	}
}

func TestNotificationMalformedFrameIsAsynchronousError(t *testing.T) {
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		request := <-peer.requests
		peer.writeFrame([]byte(`{"jsonrpc":"2.0","method":"eth_subscription","params":`))
		peer.respond(request, `"ok"`)
	})
	tr, stream := openNotificationTestStream(t, peerURL, context.Background(), 1)
	var value string
	if err := tr.CallContext(context.Background(), &value, "eth_test"); err != nil {
		t.Fatalf("call = %v", err)
	}
	select {
	case err := <-stream.Err():
		if err == nil {
			t.Fatal("malformed notification closed Err without an error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("malformed notification did not reach Err")
	}
}

func TestNotificationQueueOverflowTerminatesOnlyStream(t *testing.T) {
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		request := <-peer.requests
		peer.writeFrame(notificationFrame("sub-1", `1`))
		peer.writeFrame(notificationFrame("sub-1", `2`))
		peer.respond(request, `"ok"`)
		peer.respond(<-peer.requests, `"ok"`)
	})
	tr, stream := openNotificationTestStream(t, peerURL, context.Background(), 1)
	var value string
	if err := tr.CallContext(context.Background(), &value, "eth_test"); err != nil {
		t.Fatalf("call = %v", err)
	}
	if got := <-stream.Events(); string(got.Result) != "1" {
		t.Fatalf("first event = %s", got.Result)
	}
	select {
	case err := <-stream.Err():
		if !errors.Is(err, ErrNotificationBackpressure) {
			t.Fatalf("overflow err = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("overflow did not terminate stream")
	}
	// The owner remains usable for unary calls after a stream-local overflow.
	if err := tr.CallContext(context.Background(), &value, "eth_followup"); err != nil {
		t.Fatalf("follow-up call after overflow: %v", err)
	}
}

func TestNotificationContextCancellationAndOwnerCloseAreBounded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		// Keep the socket alive until the client closes it after cancelling the
		// stream; returning here would race stream establishment.
		for range peer.requests {
		}
	})
	tr, stream := openNotificationTestStream(t, peerURL, ctx, 1)
	cancel()
	select {
	case <-stream.Err():
	case <-time.After(3 * time.Second):
		t.Fatal("context cancellation did not close Err")
	}
	stream.Unsubscribe()
	stream.Unsubscribe()
	tr.Close()
	select {
	case _, ok := <-stream.Events():
		if ok {
			t.Fatal("events remained open after cancellation")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("events did not close")
	}
}

func TestNotificationPeerDisconnectClosesStreamWithTransportError(t *testing.T) {
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		// Keep the connection alive until the client has opened its
		// notification stream.  The follow-up call below is the explicit
		// trigger for the peer disconnect; closing immediately after the
		// handshake races stream establishment and makes the test flaky.
		<-peer.requests
		_ = peer.conn.Close()
	})
	tr, stream := openNotificationTestStream(t, peerURL, context.Background(), 1)
	var value string
	if err := tr.CallContext(context.Background(), &value, "eth_disconnect"); err == nil {
		t.Fatal("disconnecting call unexpectedly succeeded")
	}
	select {
	case err := <-stream.Err():
		if !errors.Is(err, transport.ErrConnectionClosed) {
			t.Fatalf("disconnect err = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("disconnect did not close Err")
	}
}

func TestDecodeNotificationRequiresSubscriptionPayload(t *testing.T) {
	_, err := decodeNotification(json.RawMessage(`{"jsonrpc":"2.0","method":"eth_subscription","params":{}}`))
	if err == nil {
		t.Fatal("missing subscription payload accepted")
	}
}

func TestSubscribeBindsIDAndUnsubscribesServerSide(t *testing.T) {
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		subscribe := <-peer.requests
		var wire struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(subscribe, &wire); err != nil {
			peer.t.Errorf("decode subscribe: %v", err)
			return
		}
		if wire.Method != "eth_subscribe" || len(wire.Params) != 1 || string(wire.Params[0]) != `"newHeads"` {
			peer.t.Errorf("subscribe request = %s", subscribe)
		}
		peer.respond(subscribe, `"sub-1"`)
		peer.writeFrame(notificationFrame("sub-1", `{"number":"0x2"}`))
		unsubscribe := <-peer.requests
		var unsub struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(unsubscribe, &unsub)
		if unsub.Method != "eth_unsubscribe" {
			peer.t.Errorf("unsubscribe request = %s", unsubscribe)
		}
		peer.respond(unsubscribe, `true`)
	})
	tr := dialTestTransport(t, peerURL, 0)
	sub, err := tr.Subscribe(context.Background(), "newHeads")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	select {
	case event := <-sub.stream.Events():
		if event.SubscriptionID != "sub-1" || string(event.Result) != `{"number":"0x2"}` {
			t.Fatalf("event = %+v", event)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("subscription event was not delivered")
	}
	sub.Unsubscribe()
	sub.Unsubscribe()
}
