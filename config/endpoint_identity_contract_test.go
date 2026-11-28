package config_test

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/config"
)

func endpointYAML(endpoints ...string) []byte {
	var body strings.Builder
	body.WriteString("upstreams:\n")
	for _, endpoint := range endpoints {
		fmt.Fprintf(&body, "  - endpoint: %q\n", endpoint)
	}
	return []byte(body.String())
}

func TestInferredEndpointConfigurationReservesAllExplicitIDs(t *testing.T) {
	cfg, err := config.ParseYAML([]byte(`upstreams:
  - endpoint: https://first.example
  - id: rpc-3
    endpoint: ws://second.example
  - endpoint: https://third.example
  - id: rpc-1
    endpoint: wss://fourth.example
  - endpoint: http://fifth.example
`))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, source := range cfg.Upstreams {
		got = append(got, source.ID)
	}
	if want := []string{"rpc-2", "rpc-3", "rpc-4", "rpc-1", "rpc-5"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("assigned IDs=%v, want %v; later explicit IDs must be reserved first", got, want)
	}
	for i, want := range []config.Transport{config.TransportNativeHTTP, config.TransportWSS, config.TransportNativeHTTP, config.TransportWSS, config.TransportNativeHTTP} {
		if cfg.Upstreams[i].Transport != want {
			t.Errorf("source %d transport=%s, want %s", i, cfg.Upstreams[i].Transport, want)
		}
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("normalized configuration does not validate: %v", err)
	}
}

func TestInferredEndpointConfigurationRejectsExplicitIDCollisionsAndMismatches(t *testing.T) {
	for name, body := range map[string]string{
		"duplicate explicit IDs": "upstreams:\n  - id: same\n    endpoint: http://a.example\n  - id: same\n    endpoint: http://b.example\n",
		"empty ID":               "upstreams:\n  - id: ''\n    endpoint: http://a.example\n",
		"null ID":                "upstreams:\n  - id: null\n    endpoint: http://a.example\n",
		"whitespace ID":          "upstreams:\n  - id: '  '\n    endpoint: http://a.example\n",
		"HTTP declared WSS":      "upstreams:\n  - endpoint: http://a.example\n    transport: wss\n",
		"WSS declared HTTP":      "upstreams:\n  - endpoint: ws://a.example\n    transport: native-http\n",
		"HTTP declared IPC":      "upstreams:\n  - endpoint: http://a.example\n    transport: ipc\n",
		"IPC declared HTTP":      "upstreams:\n  - endpoint: /tmp/geth.ipc\n    transport: native-http\n",
		"unknown transport":      "upstreams:\n  - endpoint: http://a.example\n    transport: tcp\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := config.ParseYAML([]byte(body)); err == nil {
				t.Fatal("invalid explicit configuration was accepted")
			}
		})
	}
}

func TestEndpointIdentityPreservesEstablishedURLNormalization(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"HTTP://NODE.EXAMPLE:80/", "http://node.example"},
		{"HTTPS://NODE.EXAMPLE:443/", "https://node.example"},
		{"WS://NODE.EXAMPLE:80/rpc", "ws://node.example/rpc"},
		{"WSS://NODE.EXAMPLE:443/rpc", "wss://node.example/rpc"},
		{"https://NODE.EXAMPLE:8443/a/b?token=AbC#node", "https://node.example:8443/a/b?token=AbC#node"},
		{"http://[::1]:80/", "http://[::1]"},
		{"http://[::1]:8545/rpc", "http://[::1]:8545/rpc"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := config.CanonicalEndpoint(tc.input)
			if err != nil || got != tc.want {
				t.Fatalf("identity=%q err=%v, want %q", got, err, tc.want)
			}
			cfg, err := config.FromEndpoints(tc.input)
			if err != nil || len(cfg.Upstreams) != 1 {
				t.Fatalf("supported URL construction=%+v err=%v", cfg, err)
			}
		})
	}
	for _, pair := range [][2]string{
		{"https://node.example:8443", "https://node.example"},
		{"https://node.example/a", "https://node.example/b"},
		{"https://node.example?a=1", "https://node.example?a=2"},
		{"http://node.example", "https://node.example"},
	} {
		if cfg, err := config.FromEndpoints(pair[0], pair[1]); err != nil || len(cfg.Upstreams) != 2 {
			t.Fatalf("distinct URL endpoints=%v config=%+v err=%v", pair, cfg, err)
		}
	}
}

func TestEndpointIdentityRejectsAmbiguousAndMalformedInputs(t *testing.T) {
	for _, input := range []string{
		"", " ", "geth.ipc", "run/geth.ipc", "localhost:8545", "localhost:8545/path",
		"http:/node", "https:node", "https:///missing-host", "ws://", "ftp://node/path", "tcp://node:8545",
		"ipc://remote/tmp/geth.ipc", "ipc://user@remote/tmp/geth.ipc", "ipc:///tmp/geth.ipc?mode=x", "ipc:///tmp/geth.ipc#x", "ipc:///tmp/bad%escape",
		"ipc:///tmp/geth.ipc?", "ipc:///tmp/geth.ipc#", "ipc:///tmp/geth%00.ipc",
		`\\.\pipe\`, `//./pipe/`, `ipc://\\.\pipe\`, `ipc:////./pipe/`,
	} {
		t.Run(input, func(t *testing.T) {
			if _, err := config.CanonicalEndpoint(input); err == nil {
				t.Errorf("CanonicalEndpoint accepted %q", input)
			}
			if _, err := config.FromEndpoints(input); err == nil {
				t.Errorf("FromEndpoints accepted %q", input)
			}
			if _, err := config.ParseYAML(endpointYAML(input)); err == nil {
				t.Errorf("ParseYAML accepted %q", input)
			}
		})
	}
}

