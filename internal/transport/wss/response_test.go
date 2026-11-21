package wss

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/rpc"
)

func TestResponseOnlyTracksTheCurrentRequest(t *testing.T) {
	peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
		<-peer.requests
		peer.writeFrame([]byte(`{"jsonrpc":"2.0","id":999999,"result":"unrelated"}`))
		peer.writeFrame(notificationFrame("sub-1", `1`))
		peer.respond(<-peer.requests, `null`)
	})
	tr, stream := openNotificationTestStream(t, peerURL, t.Context(), 2)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan rpc.Response, 1)
	go func() { done <- tr.Call(ctx, "eth_wait") }()
	select {
	case <-stream.Events():
	case <-time.After(3 * time.Second):
		t.Fatal("notification not processed")
	}
	cancel()
	response := <-done
	if !errors.Is(response.Err(), context.Canceled) || response.Exchange.Progress != rpc.ExchangeStarted || response.Answer.Kind != rpc.AnswerUnassessed {
		t.Fatalf("unrelated traffic became an answer: %+v", response)
	}
	followup := tr.Call(t.Context(), "eth_followup")
	if followup.Err() != nil || followup.Exchange.Progress != rpc.ExchangeCompleted || followup.Answer.Kind != rpc.AnswerResult || string(followup.Answer.Result) != "null" {
		t.Fatalf("socket after cancellation: %+v", followup)
	}
}

func TestResponseRetainsProtocolAndInvalidAnswerKinds(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		kind          rpc.AnswerKind
	}{
		{"null", `"result":null`, rpc.AnswerResult},
		{"protocol", `"error":{"code":-32000,"message":"rejected","data":null}`, rpc.AnswerProtocolError},
		{"invalid", `"error":null`, rpc.AnswerInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			peerURL := newScriptedPeer(t, func(peer *scriptedPeer) {
				req := <-peer.requests
				peer.writeFrame([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,%s}`, requestID(req), tc.payload)))
			})
			tr := dialTestTransport(t, peerURL, 0)
			response := tr.Call(t.Context(), "eth_test")
			if err := response.Validate(); err != nil {
				t.Fatal(err)
			}
			if response.Exchange.Progress != rpc.ExchangeCompleted || response.Exchange.Cause != nil || response.Answer.Kind != tc.kind {
				t.Fatalf("response = %+v", response)
			}
		})
	}
}
