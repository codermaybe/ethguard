package wss

// Handshake-layer tests. The peer is a raw in-process HTTP server whose
// handler hijacks the connection and speaks exact bytes, so every acceptance
// criterion of the owned wire contract has a controlled counterpart: valid
// upgrades, malformed responses, redirection, extension negotiation, TLS
// name and certificate errors, deadlines, and cancellation. No third-party
// websocket library takes part.

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// startRawServer serves one connection per request: the handler hijacks the
// connection and hands the exact wire to the script.
func startRawServer(t *testing.T, serve func(t *testing.T, r *http.Request, rw *bufio.ReadWriter, conn net.Conn)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("server does not support hijacking")
			return
		}
		conn, rw, err := hijacker.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		defer conn.Close()
		serve(t, r, rw, conn)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// wsURL converts an httptest HTTP URL to its ws form.
func wsURL(srv *httptest.Server) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

// writeUpgradeAccept answers a client handshake with a valid 101, optionally
// followed by extra raw header lines the script controls.
func writeUpgradeAccept(t *testing.T, rw *bufio.ReadWriter, r *http.Request, extra ...string) {
	t.Helper()
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		t.Error("client sent no Sec-WebSocket-Key")
		return
	}
	lines := []string{
		"HTTP/1.1 101 Switching Protocols",
		"Upgrade: websocket",
		"Connection: Upgrade",
		"Sec-WebSocket-Accept: " + secWebSocketAccept(key),
	}
	lines = append(lines, extra...)
	if _, err := rw.WriteString(strings.Join(append(lines, "", ""), "\r\n")); err != nil {
		t.Errorf("write upgrade accept: %v", err)
		return
	}
	if err := rw.Flush(); err != nil {
		t.Errorf("flush upgrade accept: %v", err)
	}
}

// readUpgradeRequest reads the client's upgrade request from the hijacked
// connection. httptest's parser already consumed the request for the handler,
// so this returns the parsed request the script asserts on.
func upgradeRequestHeader(r *http.Request, name string) string {
	return r.Header.Get(name)
}

// testAuthority builds a minimal CA and one server leaf valid for exactly the
// given DNS names. The pool trusts only this CA.
func testAuthority(t *testing.T, leafDNS []string) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "ethguard wss handshake test ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, caKey.Public(), caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: leafDNS[0]},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     leafDNS,
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, leafKey.Public(), caKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x509.ParseCertificate(leafDER); err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	return tls.Certificate{Certificate: [][]byte{leafDER, caDER}, PrivateKey: leafKey}, pool
}

// startTLSServer serves the hijack protocol over TLS with the given
// certificate.
func startTLSServer(t *testing.T, cert tls.Certificate, serve func(t *testing.T, r *http.Request, rw *bufio.ReadWriter, conn net.Conn)) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("server does not support hijacking")
			return
		}
		conn, rw, err := hijacker.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		defer conn.Close()
		serve(t, r, rw, conn)
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func TestHandshakeUpgradesLoopbackConnection(t *testing.T) {
	srv := startRawServer(t, func(t *testing.T, r *http.Request, rw *bufio.ReadWriter, conn net.Conn) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		if !tokenListContains(r.Header, "Upgrade", "websocket") {
			t.Errorf("client Upgrade header = %q", r.Header.Get("Upgrade"))
		}
		if !tokenListContains(r.Header, "Connection", "upgrade") {
			t.Errorf("client Connection header = %q", r.Header.Get("Connection"))
		}
		if version := r.Header.Get("Sec-WebSocket-Version"); version != "13" {
			t.Errorf("client Sec-WebSocket-Version = %q, want 13", version)
		}
		if r.Header.Get("Sec-WebSocket-Key") == "" {
			t.Error("client sent no Sec-WebSocket-Key")
		}
		if upgradeRequestHeader(r, "Origin") != "" {
			t.Error("client sent an Origin header; the contract sends none")
		}
		if r.Header.Get("Sec-WebSocket-Extensions") != "" {
			t.Error("client offered extensions; the contract offers none")
		}
		if r.Header.Get("Sec-WebSocket-Protocol") != "" {
			t.Error("client offered subprotocols; the contract offers none")
		}
		writeUpgradeAccept(t, rw, r)
		// Write a frame only after the client's handshake deadline has
		// passed: a leaked deadline would fail this read.
		time.Sleep(300 * time.Millisecond)
		if _, err := conn.Write([]byte{0x81, 0x01, 'A'}); err != nil {
			t.Errorf("post-handshake write: %v", err)
		}
	})
	result, err := dialWebSocket(context.Background(), wsURL(srv), dialOptions{handshakeTimeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatalf("dialWebSocket: %v", err)
	}
	defer result.conn.Close()
	frame := make([]byte, 3)
	if _, err := io.ReadFull(result.br, frame); err != nil {
		t.Fatalf("read after handshake deadline elapsed: %v", err)
	}
	if frame[0] != 0x81 || frame[1] != 0x01 || frame[2] != 'A' {
		t.Fatalf("frame bytes = %v, want a one-byte text frame", frame)
	}
}

