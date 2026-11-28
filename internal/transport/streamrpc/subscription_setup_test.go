package streamrpc

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

func TestDecodeNotificationRequiresSubscriptionPayload(t *testing.T) {
	_, err := decodeNotification(json.RawMessage(`{"jsonrpc":"2.0","method":"eth_subscription","params":{}}`))
	if err == nil {
		t.Fatal("missing subscription payload accepted")
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
				releaseWriter = tr.conn.(*scriptedConn).holdWriter()
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
