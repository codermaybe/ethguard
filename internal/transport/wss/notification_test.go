package wss

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/rpcwire"
	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/rpc"
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
	if err := tr.Call(context.Background(), "eth_test").Decode(&value); err != nil || value != "ok" {
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
	if err := tr.Call(context.Background(), "eth_test").Decode(&value); err != nil {
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
	if err := tr.Call(context.Background(), "eth_test").Decode(&value); err != nil {
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
	if err := tr.Call(context.Background(), "eth_followup").Decode(&value); err != nil {
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
	if err := tr.Call(context.Background(), "eth_disconnect").Decode(&value); err == nil {
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
func TestSubscribeCancellationClosesTransportBeforeLateResponse(t *testing.T) {
	requestSeen := make(chan struct{})
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		request := <-peer.requests
		close(requestSeen)
		time.Sleep(100 * time.Millisecond)
		peer.respond(request, `"late-subscription"`)
	})
	tr := dialTestTransport(t, peerURL, 0)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := tr.Subscribe(ctx, "newHeads")
		done <- err
	}()
	<-requestSeen
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) || !errors.Is(err, transport.ErrConnectionClosed) {
		t.Fatalf("canceled setup did not close its uncertain transport: %v", err)
	}
	var value string
	if err := tr.Call(t.Context(), "eth_after").Decode(&value); !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("canceled setup socket was reused: %v", err)
	}
}

func TestSubscribeCancellationBeforeWritePreservesOtherStreams(t *testing.T) {
	for _, waiting := range []bool{false, true} {
		t.Run(fmt.Sprintf("waiting_for_writer=%t", waiting), func(t *testing.T) {
			peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
				for request := range peer.requests {
					var wire struct {
						Method string `json:"method"`
					}
					if err := json.Unmarshal(request, &wire); err != nil {
						t.Error(err)
						return
					}
					if wire.Method != "eth_followup" {
						t.Errorf("canceled setup wrote a request: %s", request)
						return
					}
					peer.respond(request, `"ok"`)
					peer.writeFrame(notificationFrame("existing", `"kept"`))
				}
			})
			tr := dialTestTransport(t, peerURL, 0)
			stream, err := tr.openNotificationStreamWithID(t.Context(), "source", 1, 1, "existing")
			if err != nil {
				t.Fatal(err)
			}
			if !stream.bindSubscriptionID("existing") {
				t.Fatal("bind existing stream")
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var releaseWriter func()
			if waiting {
				releaseWriter = tr.ws.writeControlLock()
			} else {
				cancel()
			}
			done := make(chan error, 1)
			go func() {
				_, err := tr.Subscribe(ctx, "newHeads")
				done <- err
			}()
			if waiting {
				deadline := time.Now().Add(time.Second)
				for tr.pendingCount() == 0 && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				if tr.pendingCount() == 0 {
					releaseWriter()
					t.Fatal("setup never reached the writer")
				}
				cancel()
			}
			err = lifecycleResult(t, done)
			if releaseWriter != nil {
				releaseWriter()
			}
			if !errors.Is(err, context.Canceled) || errors.Is(err, transport.ErrConnectionClosed) {
				t.Fatalf("unsent setup cancellation = %v", err)
			}
			followCtx, followCancel := context.WithTimeout(t.Context(), time.Second)
			defer followCancel()
			var value string
			if err := tr.Call(followCtx, "eth_followup").Decode(&value); err != nil || value != "ok" {
				t.Fatalf("shared connection = %q, %v", value, err)
			}
			select {
			case event, ok := <-stream.Events():
				if !ok || string(event.Result) != `"kept"` {
					t.Fatalf("existing stream was lost: %+v, %t", event, ok)
				}
			case <-followCtx.Done():
				t.Fatal("existing stream stopped delivering")
			}
		})
	}
}

