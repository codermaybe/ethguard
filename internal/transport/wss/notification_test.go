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
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {})
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
		_ = peer.conn.Close()
	})
	_, stream := openNotificationTestStream(t, peerURL, context.Background(), 1)
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
