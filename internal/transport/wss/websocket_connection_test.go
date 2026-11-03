package wss

// Connection-layer tests. The peer is the raw hijack server from the
// handshake tests, speaking the owned codec from the server side. The tests
// pin the wire mechanics the adapter's lifecycle contract rides on: control
// replies, close-frame semantics, buffered handshake bytes, the read limit,
// and write deadlines. Every server script runs inline on its connection,
// so a script cannot outlive the exchange it asserts on.

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// serverFrames speaks owned frames from the hijacked server side.
type serverFrames struct {
	t    *testing.T
	conn net.Conn
	br   *bufio.Reader
}

func newServerFrames(t *testing.T, conn net.Conn, rw *bufio.ReadWriter) *serverFrames {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Errorf("set server read deadline: %v", err)
	}
	return &serverFrames{t: t, conn: conn, br: rw.Reader}
}

// write emits one raw frame, fragmented frames included: the server side of
// a scripted exchange is allowed to send wire forms the client writer would
// refuse for itself.
func (s *serverFrames) write(fin bool, opcode byte, payload []byte) {
	s.t.Helper()
	if _, err := s.conn.Write(rawServerFrame(fin, opcode, payload)); err != nil {
		s.t.Errorf("server frame write: %v", err)
	}
}

// writeRaw emits arbitrary bytes: malformed frames come from here.
func (s *serverFrames) writeRaw(data []byte) {
	s.t.Helper()
	if _, err := s.conn.Write(data); err != nil {
		s.t.Errorf("server raw write: %v", err)
	}
}

func (s *serverFrames) readFrame() frame {
	s.t.Helper()
	reader := newFrameReader(s.br, serverSide, 1<<20)
	f, err := reader.readFrame()
	if err != nil {
		s.t.Fatalf("server frame read: %v", err)
	}
	return f
}

// expectSilence asserts that no frame crosses the wire within d. A peer
// close before d elapses also confirms the assertion: the connection ended
// with zero bytes delivered, which is exactly the guarantee under test.
func (s *serverFrames) expectSilence(d time.Duration) {
	s.t.Helper()
	if err := s.conn.SetReadDeadline(time.Now().Add(d)); err != nil {
		s.t.Fatalf("set silence deadline: %v", err)
	}
	defer func() { _ = s.conn.SetReadDeadline(time.Now().Add(3 * time.Second)) }()
	buf := make([]byte, 64)
	_, err := s.br.Read(buf)
	switch {
	case err == nil:
		s.t.Fatal("bytes arrived on a wire that must be silent")
	case errors.Is(err, os.ErrDeadlineExceeded), errors.Is(err, io.EOF), errors.Is(err, net.ErrClosed):
		return
	default:
		s.t.Fatalf("silence check failed with %v", err)
	}
}

// startFrameServer upgrades every connection and runs one script per
// connection, inline.
func startFrameServer(t *testing.T, script func(t *testing.T, frames *serverFrames)) *httptest.Server {
	t.Helper()
	return startRawServer(t, func(t *testing.T, r *http.Request, rw *bufio.ReadWriter, conn net.Conn) {
		writeUpgradeAccept(t, rw, r)
		script(t, newServerFrames(t, conn, rw))
	})
}

// dialTestConn dials the raw test server and binds the owned connection.
func dialTestConn(t *testing.T, url string, limit int64) *Conn {
	t.Helper()
	result, err := dialWebSocket(context.Background(), url, dialOptions{})
	if err != nil {
		t.Fatalf("dialWebSocket: %v", err)
	}
	t.Cleanup(func() { _ = result.conn.Close() })
	return newConn(result, limit)
}

func TestConnectionDeliversTextAndAnswersPing(t *testing.T) {
	srv := startFrameServer(t, func(t *testing.T, frames *serverFrames) {
		frames.write(true, opPing, []byte("probe"))
		frames.write(true, opText, []byte("hello"))
		pong := frames.readFrame()
		if pong.opcode != opPong || string(pong.payload) != "probe" {
			t.Errorf("pong = %#x %q, want an echo of the ping payload", pong.opcode, pong.payload)
		}
	})
	conn := dialTestConn(t, wsURL(srv), 1<<20)
	data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("data = %q, want hello", data)
	}
}

