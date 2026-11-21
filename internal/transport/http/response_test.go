package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/codermaybe/ethguard/internal/rpcwire"
	"github.com/codermaybe/ethguard/internal/transport"
	"github.com/codermaybe/ethguard/rpc"
)

func TestResponseKeepsExchangeAndAnswerDistinct(t *testing.T) {
	cases := []struct {
		name, body string
		status     int
		kind       rpc.AnswerKind
		want       error
	}{
		{"empty", "", 200, rpc.AnswerMissing, rpcwire.ErrEmptyResponse},
		{"whitespace", " \n", 200, rpc.AnswerMissing, rpcwire.ErrEmptyResponse},
		{"null", `{"jsonrpc":"2.0","id":1,"result":null}`, 200, rpc.AnswerResult, nil},
		{"error", `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"rejected","data":null}}`, 200, rpc.AnswerProtocolError, nil},
		{"bad-json", `{`, 200, rpc.AnswerInvalid, rpcwire.ErrMalformedResponse},
		{"wrong-id", `{"jsonrpc":"2.0","id":999,"result":1}`, 200, rpc.AnswerInvalid, rpcwire.ErrIDMismatch},
		{"http-error", `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"not an RPC answer"}}`, 503, rpc.AnswerUnassessed, nil},
		{"nonstandard-status", "unavailable", 700, rpc.AnswerUnassessed, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			tr := newTransport(srv.URL, DefaultBodyLimit)
			defer tr.Close()
			response := tr.Call(t.Context(), "eth_test")
			if err := response.Validate(); err != nil {
				t.Fatal(err)
			}
			if response.Exchange.Progress != rpc.ExchangeCompleted || response.Exchange.HTTPStatus != tc.status || response.Answer.Kind != tc.kind {
				t.Fatalf("response = %+v", response)
			}
			if tc.want != nil && !errors.Is(response.Err(), tc.want) {
				t.Fatalf("error = %v, want %v", response.Err(), tc.want)
			}
			if tc.kind == rpc.AnswerResult && (response.Err() != nil || string(response.Answer.Result) != "null") {
				t.Fatalf("null = %+v", response)
			}
			if tc.kind == rpc.AnswerProtocolError {
				if response.Exchange.Cause != nil || response.Answer.RPCError.DataPresence != rpc.ErrorDataPresenceNull {
					t.Fatalf("protocol error = %+v", response)
				}
				var protocol rpc.ProtocolError
				if !errors.As(response.Err(), &protocol) {
					t.Fatal("protocol capability missing")
				}
			}
			if tc.status >= 300 {
				var status rpc.HTTPStatusError
				if !errors.As(response.Err(), &status) || status.HTTPStatusCode() != tc.status {
					t.Fatalf("status lost: %v", response.Err())
				}
				var protocol rpc.ProtocolError
				if errors.As(response.Err(), &protocol) {
					t.Fatal("HTTP body became a protocol answer")
				}
			}
		})
	}
}

type interruptedBody struct {
	ctx    context.Context
	prefix string
}

func (b *interruptedBody) Read(p []byte) (int, error) {
	if b.prefix != "" {
		n := copy(p, b.prefix)
		b.prefix = b.prefix[n:]
		return n, nil
	}
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}
func (*interruptedBody) Close() error { return nil }

func TestResponseDeadlineAfterHeadersPreservesProgress(t *testing.T) {
	for _, status := range []int{200, 503} {
		for _, prefix := range []string{"", `{"jsonrpc":`} {
			synctest.Test(t, func(t *testing.T) {
				tr := newTransport("http://test.invalid", DefaultBodyLimit)
				defer tr.Close()
				tr.client = &nethttp.Client{Transport: roundTripFunc(func(req *nethttp.Request) (*nethttp.Response, error) {
					return &nethttp.Response{StatusCode: status, Status: "test status", Header: make(nethttp.Header), Body: &interruptedBody{ctx: req.Context(), prefix: prefix}}, nil
				})}
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				response := tr.Call(ctx, "eth_test")
				if !errors.Is(response.Err(), context.DeadlineExceeded) || response.Exchange.Progress != rpc.ExchangeResponded || response.Exchange.HTTPStatus != status || response.Answer.Kind != rpc.AnswerUnassessed {
					t.Fatalf("response = %+v", response)
				}
			})
		}
	}
}

func TestResponseRetainsStatusAndInterruptedBodyCause(t *testing.T) {
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(503)
		_, _ = io.WriteString(w, "short")
	}))
	defer srv.Close()
	tr := newTransport(srv.URL, DefaultBodyLimit)
	defer tr.Close()
	response := tr.Call(t.Context(), "eth_test")
	var status rpc.HTTPStatusError
	if !errors.As(response.Err(), &status) || status.HTTPStatusCode() != 503 || !errors.Is(response.Err(), io.ErrUnexpectedEOF) {
		t.Fatalf("status/read cause = %v", response.Err())
	}
	if response.Exchange.Progress != rpc.ExchangeResponded || response.Answer.Kind != rpc.AnswerUnassessed {
		t.Fatalf("response = %+v", response)
	}
}

func TestResponseLimitNeverInterpretsPrefix(t *testing.T) {
	for _, status := range []int{200, 503} {
		t.Run(nethttp.StatusText(status), func(t *testing.T) {
			srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
				w.WriteHeader(status)
				_, _ = io.WriteString(w, strings.Repeat("x", maxStatusBody+2))
			}))
			defer srv.Close()
			tr := newTransport(srv.URL, 32)
			defer tr.Close()
			response := tr.Call(t.Context(), "eth_test")
			if !errors.Is(response.Err(), transport.ErrBodyTooLarge) || response.Exchange.Progress != rpc.ExchangeResponded || response.Exchange.HTTPStatus != status || response.Answer.Kind != rpc.AnswerUnassessed {
				t.Fatalf("response = %+v", response)
			}
		})
	}
}

func TestResponseCancellationAfterCompleteBodyKeepsAnswer(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	tr := newTransport("http://test.invalid", DefaultBodyLimit)
	defer tr.Close()
	tr.client = &nethttp.Client{Transport: roundTripFunc(func(*nethttp.Request) (*nethttp.Response, error) {
		return &nethttp.Response{StatusCode: 200, Body: &cancelOnCloseBody{Reader: strings.NewReader(`{"jsonrpc":"2.0","id":1,"result":null}`), cancel: cancel}, Header: make(nethttp.Header)}, nil
	})}
	response := tr.Call(ctx, "eth_test")
	if !errors.Is(response.Err(), context.Canceled) || response.Exchange.Progress != rpc.ExchangeCompleted || response.Answer.Kind != rpc.AnswerResult || string(response.Answer.Result) != "null" {
		t.Fatalf("response = %+v", response)
	}
	var value json.RawMessage
	if !errors.Is(response.Decode(&value), context.Canceled) || value != nil {
		t.Fatal("cancellation did not govern delivery")
	}
}

type cancelOnCloseBody struct {
	io.Reader
	cancel context.CancelFunc
}

func (b *cancelOnCloseBody) Close() error { b.cancel(); return nil }