func TestHandshakePreservesPathQueryAndHost(t *testing.T) {
	srv := startRawServer(t, func(t *testing.T, r *http.Request, rw *bufio.ReadWriter, conn net.Conn) {
		if r.RequestURI != "/eth?chain=main" {
			t.Errorf("request URI = %q, want /eth?chain=main", r.RequestURI)
		}
		if !strings.Contains(r.Host, "127.0.0.1") {
			t.Errorf("Host header = %q, want the endpoint host", r.Host)
		}
		writeUpgradeAccept(t, rw, r)
	})
	endpoint := wsURL(srv) + "/eth?chain=main"
	result, err := dialWebSocket(context.Background(), endpoint, dialOptions{})
	if err != nil {
		t.Fatalf("dialWebSocket: %v", err)
	}
	defer result.conn.Close()
}

func TestHandshakeRejectsNon101Responses(t *testing.T) {
	cases := []struct {
		name       string
		response   string
		wantStatus int
	}{
		{name: "500", response: "HTTP/1.1 500 Internal Server Error\r\nContent-Length: 0\r\n\r\n", wantStatus: 500},
		{name: "404", response: "HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\n\r\n", wantStatus: 404},
		{name: "426-version", response: "HTTP/1.1 426 Upgrade Required\r\nSec-WebSocket-Version: 8\r\nContent-Length: 0\r\n\r\n", wantStatus: 426},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			srv := startRawServer(t, func(t *testing.T, r *http.Request, rw *bufio.ReadWriter, conn net.Conn) {
				if _, err := rw.WriteString(tc.response); err != nil {
					t.Errorf("write response: %v", err)
				}
				_ = rw.Flush()
			})
			_, err := dialWebSocket(context.Background(), wsURL(srv), dialOptions{})
			var handshake *HandshakeError
			if !errors.As(err, &handshake) {
				t.Fatalf("err = %v, want a handshake rejection", err)
			}
			if !errors.Is(err, errHandshakeRejected) {
				t.Fatalf("err = %v, want the rejection sentinel", err)
			}
			if handshake.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d", handshake.StatusCode, tc.wantStatus)
			}
		})
	}
}

func TestHandshakeRefusesRedirects(t *testing.T) {
	requests := 0
	srv := startRawServer(t, func(t *testing.T, r *http.Request, rw *bufio.ReadWriter, conn net.Conn) {
		requests++
		if _, err := rw.WriteString("HTTP/1.1 302 Found\r\nLocation: ws://elsewhere.invalid/socket\r\nContent-Length: 0\r\n\r\n"); err != nil {
			t.Errorf("write redirect: %v", err)
		}
		_ = rw.Flush()
	})
	_, err := dialWebSocket(context.Background(), wsURL(srv), dialOptions{})
	var handshake *HandshakeError
	if !errors.As(err, &handshake) || handshake.StatusCode != 302 {
		t.Fatalf("err = %v, want a redirect rejection", err)
	}
	if requests != 1 {
		t.Fatalf("server saw %d requests, want exactly one (no redirect follow)", requests)
	}
}

