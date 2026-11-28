// Package endpoint owns endpoint syntax, dial targets and canonical identity.
// Parsing performs no network I/O and does not require an IPC socket to exist.
package endpoint

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

type Kind uint8

const (
	Network Kind = iota + 1
	UnixSocket
	NamedPipe
)

// Endpoint is an immutable parsed endpoint. Its zero value is not an endpoint.
// DialTarget is the sole input to physical dialing; CanonicalID is an identity,
// not an input that consumers should reinterpret as an endpoint.
type Endpoint struct {
	kind        Kind
	transport   string
	dialTarget  string
	canonicalID string
}

func (e Endpoint) Kind() Kind          { return e.kind }
func (e Endpoint) Transport() string   { return e.transport }
func (e Endpoint) DialTarget() string  { return e.dialTarget }
func (e Endpoint) CanonicalID() string { return e.canonicalID }

// Parse recognizes network URLs, Windows named pipes and explicit local paths.
// Relative paths are frozen against cwd now, before any lazy transport opens.
func Parse(raw string) (Endpoint, error) {
	if strings.TrimSpace(raw) == "" {
		return Endpoint{}, fmt.Errorf("endpoint is empty: endpoint is required")
	}
	if strings.ContainsRune(raw, 0) {
		return Endpoint{}, fmt.Errorf("endpoint contains a NUL byte")
	}
	pipe, explicit := raw, false
	if len(raw) >= len("ipc://") && strings.EqualFold(raw[:len("ipc://")], "ipc://") {
		pipe, explicit = raw[len("ipc://"):], true
	}
	pipe = strings.ReplaceAll(pipe, `\`, "/")
	if strings.EqualFold(pipe, "//./pipe") {
		return Endpoint{}, fmt.Errorf("named pipe endpoint requires a nonempty name")
	}
	if strings.HasPrefix(strings.ToLower(pipe), "//./pipe/") {
		name := pipe[len("//./pipe/"):]
		if explicit {
			if strings.ContainsAny(name, "?#") {
				return Endpoint{}, fmt.Errorf("IPC URI must not contain a query or fragment")
			}
			var err error
			name, err = url.PathUnescape(name)
			if err != nil {
				return Endpoint{}, fmt.Errorf("IPC URI has invalid percent encoding: %w", err)
			}
		}
		if name == "" || strings.ContainsRune(name, 0) {
			return Endpoint{}, fmt.Errorf("named pipe endpoint requires a nonempty name")
		}
		target := `\\.\pipe\` + strings.ReplaceAll(name, "/", `\`)
		return Endpoint{kind: NamedPipe, transport: "ipc", dialTarget: target, canonicalID: "ipc://" + strings.ToLower(target)}, nil
	}
	// Paths are recognized before URL parsing, so filename punctuation and
	// whitespace retain their literal meaning. Native absolute paths also keep
	// a normalized target reparsable when construction runs on Windows.
	if strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "./") || strings.HasPrefix(raw, "../") || filepath.IsAbs(raw) {
		return unixPath(raw)
	}
	// Only network/URI spellings receive the historical outer whitespace trim.
	value := strings.TrimSpace(raw)
	if !strings.Contains(value, "://") {
		return Endpoint{}, fmt.Errorf("endpoint is not a valid URL or explicit local path: use an http(s)/ws(s) URL, ./geth.ipc, or /abs/path")
	}
	u, err := url.Parse(value)
	if err != nil {
		return Endpoint{}, fmt.Errorf("endpoint URL is malformed: %w", err)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme == "ipc" {
		if u.Host != "" || u.User != nil || u.Opaque != "" {
			return Endpoint{}, fmt.Errorf("IPC URI must not contain an authority or host")
		}
		if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(value, "#") {
			return Endpoint{}, fmt.Errorf("IPC URI must not contain a query or fragment")
		}
		if !strings.HasPrefix(u.Path, "/") {
			return Endpoint{}, fmt.Errorf("IPC URI requires an absolute path")
		}
		return unixPath(u.Path)
	}
	var adapter string
	switch u.Scheme {
	case "http", "https":
		adapter = "native-http"
	case "ws", "wss":
		adapter = "wss"
	default:
		return Endpoint{}, fmt.Errorf("endpoint scheme %q is not an Ethereum RPC transport: use http, https, ws, or wss, or an explicit IPC path", u.Scheme)
	}
	if u.Host == "" || u.Hostname() == "" || u.Opaque != "" {
		return Endpoint{}, fmt.Errorf("endpoint must include a valid scheme and host")
	}
	target := u.String()
	u.Host = strings.ToLower(u.Host)
	if defaultPort(u.Scheme, u.Port()) {
		u.Host = u.Hostname()
		if strings.Contains(u.Host, ":") {
			u.Host = "[" + u.Host + "]"
		}
	}
	if u.Path == "/" {
		u.Path = ""
	}
	return Endpoint{kind: Network, transport: adapter, dialTarget: target, canonicalID: u.String()}, nil
}

func unixPath(raw string) (Endpoint, error) {
	if strings.ContainsRune(raw, 0) {
		return Endpoint{}, fmt.Errorf("IPC path contains a NUL byte")
	}
	target, err := filepath.Abs(raw)
	if err != nil {
		return Endpoint{}, fmt.Errorf("resolve IPC path: %w", err)
	}
	target = filepath.Clean(target)
	identity := (&url.URL{Scheme: "ipc", Path: filepath.ToSlash(target)}).String()
	return Endpoint{kind: UnixSocket, transport: "ipc", dialTarget: target, canonicalID: identity}, nil
}

func defaultPort(scheme, port string) bool {
	switch scheme {
	case "http", "ws":
		return port == "80"
	case "https", "wss":
		return port == "443"
	}
	return false
}