func TestConnectionIgnoresUnsolicitedPongs(t *testing.T) {
	srv := startFrameServer(t, func(t *testing.T, frames *serverFrames) {
		frames.write(true, opPong, []byte("whatever"))
		frames.write(true, opText, []byte("hello"))
	})
	conn := dialTestConn(t, wsURL(srv), 1<<20)
	data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("data = %q, want hello", data)
	}
}

func TestConnectionEchoesCloseFrames(t *testing.T) {
	cases := []struct {
		name       string
		payload    []byte
		wantCode   int
		wantReason string
	}{
		{
			name:       "close with code and reason",
			payload:    closePayload(closeGoingAway, "bye"),
			wantCode:   closeGoingAway,
			wantReason: "bye",
		},
		{
			name:       "close without status",
			payload:    nil,
			wantCode:   closeNoStatusReceived,
			wantReason: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := startFrameServer(t, func(t *testing.T, frames *serverFrames) {
				frames.write(true, opClose, tc.payload)
				echo := frames.readFrame()
				if echo.opcode != opClose {
					t.Errorf("echo opcode = %#x, want close", echo.opcode)
					return
				}
				if tc.wantCode == closeNoStatusReceived {
					if len(echo.payload) != 0 {
						t.Errorf("echo payload = %v, want empty for a no-status close", echo.payload)
					}
					return
				}
				parsed, err := parseCloseFrame(echo.payload)
				if err != nil || parsed.Code != tc.wantCode {
					t.Errorf("echo = %d %v, want code %d", parsed.Code, err, tc.wantCode)
				}
			})
			conn := dialTestConn(t, wsURL(srv), 1<<20)
			_, err := conn.ReadMessage()
			var closeErr CloseError
			if !errors.As(err, &closeErr) {
				t.Fatalf("err = %v, want a CloseError", err)
			}
			if closeErr.Code != tc.wantCode || closeErr.Reason != tc.wantReason {
				t.Fatalf("close = %+v, want code %d reason %q", closeErr, tc.wantCode, tc.wantReason)
			}
		})
	}
}

func TestConnectionProtocolViolationAnswersClose1002(t *testing.T) {
	srv := startFrameServer(t, func(t *testing.T, frames *serverFrames) {
		frames.writeRaw([]byte{0x83, 0x00}) // undefined opcode
		echo := frames.readFrame()
		parsed, err := parseCloseFrame(echo.payload)
		if err != nil || parsed.Code != closeProtocolError {
			t.Errorf("close echo = %d %v, want 1002", parsed.Code, err)
		}
	})
	conn := dialTestConn(t, wsURL(srv), 1<<20)
	_, err := conn.ReadMessage()
	var frameErr *frameError
	if !errors.As(err, &frameErr) || frameErr.code != closeProtocolError {
		t.Fatalf("err = %v, want the 1002 protocol error", err)
	}
}

// A broken stream stays broken: the connection's reader is sticky, so a
// caller that ignores the first error cannot misparse what follows.
func TestConnectionReadFailureIsSticky(t *testing.T) {
	srv := startFrameServer(t, func(t *testing.T, frames *serverFrames) {
		frames.writeRaw([]byte{0x83, 0x00}) // undefined opcode
		frames.write(true, opText, []byte("decoy"))
	})
	conn := dialTestConn(t, wsURL(srv), 1<<20)
	if _, first := conn.ReadMessage(); !errors.Is(first, ErrFrameProtocol) {
		t.Fatalf("first read = %v, want the protocol sentinel", first)
	}
	_, second := conn.ReadMessage()
	if !errors.Is(second, ErrFrameProtocol) {
		t.Fatalf("second read = %v, want the same protocol sentinel, not a resynchronized frame", second)
	}
}

