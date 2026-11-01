package config

import (
	"strings"
	"testing"
)

func TestExplicitTransportEndpointValidation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		transport Transport
		endpoint  string
		wantError string
	}{
		{"native http", TransportNativeHTTP, "http://localhost:8545", ""},
		{"native https", TransportNativeHTTP, "https://provider.example", ""},
		{"raw ws", TransportWSS, "ws://localhost:8546", ""},
		{"raw wss", TransportWSS, "wss://provider.example", ""},
		{"HTTP cannot silently switch", TransportNativeHTTP, "wss://provider.example", "http(s)"},
		{"WSS cannot silently switch", TransportWSS, "https://provider.example", "ws(s)"},
		{"compatibility websocket may omit selection", "", "wss://provider.example", ""},
		{"unsupported default scheme", "", "ipc:///tmp/ethguard.ipc", "valid scheme and host"},
		{"unknown transport", "automatic", "https://provider.example", "unknown transport"},
		{"missing host", TransportWSS, "wss:///only-path", "scheme and host"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{Upstreams: []Upstream{{ID: "source", Endpoint: tc.endpoint, Transport: tc.transport}}}
			err := cfg.Validate()
			if tc.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("validation=%v want %q", err, tc.wantError)
			}
		})
	}
}

func TestYAMLTransportValidationMatchesProgrammaticConfig(t *testing.T) {
	_, err := ParseYAML([]byte("upstreams:\n  - id: ws\n    endpoint: https://provider.example\n    transport: wss\n"))
	if err == nil || !strings.Contains(err.Error(), "ws(s)") {
		t.Fatalf("YAML bypassed transport validation: %v", err)
	}
}