func TestHandshakeRejectsBrokenUpgradeHeaders(t *testing.T) {
	cases := []struct {
		name   string
		status string
		extra  []string
	}{
		{name: "wrong-upgrade", extra: []string{"Upgrade: h2c"}},
		{name: "missing-upgrade", extra: nil},
		{name: "missing-connection", extra: []string{"Connection: keep-alive"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			srv := startRawServer(t, func(t *testing.T, r *http.Request, rw *bufio.ReadWriter, conn net.Conn) {
				// Write the 101 manually with the accept key but broken
				// upgrade headers.
				key := r.Header.Get("Sec-WebSocket-Key")
				lines := []string{
					"HTTP/1.1 101 Switching Protocols",
					"Sec-WebSocket-Accept: " + secWebSocketAccept(key),
				}
				lines = append(lines, tc.extra...)
				if _, err := rw.WriteString(strings.Join(append(lines, "", ""), "\r\n")); err != nil {
					t.Errorf("write response: %v", err)
				}
				_ = rw.Flush()
			})
			_, err := dialWebSocket(context.Background(), wsURL(srv), dialOptions{})
			if !errors.Is(err, errHandshakeRejected) {
				t.Fatalf("err = %v, want the rejection sentinel", err)
			}
		})
	}
}

func TestHandshakeRejectsWrongAcceptKey(t *testing.T) {
	srv := startRawServer(t, func(t *testing.T, r *http.Request, rw *bufio.ReadWriter, conn net.Conn) {
		lines := []string{
			"HTTP/1.1 101 Switching Protocols",
			"Upgrade: websocket",
			"Connection: Upgrade",
			"Sec-WebSocket-Accept: " + secWebSocketAccept("not-the-client-key"),
			"",
			"",
		}
		if _, err := rw.WriteString(strings.Join(lines, "\r\n")); err != nil {
			t.Errorf("write response: %v", err)
		}
		_ = rw.Flush()
	})
	_, err := dialWebSocket(context.Background(), wsURL(srv), dialOptions{})
	if !errors.Is(err, errHandshakeRejected) {
		t.Fatalf("err = %v, want the rejection sentinel", err)
	}
}

func TestHandshakeRejectsNegotiatedExtensionAndSubprotocol(t *testing.T) {
	cases := []struct {
		name  string
		extra []string
	}{
		{name: "permessage-deflate", extra: []string{"Sec-WebSocket-Extensions: permessage-deflate; client_max_window_bits"}},
		{name: "unknown-extension", extra: []string{"Sec-WebSocket-Extensions: x-webkit-deflate-frame"}},
		{name: "unrequested-subprotocol", extra: []string{"Sec-WebSocket-Protocol: chat"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			srv := startRawServer(t, func(t *testing.T, r *http.Request, rw *bufio.ReadWriter, conn net.Conn) {
				writeUpgradeAccept(t, rw, r, tc.extra...)
			})
			_, err := dialWebSocket(context.Background(), wsURL(srv), dialOptions{})
			if !errors.Is(err, errHandshakeRejected) {
				t.Fatalf("err = %v, want the rejection sentinel", err)
			}
		})
	}
}

func TestHandshakeTimesOutWithoutContextDeadline(t *testing.T) {
	srv := startRawServer(t, func(t *testing.T, r *http.Request, rw *bufio.ReadWriter, conn net.Conn) {
		// Stall forever: the client's own bound must end the handshake.
		buf := make([]byte, 1)
		for {
			if _, err := conn.Read(buf); err != nil {
				return
			}
		}
	})
	start := time.Now()
	_, err := dialWebSocket(context.Background(), wsURL(srv), dialOptions{handshakeTimeout: 150 * time.Millisecond})
	if err == nil {
		t.Fatal("dialWebSocket succeeded on a stalled server")
	}
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("err = %v, want an i/o deadline failure", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("handshake took %v, the configured bound did not apply", elapsed)
	}
}

func TestHandshakeContextDeadlineAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func() (context.Context, context.CancelFunc)
		want  error
	}{
		{name: "deadline", build: func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), 150*time.Millisecond)
		}, want: context.DeadlineExceeded},
		{name: "canceled", build: func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			time.AfterFunc(50*time.Millisecond, cancel)
			return ctx, cancel
		}, want: context.Canceled},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			srv := startRawServer(t, func(t *testing.T, r *http.Request, rw *bufio.ReadWriter, conn net.Conn) {
				buf := make([]byte, 1)
				for {
					if _, err := conn.Read(buf); err != nil {
						return
					}
				}
			})
			ctx, cancel := tc.build()
			defer cancel()
			_, err := dialWebSocket(ctx, wsURL(srv), dialOptions{})
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestHandshakeContextCancellationClosesTheConnection(t *testing.T) {
	peerClosed := make(chan struct{})
	srv := startRawServer(t, func(t *testing.T, r *http.Request, rw *bufio.ReadWriter, conn net.Conn) {
		buf := make([]byte, 1)
		for {
			if _, err := conn.Read(buf); err != nil {
				close(peerClosed)
				return
			}
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	if _, err := dialWebSocket(ctx, wsURL(srv), dialOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	select {
	case <-peerClosed:
	case <-time.After(3 * time.Second):
		t.Fatal("canceled handshake left the connection open")
	}
}

func TestHandshakeTLSNameAndCertificateErrors(t *testing.T) {
	t.Run("unknown-authority", func(t *testing.T) {
		cert, _ := testAuthority(t, []string{"localhost"})
		srv := startTLSServer(t, cert, func(t *testing.T, r *http.Request, rw *bufio.ReadWriter, conn net.Conn) {
			writeUpgradeAccept(t, rw, r)
		})
		// Dial by DNS name so the name check passes and the failure is the
		// trust chain itself: the client trusts only the system roots, and
		// the test CA is not one of them.
		_, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "https://"))
		if err != nil {
			t.Fatal(err)
		}
		_, err = dialWebSocket(context.Background(), "wss://localhost:"+port, dialOptions{})
		var authorityErr x509.UnknownAuthorityError
		if !errors.As(err, &authorityErr) {
			t.Fatalf("err = %v, want an unknown-authority failure", err)
		}
	})
	t.Run("wrong-server-name", func(t *testing.T) {
		cert, pool := testAuthority(t, []string{"wrong.example"})
		srv := startTLSServer(t, cert, func(t *testing.T, r *http.Request, rw *bufio.ReadWriter, conn net.Conn) {
			writeUpgradeAccept(t, rw, r)
		})
		cfg := &tls.Config{RootCAs: pool}
		_, err := dialWebSocket(context.Background(), "wss://"+strings.TrimPrefix(srv.URL, "https://"), dialOptions{tlsConfig: cfg})
		var nameErr x509.HostnameError
		if !errors.As(err, &nameErr) {
			t.Fatalf("err = %v, want a server-name failure", err)
		}
	})
}

func TestHandshakeTLSWithTrustedAuthoritySucceeds(t *testing.T) {
	cert, pool := testAuthority(t, []string{"localhost"})
	srv := startTLSServer(t, cert, func(t *testing.T, r *http.Request, rw *bufio.ReadWriter, conn net.Conn) {
		writeUpgradeAccept(t, rw, r)
	})
	_, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "https://"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := dialWebSocket(context.Background(), "wss://localhost:"+port, dialOptions{tlsConfig: &tls.Config{RootCAs: pool}})
	if err != nil {
		t.Fatalf("dialWebSocket over wss: %v", err)
	}
	defer result.conn.Close()
}

func TestHandshakeRejectsUnknownSchemesBeforeDialing(t *testing.T) {
	for _, rawURL := range []string{"http://127.0.0.1:1/x", "https://127.0.0.1:1/x", "ipc:///tmp/x", "tcp://127.0.0.1:1"} {
		if _, err := dialWebSocket(context.Background(), rawURL, dialOptions{}); err == nil {
			t.Fatalf("scheme %q must be rejected", rawURL)
		}
	}
}

func TestTokenListContains(t *testing.T) {
	header := http.Header{}
	header.Set("Connection", "keep-alive, Upgrade")
	if !tokenListContains(header, "Connection", "upgrade") {
		t.Error("token list must match case-insensitively across the list")
	}
	if tokenListContains(header, "Connection", "websocket") {
		t.Error("token list matched a token that is not present")
	}
}