// Message-level failures (UTF-8, fragmentation, limits) are sticky at the
// connection layer too: the second read returns the same violation instead
// of the decoy frame that follows on the wire.
func TestConnectionMessageLevelViolationIsSticky(t *testing.T) {
	srv := startFrameServer(t, func(t *testing.T, frames *serverFrames) {
		// A text message whose payload is not valid UTF-8, then a valid
		// frame a non-sticky reader would deliver on the second call.
		frames.write(true, opText, []byte{0xFF, 0xFE})
		frames.write(true, opText, []byte("decoy"))
	})
	conn := dialTestConn(t, wsURL(srv), 1<<20)
	_, first := conn.ReadMessage()
	var frameErr *frameError
	if !errors.As(first, &frameErr) || frameErr.code != closeInvalidPayloadData {
		t.Fatalf("first read = %v, want the 1007 classification", first)
	}
	_, second := conn.ReadMessage()
	if !errors.Is(second, first) {
		t.Fatalf("second read = %v, want the recorded 1007 failure, not a resynchronized frame", second)
	}
}

// The protocol close reply leaves the wire at most once per connection, no
// matter how often the caller reads after the first failure.
func TestConnectionSendsAtMostOneCloseReply(t *testing.T) {
	srv := startFrameServer(t, func(t *testing.T, frames *serverFrames) {
		frames.write(true, opText, []byte{0xFF, 0xFE}) // invalid UTF-8 message
		echo := frames.readFrame()
		parsed, err := parseCloseFrame(echo.payload)
		if err != nil || parsed.Code != closeInvalidPayloadData {
			t.Errorf("close echo = %d %v, want 1007", parsed.Code, err)
		}
		// The caller reads again below: a second close frame must not appear.
		frames.expectSilence(300 * time.Millisecond)
	})
	conn := dialTestConn(t, wsURL(srv), 1<<20)
	if _, err := conn.ReadMessage(); err == nil {
		t.Fatal("first read succeeded on an invalid message")
	}
	if _, err := conn.ReadMessage(); err == nil {
		t.Fatal("second read succeeded on a sticky failure")
	}
}

func TestConnectionCloseWithInvalidReasonAnswersClose1007(t *testing.T) {
	srv := startFrameServer(t, func(t *testing.T, frames *serverFrames) {
		// Close code 1000 followed by a reason byte that is not UTF-8.
		frames.writeRaw([]byte{0x88, 0x03, 0x03, 0xE8, 0xFF})
		echo := frames.readFrame()
		parsed, err := parseCloseFrame(echo.payload)
		if err != nil || parsed.Code != closeInvalidPayloadData {
			t.Errorf("close echo = %d %v, want 1007", parsed.Code, err)
		}
	})
	conn := dialTestConn(t, wsURL(srv), 1<<20)
	_, err := conn.ReadMessage()
	var frameErr *frameError
	if !errors.As(err, &frameErr) || frameErr.code != closeInvalidPayloadData {
		t.Fatalf("err = %v, want the 1007 classification", err)
	}
}

func TestConnectionReadLimitAbortsOnOversizedFrame(t *testing.T) {
	srv := startFrameServer(t, func(t *testing.T, frames *serverFrames) {
		frames.write(true, opText, make([]byte, 1000))
		// A read-limit breach aborts without a close frame: the stream can
		// never be resynchronized, so no close handshake is attempted.
		frames.expectSilence(300 * time.Millisecond)
	})
	conn := dialTestConn(t, wsURL(srv), 64)
	if _, err := conn.ReadMessage(); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("err = %v, want the frame-too-large sentinel", err)
	}
}

func TestConnectionAssemblesFragmentedMessage(t *testing.T) {
	srv := startFrameServer(t, func(t *testing.T, frames *serverFrames) {
		frames.write(false, opText, []byte("frag"))
		frames.write(false, opContinuation, []byte("ment"))
		frames.write(true, opContinuation, []byte("ed"))
	})
	conn := dialTestConn(t, wsURL(srv), 1<<20)
	data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if string(data) != "fragmented" {
		t.Fatalf("data = %q, want fragmented", data)
	}
}

