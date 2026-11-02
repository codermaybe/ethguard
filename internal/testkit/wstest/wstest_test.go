package wstest

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/transport/wss"
)

func TestUpgradeExchangesFramesWithTheOwnedClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := Upgrade(t, w, r)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		body, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		if err := conn.WriteText(body); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer srv.Close()

	conn, err := wss.DialRaw(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http"), 5*time.Second, 0)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := conn.WriteMessage(ctx, []byte("echo-me")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("read deadline: %v", err)
	}
	data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "echo-me" {
		t.Fatalf("round trip = %q, want echo-me", data)
	}
}

func TestReadMessageReportsClientClose(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := Upgrade(t, w, r)
		if err != nil {
			return
		}
		defer conn.Close()
		// An abrupt client close is a stream end, not a close frame: the
		// read fails with io.EOF rather than the frame-level sentinel.
		if _, err := conn.ReadMessage(); !errors.Is(err, errClosed) && !errors.Is(err, io.EOF) {
			t.Errorf("read after client close = %v, want errClosed or EOF", err)
		}
	}))
	defer srv.Close()

	conn, err := wss.DialRaw(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http"), 5*time.Second, 0)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = conn.Close()
}

func TestAcceptDerivesRFC6455Value(t *testing.T) {
	// The fixed example pair from RFC 6455 §1.3.
	got := Accept("dGhlIHNhbXBsZSBub25jZQ==")
	if got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatalf("Accept = %q, want the RFC 6455 example value", got)
	}
}
