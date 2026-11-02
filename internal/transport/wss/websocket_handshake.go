package wss

// The client side of the RFC 6455 opening handshake, implemented directly on
// the Go standard library: net.Dialer for the TCP connection, crypto/tls for
// wss, a hand-written HTTP Upgrade request, and net/http + bufio to parse the
// response. The owned wire contract (docs/design/wss-wire-contract.md §2.1)
// pins this layer to ws/wss, client masking, and a fail-closed response
// validation: 101 with a matching Sec-WebSocket-Accept, no extension or
// subprotocol negotiation, and no redirection.

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// websocketGUID is the fixed GUID from RFC 6455 §1.3 that both sides combine
// with Sec-WebSocket-Key to derive Sec-WebSocket-Accept.
const websocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// errHandshakeRejected marks a completed HTTP exchange that did not upgrade.
// Every refusal carries it so tests and future dial classification match the
// outcome, not the message text.
var errHandshakeRejected = errors.New("wss: websocket handshake rejected")

// HandshakeError reports why an HTTP response failed the upgrade validation.
// StatusCode carries the HTTP status the server answered with, so evidence
// tooling can record provider handshake refusals without parsing text.
type HandshakeError struct {
	// StatusCode is the HTTP status the server answered with; 0 when the
	// status line itself was unusable.
	StatusCode int
	// Status is the raw status line when one was parsed.
	Status string
	// Detail names the failed validation.
	Detail string
}

func (e *HandshakeError) Error() string {
	if e.Status != "" {
		return fmt.Sprintf("%s: %s: %s", errHandshakeRejected, e.Status, e.Detail)
	}
	return fmt.Sprintf("%s: %s", errHandshakeRejected, e.Detail)
}

func (e *HandshakeError) Unwrap() error { return errHandshakeRejected }

// dialOptions bounds and secures one client handshake.
type dialOptions struct {
	// handshakeTimeout bounds dial, TLS, request write, and response read
	// when the context carries no earlier deadline. Zero means unbounded.
	handshakeTimeout time.Duration
	// tlsConfig replaces the default client TLS configuration. ServerName is
	// derived from the endpoint host when the config does not set it.
	// Production adapters pass nil and get system root verification.
	tlsConfig *tls.Config
}

// handshakeResult is one accepted upgrade: the live connection (TLS-wrapped
// for wss) and the reader that may already hold bytes buffered past the
// response header. The frame reader must consume br — reading conn directly
// would lose those bytes.
type handshakeResult struct {
	conn net.Conn
	br   *bufio.Reader
}

// dialWebSocket performs the client side of the RFC 6455 opening handshake.
// Only ws and wss are known schemes; anything else is refused before any
// network I/O. A 3xx answer is a rejection, never a redirect: the contract
// pins the connection to the host the caller named. On success the connection
// deadlines are cleared; frame I/O is governed by call contexts alone.
func dialWebSocket(ctx context.Context, rawURL string, opts dialOptions) (*handshakeResult, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("wss: parse endpoint: %w", err)
	}
	var secure bool
	switch u.Scheme {
	case "ws":
	case "wss":
		secure = true
	default:
		return nil, fmt.Errorf("wss: no known transport for URL scheme %q", u.Scheme)
	}
	hostPort, err := hostPortForScheme(u, secure)
	if err != nil {
		return nil, err
	}

	deadline := time.Time{}
	if d, ok := ctx.Deadline(); ok {
		deadline = d
	}
	if opts.handshakeTimeout > 0 {
		bounded := time.Now().Add(opts.handshakeTimeout)
		if deadline.IsZero() || bounded.Before(deadline) {
			deadline = bounded
		}
	}

	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", hostPort)
	if err != nil {
		return nil, contextOutcome(ctx, err, "wss: dial tcp %s: %w", hostPort, err)
	}
	accepted := false
	defer func() {
		if !accepted {
			_ = conn.Close()
		}
	}()

	// Abort handshake I/O as soon as the context is canceled, even when the
	// context carries no deadline. The watcher is disarmed before the result
	// is returned; a caller that cancels at exactly that moment races with
	// its own dial and gets a closed connection either way.
	if ctx.Done() != nil {
		abort := make(chan struct{})
		defer close(abort)
		go func() {
			select {
			case <-ctx.Done():
				_ = conn.Close()
			case <-abort:
			}
		}()
	}

	if !deadline.IsZero() {
		_ = conn.SetDeadline(deadline)
	}

	if secure {
		tlsConn, err := tlsHandshake(ctx, conn, hostPort, opts.tlsConfig)
		if err != nil {
			return nil, err
		}
		conn = tlsConn
	}

	key, err := secWebSocketKey()
	if err != nil {
		return nil, fmt.Errorf("wss: generate handshake key: %w", err)
	}
	if _, err := conn.Write(upgradeRequest(u, key)); err != nil {
		return nil, contextOutcome(ctx, err, "wss: write upgrade request: %w", err)
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	if err != nil {
		return nil, contextOutcome(ctx, err, "wss: read handshake response: %w", err)
	}
	if err := validateUpgradeResponse(resp, key); err != nil {
		return nil, err
	}

	accepted = true
	_ = conn.SetDeadline(time.Time{})
	return &handshakeResult{conn: conn, br: br}, nil
}

// contextOutcome prefers the context's own outcome when the context has
// ended: a closed connection or an i/o deadline is the mechanism the watcher
// used to stop the handshake, not the cause. The conn deadline and the
// context deadline are the same instant, so an i/o timeout can surface just
// before the context timer runs; such errors get a bounded chance to settle
// so callers classify the cause, not the symptom.
func contextOutcome(ctx context.Context, err error, format string, args ...any) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if ctx.Done() != nil && (errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, net.ErrClosed)) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
	}
	return fmt.Errorf(format, args...)
}