func TestConnectionHandshakeBufferedBytesCarryOver(t *testing.T) {
	srv := startRawServer(t, func(t *testing.T, r *http.Request, rw *bufio.ReadWriter, conn net.Conn) {
		// The 101 response and the first data frame leave in ONE write: the
		// frame bytes land in the handshake reader's buffer, and the frame
		// reader must consume them from there.
		key := r.Header.Get("Sec-WebSocket-Key")
		head := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + secWebSocketAccept(key) + "\r\n\r\n"
		buf := append([]byte(head), rawServerFrame(true, opText, []byte("carried"))...)
		if _, err := conn.Write(buf); err != nil {
			t.Errorf("single write: %v", err)
		}
	})
	result, err := dialWebSocket(context.Background(), wsURL(srv), dialOptions{})
	if err != nil {
		t.Fatalf("dialWebSocket: %v", err)
	}
	defer result.conn.Close()
	conn := newConn(result, 1<<20)
	data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if string(data) != "carried" {
		t.Fatalf("data = %q, want the frame buffered behind the handshake", data)
	}
}

func TestConnectionWriteFailsAfterPeerClose(t *testing.T) {
	srv := startRawServer(t, func(t *testing.T, r *http.Request, rw *bufio.ReadWriter, conn net.Conn) {
		writeUpgradeAccept(t, rw, r)
		_ = conn.Close()
	})
	conn := dialTestConn(t, wsURL(srv), 1<<20)
	time.Sleep(50 * time.Millisecond)
	// The first write into a freshly closed loopback peer can still land in
	// the kernel buffer; the connection is unusable from the second write on.
	if err := conn.WriteMessage(context.Background(), []byte(`{"first":true}`)); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := conn.WriteMessage(context.Background(), []byte(`{"second":true}`)); err == nil {
		t.Fatal("write to a closed connection succeeded")
	}
}

func TestConnectionWriteDeadlineBoundsAndClears(t *testing.T) {
	// A server that never reads: a large write must hit the context
	// deadline instead of blocking forever.
	stall := make(chan struct{})
	srv := startRawServer(t, func(t *testing.T, r *http.Request, rw *bufio.ReadWriter, conn net.Conn) {
		writeUpgradeAccept(t, rw, r)
		<-stall
	})
	conn := dialTestConn(t, wsURL(srv), 1<<20)
	defer close(stall)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := conn.WriteMessage(ctx, make([]byte, 16<<20)); err == nil {
		t.Fatal("unbounded write into a stalled connection succeeded")
	} else if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("err = %v, want an i/o deadline failure", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("write returned after %v, the deadline did not bound it", elapsed)
	}
	// A following small write on a fresh connection must not inherit the
	// stale deadline: a context without a deadline clears it.
	fresh := dialTestConn(t, wsURL(srv), 1<<20)
	if err := fresh.WriteMessage(context.Background(), []byte(`{}`)); err != nil {
		t.Fatalf("write after the deadline elapsed: %v", err)
	}
}

func TestConnectionWriteSkipsBytesWhenContextCanceled(t *testing.T) {
	srv := startFrameServer(t, func(t *testing.T, frames *serverFrames) {
		frames.expectSilence(300 * time.Millisecond)
	})
	conn := dialTestConn(t, wsURL(srv), 1<<20)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := conn.WriteMessage(ctx, []byte(`{"canceled":true}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestClosePayloadEncoding(t *testing.T) {
	payload := closePayload(closeNormalClosure, "ok")
	if len(payload) != 4 || payload[0] != 0x03 || payload[1] != 0xE8 || string(payload[2:]) != "ok" {
		t.Fatalf("payload = %v, want code 1000 with reason ok", payload)
	}
	parsed, err := parseCloseFrame(payload)
	if err != nil || parsed.Code != closeNormalClosure || parsed.Reason != "ok" {
		t.Fatalf("round trip = %+v %v", parsed, err)
	}
	if !strings.Contains(parsed.Error(), "1000") {
		t.Fatalf("error text = %q", parsed.Error())
	}
}
