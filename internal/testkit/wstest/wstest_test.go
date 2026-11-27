package wstest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
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
		defer conn.Close() //nolint:errcheck // Socket cleanup has no pending output; disconnects are expected at fixture teardown.
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
	defer conn.Close() //nolint:errcheck // Socket cleanup has no pending output; disconnects are expected at fixture teardown.
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
		defer conn.Close() //nolint:errcheck // Socket cleanup has no pending output; disconnects are expected at fixture teardown.
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

func TestReadMessageRejectsUnrepresentableLengths(t *testing.T) {
	tests := []struct {
		name   string
		length uint64
	}{
		{name: "64-bit high bit", length: 1 << 63},
		{name: "maximum unsigned length", length: math.MaxUint64},
		{name: "above native int", length: uint64(math.MaxInt) + 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("ReadMessage panicked for length %d: %v", tt.length, recovered)
				}
			}()
			wire := binary.BigEndian.AppendUint64([]byte{0x81, 0xFF}, tt.length)
			// A complete mask ensures the old reader reaches its allocation;
			// truncating it would hide the unchecked length behind an EOF.
			wire = append(wire, 1, 2, 3, 4)
			conn := &ServerConn{br: bufio.NewReader(bytes.NewReader(wire))}
			body, err := conn.ReadMessage()
			want := "wstest: frame payload exceeds int range"
			if tt.length&(1<<63) != 0 {
				want = "wstest: 64-bit payload length has the high bit set"
			}
			if err == nil || err.Error() != want || body != nil {
				t.Fatalf("ReadMessage = (%v, %v), want (nil, %q)", body, err, want)
			}
		})
	}
}

func TestReadMessageMaskedLengthBoundaries(t *testing.T) {
	tests := []struct {
		size   int
		header []byte
	}{
		{size: 0, header: []byte{0x81, 0x80}},
		{size: 1, header: []byte{0x81, 0x81}},
		{size: 125, header: []byte{0x81, 0xFD}},
		{size: 126, header: []byte{0x81, 0xFE, 0, 126}},
		{size: 65535, header: []byte{0x81, 0xFE, 0xFF, 0xFF}},
		{size: 65536, header: []byte{0x81, 0xFF, 0, 0, 0, 0, 0, 1, 0, 0}},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.size), func(t *testing.T) {
			payload := bytes.Repeat([]byte("x"), tt.size)
			mask := [4]byte{1, 2, 3, 4}
			wire := append(tt.header, mask[:]...)
			for i, b := range payload {
				wire = append(wire, b^mask[i&3])
			}
			conn := &ServerConn{br: bufio.NewReader(bytes.NewReader(wire))}
			body, err := conn.ReadMessage()
			if err != nil || !bytes.Equal(body, payload) {
				t.Fatalf("ReadMessage = (%d bytes, %v), want %d matching bytes", len(body), err, tt.size)
			}
		})
	}
}

func TestReadMessagePreservesTruncatedFrameErrors(t *testing.T) {
	tests := []struct {
		name string
		wire []byte
		want string
		err  error
	}{
		{name: "missing header", want: "read frame header", err: io.EOF},
		{name: "partial header", wire: []byte{0x81}, want: "read frame header", err: io.ErrUnexpectedEOF},
		{name: "16-bit length", wire: []byte{0x81, 0xFE, 0x01}, want: "read 16-bit length", err: io.ErrUnexpectedEOF},
		{name: "64-bit length", wire: []byte{0x81, 0xFF, 0, 0, 0, 0, 0, 1, 0}, want: "read 64-bit length", err: io.ErrUnexpectedEOF},
		{name: "mask", wire: []byte{0x81, 0x81, 1, 2, 3}, want: "read mask", err: io.ErrUnexpectedEOF},
		{name: "payload", wire: []byte{0x81, 0x82, 1, 2, 3, 4, 'x' ^ 1}, want: "read payload", err: io.ErrUnexpectedEOF},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := &ServerConn{br: bufio.NewReader(bytes.NewReader(tt.wire))}
			body, err := conn.ReadMessage()
			if !errors.Is(err, tt.err) || !strings.Contains(err.Error(), tt.want) || body != nil {
				t.Fatalf("ReadMessage = (%v, %v), want nil and %s wrapping %v", body, err, tt.want, tt.err)
			}
		})
	}
}