func TestWindowsPipeSpellingsShareOneCaseInsensitiveIdentity(t *testing.T) {
	inputs := []string{`\\.\pipe\GETH.IPC`, `//./pipe/Geth.Ipc`, `ipc://\\.\pipe\geth.ipc`, `ipc:////./pipe/GETH.ipc`}
	want := `ipc://\\.\pipe\geth.ipc`
	for _, input := range inputs {
		got, err := config.CanonicalEndpoint(input)
		if err != nil || got != want {
			t.Fatalf("pipe %q identity=%q err=%v, want %q", input, got, err, want)
		}
		cfg, err := config.FromEndpoints(input)
		if err != nil || len(cfg.Upstreams) != 1 || cfg.Upstreams[0].Transport != config.TransportIPC {
			t.Fatalf("pipe %q configuration=%+v err=%v", input, cfg, err)
		}
	}
	for i, first := range inputs {
		for _, second := range inputs[i+1:] {
			if _, err := config.FromEndpoints(first, second); err == nil {
				t.Fatalf("duplicate pipes accepted: %q / %q", first, second)
			}
			if _, err := config.ParseYAML(endpointYAML(first, second)); err == nil {
				t.Fatalf("YAML duplicate pipes accepted: %q / %q", first, second)
			}
		}
	}
}

func TestUnixIPCSpellingsDeduplicateWithoutRequiringExistingSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket path spelling")
	}
	base := t.TempDir()
	t.Chdir(base)
	path := filepath.Join(base, "geth.ipc")
	inputs := []string{"./geth.ipc", "./missing/../geth.ipc", path, "ipc://" + path}
	for _, input := range inputs {
		got, err := config.CanonicalEndpoint(input)
		if err != nil || got != "ipc://"+path {
			t.Fatalf("IPC %q identity=%q err=%v", input, got, err)
		}
	}
	for i, first := range inputs {
		for _, second := range inputs[i+1:] {
			if _, err := config.FromEndpoints(first, second); err == nil {
				t.Fatalf("duplicate sockets accepted: %q / %q", first, second)
			}
			if _, err := config.ParseYAML(endpointYAML(first, second)); err == nil {
				t.Fatalf("YAML duplicate sockets accepted: %q / %q", first, second)
			}
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("test expected no socket file, stat=%v", err)
	}
}

func TestParsedRelativeEndpointsStayFrozenAcrossWorkingDirectoryChanges(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix relative socket paths")
	}
	for _, entry := range []string{"FromEndpoints", "ParseYAML", "LoadFile", "NormalizeManualConfig"} {
		t.Run(entry, func(t *testing.T) {
			base, later := t.TempDir(), t.TempDir()
			t.Chdir(base)
			// Special URI characters and the trailing space are literal filename bytes.
			relative := "./geth #?% socket.ipc "
			wantPath := filepath.Join(base, "geth #?% socket.ipc ")
			var cfg config.Config
			var err error
			switch entry {
			case "NormalizeManualConfig":
				input := config.Config{Upstreams: []config.Upstream{{Endpoint: relative}}}
				cfg, err = config.Normalize(input)
				if input.Upstreams[0].Endpoint != relative || input.Upstreams[0].ID != "" || input.Upstreams[0].Transport != "" {
					t.Fatal("normalization mutated the caller's configuration")
				}
			case "FromEndpoints":
				cfg, err = config.FromEndpoints(relative)
			case "ParseYAML":
				cfg, err = config.ParseYAML(endpointYAML(relative))
			case "LoadFile":
				// The YAML directory intentionally differs from the process cwd.
				file := filepath.Join(later, "config.yaml")
				if err := os.WriteFile(file, endpointYAML(relative), 0o600); err != nil {
					t.Fatal(err)
				}
				cfg, err = config.LoadFile(file)
			}
			if err != nil || len(cfg.Upstreams) != 1 {
				t.Fatalf("parse=%+v err=%v", cfg, err)
			}
			if cfg.Upstreams[0].Transport != config.TransportIPC {
				t.Fatalf("transport=%s, want ipc", cfg.Upstreams[0].Transport)
			}
			before, err := config.CanonicalEndpoint(cfg.Upstreams[0].Endpoint)
			wantIdentity := (&url.URL{Scheme: "ipc", Path: wantPath}).String()
			if err != nil || before != wantIdentity {
				t.Fatalf("frozen endpoint=%q identity=%q err=%v", cfg.Upstreams[0].Endpoint, before, err)
			}
			t.Chdir(later)
			if err := cfg.Validate(); err != nil {
				t.Fatalf("frozen config stopped validating after chdir: %v", err)
			}
			after, err := config.CanonicalEndpoint(cfg.Upstreams[0].Endpoint)
			if err != nil || after != before {
				t.Fatalf("relative endpoint drifted: before=%q after=%q err=%v", before, after, err)
			}
		})
	}
}

func TestUnixIPCURIAndLiteralFilenameHaveTheSameIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket path spelling")
	}
	path := filepath.Join(t.TempDir(), "geth #?% socket.ipc ")
	uri := (&url.URL{Scheme: "ipc", Path: path}).String()
	literalID, err := config.CanonicalEndpoint(path)
	if err != nil {
		t.Fatal(err)
	}
	uriID, err := config.CanonicalEndpoint(uri)
	if err != nil || uriID != literalID {
		t.Fatalf("literal=%q URI=%q err=%v", literalID, uriID, err)
	}
	if _, err := config.FromEndpoints(path, uri); err == nil {
		t.Fatal("encoded IPC URI and literal filename counted as independent sources")
	}
}
