package endpoint_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/internal/endpoint"
)

func TestParsedEndpointSeparatesDialTargetAndIdentity(t *testing.T) {
	for _, tc := range []struct {
		input, target, identity, transport string
		kind                               endpoint.Kind
	}{
		{" HTTPS://NODE.EXAMPLE:443/ ", "https://NODE.EXAMPLE:443/", "https://node.example", "native-http", endpoint.Network},
		{"WSS://NODE.EXAMPLE:443/rpc?key=A", "wss://NODE.EXAMPLE:443/rpc?key=A", "wss://node.example/rpc?key=A", "wss", endpoint.Network},
		{`\\.\pipe\Geth.IPC`, `\\.\pipe\Geth.IPC`, `ipc://\\.\pipe\geth.ipc`, "ipc", endpoint.NamedPipe},
		{`ipc:////./pipe/Geth.IPC`, `\\.\pipe\Geth.IPC`, `ipc://\\.\pipe\geth.ipc`, "ipc", endpoint.NamedPipe},
	} {
		t.Run(tc.input, func(t *testing.T) {
			parsed, err := endpoint.Parse(tc.input)
			if err != nil || parsed.Kind() != tc.kind || parsed.Transport() != tc.transport || parsed.DialTarget() != tc.target || parsed.CanonicalID() != tc.identity {
				t.Fatalf("parsed=%+v error=%v", parsed, err)
			}
			rematerialized, err := endpoint.Parse(parsed.DialTarget())
			if err != nil || rematerialized != parsed {
				t.Fatalf("dial target is not stable: %+v, %v", rematerialized, err)
			}
		})
	}
}

func TestRelativePathFreezeDoesNotFollowLaterWorkingDirectory(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	t.Chdir(first)
	parsed, err := endpoint.Parse("./run/../geth #?%.ipc ")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(first, "geth #?%.ipc ")
	if parsed.Kind() != endpoint.UnixSocket || parsed.DialTarget() != want {
		t.Fatalf("path=%q kind=%v, want %q", parsed.DialTarget(), parsed.Kind(), want)
	}
	t.Chdir(second)
	reparsed, err := endpoint.Parse(parsed.DialTarget())
	if err != nil || reparsed != parsed {
		t.Fatalf("frozen path changed with cwd: %+v, %v", reparsed, err)
	}
}

func TestMalformedEndpointsNeverBecomeLocalPaths(t *testing.T) {
	for _, input := range []string{
		"geth.ipc", "localhost:8545/path", "http:/node", "https:node", "ftp://node", "tcp://node",
		"http://", "ws:///path", "ipc://host/path", "ipc://user@/path", "ipc:///path?x=y", "ipc:///path?",
		"ipc:///path#fragment", "ipc:///path#", "ipc:///path%zz", "ipc:///path%00", `\\.\pipe\`, "//./pipe", "//./pipe/",
		"ipc:////./pipe/", "ipc:////./pipe/name?query", "ipc:////./pipe/name%zz",
	} {
		t.Run(input, func(t *testing.T) {
			if parsed, err := endpoint.Parse(input); err == nil {
				t.Fatalf("malformed endpoint accepted: %+v", parsed)
			}
		})
	}
}

func FuzzParsedDialTargetStaysStable(f *testing.F) {
	for _, seed := range []string{"https://NODE:443/", "ws://localhost:80/path", "/tmp/name #?%.ipc ", "./node.ipc", `\\.\pipe\GETH`, "ipc:////./pipe/geth", "ipc:///tmp/%23name"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		parsed, err := endpoint.Parse(input)
		if err != nil {
			return
		}
		if parsed.Kind() == 0 || parsed.Transport() == "" || parsed.CanonicalID() == "" || parsed.DialTarget() == "" || strings.ContainsRune(parsed.DialTarget(), 0) {
			t.Fatalf("incomplete endpoint: %+v", parsed)
		}
		again, err := endpoint.Parse(parsed.DialTarget())
		if err != nil || again != parsed {
			t.Fatalf("dial target changed: initial=%+v next=%+v error=%v", parsed, again, err)
		}
	})
}