// hostPortForScheme returns the dial address, applying the scheme's default
// port when the URL names none.
func hostPortForScheme(u *url.URL, secure bool) (string, error) {
	if u.Host == "" {
		return "", fmt.Errorf("wss: endpoint URL %q has no host", u)
	}
	if _, _, err := net.SplitHostPort(u.Host); err == nil {
		return u.Host, nil
	}
	if secure {
		return net.JoinHostPort(u.Host, "443"), nil
	}
	return net.JoinHostPort(u.Host, "80"), nil
}

// tlsHandshake wraps conn in a TLS client session. The server name comes from
// the dial address unless the configuration pins one; certificate and name
// verification use the standard library defaults and are never disabled here.
func tlsHandshake(ctx context.Context, conn net.Conn, hostPort string, cfg *tls.Config) (net.Conn, error) {
	if cfg == nil {
		cfg = &tls.Config{}
	} else {
		cfg = cfg.Clone()
	}
	if cfg.ServerName == "" {
		serverName, _, err := net.SplitHostPort(hostPort)
		if err != nil {
			return nil, fmt.Errorf("wss: tls handshake: resolve server name: %w", err)
		}
		cfg.ServerName = serverName
	}
	tlsConn := tls.Client(conn, cfg)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return nil, contextOutcome(ctx, err, "wss: tls handshake: %w", err)
	}
	return tlsConn, nil
}

// upgradeRequest writes the exact client bytes of the opening handshake: no
// Origin, no subprotocol, no extension offer.
func upgradeRequest(u *url.URL, key string) []byte {
	requestURI := u.RequestURI()
	if requestURI == "" {
		requestURI = "/"
	}
	var b strings.Builder
	b.WriteString("GET " + requestURI + " HTTP/1.1\r\n")
	b.WriteString("Host: " + u.Host + "\r\n")
	b.WriteString("Upgrade: websocket\r\n")
	b.WriteString("Connection: Upgrade\r\n")
	b.WriteString("Sec-WebSocket-Key: " + key + "\r\n")
	b.WriteString("Sec-WebSocket-Version: 13\r\n")
	b.WriteString("\r\n")
	return []byte(b.String())
}

// secWebSocketKey returns a fresh 16-byte key per RFC 6455 §4.1.
func secWebSocketKey() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf), nil
}

// secWebSocketAccept derives the expected Sec-WebSocket-Accept value.
func secWebSocketAccept(key string) string {
	h := sha1.New()
	_, _ = h.Write([]byte(key + websocketGUID))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// validateUpgradeResponse applies the fail-closed response checks: 101 only,
// correct Upgrade and Connection headers, matching Sec-WebSocket-Accept, and
// no negotiated extension or subprotocol the client never offered.
func validateUpgradeResponse(resp *http.Response, key string) error {
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return &HandshakeError{StatusCode: resp.StatusCode, Status: resp.Status, Detail: "redirection is not part of the handshake contract"}
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		return &HandshakeError{StatusCode: resp.StatusCode, Status: resp.Status, Detail: "the server did not switch protocols"}
	}
	if !tokenListContains(resp.Header, "Upgrade", "websocket") {
		return &HandshakeError{StatusCode: resp.StatusCode, Status: resp.Status, Detail: "missing or wrong Upgrade header"}
	}
	if !tokenListContains(resp.Header, "Connection", "upgrade") {
		return &HandshakeError{StatusCode: resp.StatusCode, Status: resp.Status, Detail: "missing or wrong Connection header"}
	}
	accept := resp.Header.Get("Sec-WebSocket-Accept")
	switch {
	case accept == "":
		return &HandshakeError{StatusCode: resp.StatusCode, Status: resp.Status, Detail: "missing Sec-WebSocket-Accept"}
	case accept != secWebSocketAccept(key):
		return &HandshakeError{StatusCode: resp.StatusCode, Status: resp.Status, Detail: "wrong Sec-WebSocket-Accept"}
	}
	if ext := resp.Header.Get("Sec-WebSocket-Extensions"); ext != "" {
		return &HandshakeError{StatusCode: resp.StatusCode, Status: resp.Status, Detail: "server negotiated an extension the client never offered: " + ext}
	}
	if proto := resp.Header.Get("Sec-WebSocket-Protocol"); proto != "" {
		return &HandshakeError{StatusCode: resp.StatusCode, Status: resp.Status, Detail: "server selected a subprotocol the client never offered: " + proto}
	}
	return nil
}

// tokenListContains reports whether the named header holds a comma-separated
// token list containing value, case-insensitively (RFC 7230 §3.2).
func tokenListContains(header http.Header, name, value string) bool {
	for _, entry := range header.Values(name) {
		for _, token := range strings.Split(entry, ",") {
			if strings.EqualFold(strings.TrimSpace(token), value) {
				return true
			}
		}
	}
	return false
}

// DialRaw performs the owned handshake and returns the raw connection for
// single-frame exchanges. It is the manual-evidence seam behind
// cmd/wsscapture: the production path is Dialer.DialContext, which owns the
// call lifecycle. readLimit bounds inbound messages; zero or less selects
// DefaultReadLimit.
func DialRaw(ctx context.Context, rawURL string, handshakeTimeout time.Duration, readLimit int64) (*Conn, error) {
	if readLimit <= 0 {
		readLimit = DefaultReadLimit
	}
	result, err := dialWebSocket(ctx, rawURL, dialOptions{handshakeTimeout: handshakeTimeout})
	if err != nil {
		return nil, fmt.Errorf("wss: dial: %w", err)
	}
	return newConn(result, readLimit), nil
}
