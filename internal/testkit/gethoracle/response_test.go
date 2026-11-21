package gethoracle

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

type responseRoundTrip func(*http.Request) (*http.Response, error)

func (f responseRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type responseBlockedBody struct {
	ctx     context.Context
	entered chan struct{}
}

func (b responseBlockedBody) Read([]byte) (int, error) {
	close(b.entered)
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}
func (responseBlockedBody) Close() error { return nil }

func TestCaptureSnapshotPreservesHeadersWhenBodyIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx, box := withRawBody(ctx)
	entered := make(chan struct{})
	tr := capturingTransport{base: responseRoundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Body: responseBlockedBody{ctx: ctx, entered: entered}}, nil
	})}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://test.invalid", nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := tr.RoundTrip(req); done <- err }()
	<-entered
	raw, present, progress, status := box.snapshot()
	if raw != nil || present || progress != rpc.ExchangeResponded || status != 503 {
		t.Fatalf("in-flight snapshot = %s/%v/%v/%d", raw, present, progress, status)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	_, present, progress, status = box.snapshot()
	if present || progress != rpc.ExchangeResponded || status != 503 {
		t.Fatal("cancellation erased observed headers")
	}
}

func TestCaptureSnapshotPublishesCompletionWithItsBody(t *testing.T) {
	ctx, box := withRawBody(t.Context())
	tr := capturingTransport{base: responseRoundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`null`))}, nil
	})}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://test.invalid", nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, err := tr.RoundTrip(req)
		if err != nil {
			t.Error(err)
			return
		}
		_ = resp.Body.Close()
	}()
	for {
		raw, present, progress, status := box.snapshot()
		if progress == rpc.ExchangeCompleted && (!present || string(raw) != "null" || status != 200) {
			t.Fatalf("torn snapshot = %s/%v/%v/%d", raw, present, progress, status)
		}
		select {
		case <-done:
			return
		default:
		}
	}
}