func TestCanceledSubscribeUsesAssociatedAnswerForCleanup(t *testing.T) {
	for _, test := range []struct {
		name       string
		answer     string
		wantRetire bool
	}{
		{name: "protocol_rejection", answer: `"error":{"code":-32601,"message":"unsupported"}`},
		{name: "created_subscription", answer: `"result":"created"`, wantRetire: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
				if test.wantRetire {
					request := <-peer.requests
					var wire struct {
						Method string   `json:"method"`
						Params []string `json:"params"`
					}
					if err := json.Unmarshal(request, &wire); err != nil || wire.Method != "eth_unsubscribe" || len(wire.Params) != 1 || wire.Params[0] != "created" {
						t.Errorf("subscription cleanup = %s, %v", request, err)
						return
					}
					peer.respond(request, `true`)
				}
				request := <-peer.requests
				if requestMethod(request) != "eth_followup" {
					t.Errorf("unexpected cleanup request: %s", request)
					return
				}
				peer.respond(request, `"ok"`)
				peer.writeFrame(notificationFrame("existing", `"kept"`))
				for range peer.requests {
				}
			})
			tr := dialTestTransport(t, peerURL, 0)
			stream, err := tr.openNotificationStreamWithID(t.Context(), "source", 1, 1, "existing")
			if err != nil {
				t.Fatal(err)
			}
			if !stream.bindSubscriptionID("existing") {
				t.Fatal("bind existing stream")
			}
			// Association can win before cancellation wins final delivery. The
			// complete answer remains evidence although Decode returns canceled.
			response := rpcwire.CompleteCall([]byte(`{"jsonrpc":"2.0","id":1,`+test.answer+`}`), json.RawMessage(`1`), rpc.Exchange{Progress: rpc.ExchangeCompleted, Cause: context.Canceled})
			var id string
			cause := response.Decode(&id)
			if !errors.Is(cause, context.Canceled) || id != "" {
				t.Fatalf("canceled response projection = %q, %v", id, cause)
			}
			if err := tr.cancelSubscribeSetup(response, true, cause); !errors.Is(err, context.Canceled) || errors.Is(err, transport.ErrConnectionClosed) {
				t.Fatalf("associated answer cleanup = %v", err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			var value string
			if err := tr.Call(ctx, "eth_followup").Decode(&value); err != nil || value != "ok" {
				t.Fatalf("shared connection = %q, %v", value, err)
			}
			select {
			case event, ok := <-stream.Events():
				if !ok || string(event.Result) != `"kept"` {
					t.Fatalf("existing stream was lost: %+v, %t", event, ok)
				}
			case <-ctx.Done():
				t.Fatal("existing stream stopped delivering")
			}
		})
	}
}

func TestConcurrentSubscriptionsIgnoreUnrelatedSetupNotifications(t *testing.T) {
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		firstRequest := <-peer.requests
		peer.respond(firstRequest, `"sub-1"`)

		secondRequest := <-peer.requests
		for i := range 300 {
			peer.writeFrame(notificationFrame("sub-1", fmt.Sprintf(`{"number":%d}`, i)))
		}
		peer.respond(secondRequest, `"sub-2"`)
		peer.writeFrame(notificationFrame("sub-2", `{"number":999}`))

		for range 2 {
			request := <-peer.requests
			peer.respond(request, `true`)
		}
	})
	tr := dialTestTransport(t, peerURL, 0)
	first, err := tr.Subscribe(context.Background(), "newHeads")
	if err != nil {
		t.Fatalf("first Subscribe: %v", err)
	}
	drained := make(chan struct{})
	go func() {
		for range first.stream.Events() {
		}
		close(drained)
	}()

	second, err := tr.Subscribe(context.Background(), "logs")
	if err != nil {
		t.Fatalf("second Subscribe after unrelated notifications: %v", err)
	}
	select {
	case event := <-second.stream.Events():
		if event.SubscriptionID != "sub-2" || string(event.Result) != `{"number":999}` {
			t.Fatalf("second event = %+v", event)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("second subscription did not receive its notification")
	}

	second.Unsubscribe()
	first.Unsubscribe()
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("first subscription stream did not close")
	}
}
