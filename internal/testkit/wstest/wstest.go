// Package wstest provides the test-only server side of the WebSocket
// handshake and frame wire for in-process peers. It exists so verification
// tests can serve raw-call WebSocket exchanges without any third-party
// websocket library: the production client is the owned stack in
// internal/transport/wss, and this package is its scripted counterpart.
//
// This package is verification-only. The architecture interlock forbids
// production packages from importing anything under internal/testkit.
package wstest

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
)

// websocketGUID is the fixed RFC 6455 §1.3 GUID.
const websocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// errClosed reports a peer that went away mid-exchange.
var errClosed = errors.New("wstest: peer closed the connection")

// ServerConn is one accepted server-side connection.
type ServerConn struct {
	conn net.Conn
	br   *bufio.Reader
	t    *testing.T
}

// Upgrade performs the server side of the opening handshake on an incoming
// HTTP request and returns the hijacked connection. The caller owns Close.
func Upgrade(t *testing.T, w http.ResponseWriter, r *http.Request) (*ServerConn, error) {
	t.Helper()
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		http.Error(w, "missing Sec-WebSocket-Key", http.StatusBadRequest)
		return nil, errors.New("wstest: client sent no Sec-WebSocket-Key")
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking unsupported", http.StatusInternalServerError)
		return nil, errors.New("wstest: hijacking unsupported")
	}
	conn, rw, err := hijacker.Hijack()
	if err != nil {
		return nil, fmt.Errorf("wstest: hijack: %w", err)
	}
	response := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + Accept(key) + "\r\n\r\n"
	if _, err := rw.WriteString(response); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("wstest: write upgrade response: %w", err)
	}
	if err := rw.Flush(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("wstest: flush upgrade response: %w", err)
	}
	return &ServerConn{conn: conn, br: rw.Reader, t: t}, nil
}

// Accept derives the Sec-WebSocket-Accept value for a client key.
func Accept(key string) string {
	h := sha1.New()
	_, _ = h.Write([]byte(key + websocketGUID))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// Conn exposes the raw connection for deadline and close control.
func (c *ServerConn) Conn() net.Conn { return c.conn }

// ReadMessage reads one complete client text message. Client pings are
// answered with pongs; a client close ends the exchange with errClosed.
func (c *ServerConn) ReadMessage() ([]byte, error) {
	var assembled []byte
	for {
		fin, opcode, payload, err := c.readFrame()
		if err != nil {
			return nil, err
		}
		switch opcode {
		case 0x1, 0x2, 0x0:
			assembled = append(assembled, payload...)
			if fin {
				return assembled, nil
			}
		case 0x8:
			return nil, errClosed
		case 0x9:
			if err := c.writeFrame(0xA, payload); err != nil {
				return nil, err
			}
		case 0xA:
			// Unsolicited pong: ignore.
		default:
			return nil, fmt.Errorf("wstest: client sent undefined opcode %#x", opcode)
		}
	}
}

// WriteText writes one unmasked server text frame.
func (c *ServerConn) WriteText(payload []byte) error {
	return c.writeFrame(0x1, payload)
}

// Close releases the connection.
func (c *ServerConn) Close() error {
	return c.conn.Close()
}

// readFrame decodes one masked client frame.
func (c *ServerConn) readFrame() (fin bool, opcode byte, payload []byte, err error) {
	var header [2]byte
	if _, err := io.ReadFull(c.br, header[:]); err != nil {
		return false, 0, nil, fmt.Errorf("wstest: read frame header: %w", err)
	}
	fin = header[0]&0x80 != 0
	opcode = header[0] & 0x0F
	masked := header[1]&0x80 != 0
	length := int64(header[1] & 0x7F)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return false, 0, nil, fmt.Errorf("wstest: read 16-bit length: %w", err)
		}
		length = int64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return false, 0, nil, fmt.Errorf("wstest: read 64-bit length: %w", err)
		}
		length = int64(binary.BigEndian.Uint64(ext[:]))
	}
	if opcode&0x8 != 0 && length > 125 {
		return false, 0, nil, errors.New("wstest: control frame payload over 125 bytes")
	}
	if !masked {
		return false, 0, nil, errors.New("wstest: client frame is not masked")
	}
	var mask [4]byte
	if _, err := io.ReadFull(c.br, mask[:]); err != nil {
		return false, 0, nil, fmt.Errorf("wstest: read mask: %w", err)
	}
	payload = make([]byte, length)
	if _, err := io.ReadFull(c.br, payload); err != nil {
		return false, 0, nil, fmt.Errorf("wstest: read payload: %w", err)
	}
	for i := range payload {
		payload[i] ^= mask[i&3]
	}
	return fin, opcode, payload, nil
}

// writeFrame encodes one unmasked server frame.
func (c *ServerConn) writeFrame(opcode byte, payload []byte) error {
	var header [10]byte
	header[0] = 0x80 | opcode
	n := 1
	switch {
	case len(payload) <= 125:
		header[n] = byte(len(payload))
		n++
	case len(payload) <= 0xFFFF:
		header[n] = 126
		n++
		binary.BigEndian.PutUint16(header[n:], uint16(len(payload)))
		n += 2
	default:
		header[n] = 127
		n++
		binary.BigEndian.PutUint64(header[n:], uint64(len(payload)))
		n += 8
	}
	if _, err := c.conn.Write(append(header[:n:n], payload...)); err != nil {
		return fmt.Errorf("wstest: write frame: %w", err)
	}
	return nil
}
