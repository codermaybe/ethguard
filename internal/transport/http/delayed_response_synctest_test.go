package http

import (
	"context"
	"encoding/json"
	"errors"
	nethttp "net/http"
	"testing"
	"testing/synctest"
	"time"
)

type roundTripFunc func(*nethttp.Request) (*nethttp.Response, error)

func (f roundTripFunc) RoundTrip(request *nethttp.Request) (*nethttp.Response, error) {
	return f(request)
}

func TestDelayedResponseDeadlineUsesFakeTimeAndCancelsRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const (
			deadline = 5 * time.Second
			delay    = time.Hour
		)

		requestCanceled := make(chan struct{})
		transport := newTransport("http://external-rpc.test", DefaultBodyLimit)
		transport.client = &nethttp.Client{
			Transport: roundTripFunc(func(request *nethttp.Request) (*nethttp.Response, error) {
				select {
				case <-time.After(delay):
					t.Error("delayed response completed before the request deadline")
					return nil, errors.New("delayed response unexpectedly completed")
				case <-request.Context().Done():
					close(requestCanceled)
					return nil, request.Context().Err()
				}
			}),
		}
		t.Cleanup(transport.Close)

		started := time.Now()
		ctx, cancel := context.WithTimeout(t.Context(), deadline)
		defer cancel()

		var result json.RawMessage
		err := transport.CallContext(ctx, &result, "eth_getBalance", "0x1", "latest")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want context.DeadlineExceeded", err)
		}
		if elapsed := time.Since(started); elapsed != deadline {
			t.Fatalf("fake elapsed time = %s, want %s", elapsed, deadline)
		}

		synctest.Wait()
		select {
		case <-requestCanceled:
		default:
			t.Fatal("deadline returned before RoundTripper observed request cancellation")
		}
		if len(result) != 0 {
			t.Fatalf("deadline produced a business result: %s", result)
		}
	})
}
