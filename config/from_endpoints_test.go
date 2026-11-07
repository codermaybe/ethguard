package config

import (
	"strings"
	"testing"
)

// TestFromEndpointsDomain pins the programmatic construction path's domain:
// every error names the field path, the offending endpoint's index, and a
// fix hint; a returned Config passes Validate unchanged.
func TestFromEndpointsDomain(t *testing.T) {
	cases := []struct {
		name  string
		urls  []string
		want  string // substring of the expected error; empty means valid
		extra func(t *testing.T, cfg Config)
	}{
		{
			name: "no endpoints",
			urls: nil,
			want: "at least one endpoint is required",
		},
		{
			name: "empty endpoint",
			urls: []string{"https://a.example", "   "},
			want: `upstreams[1]: endpoint is empty`,
		},
		{
			name: "missing host",
			urls: []string{"https://a.example", "not-a-url"},
			want: `upstreams[1]: endpoint is not a valid URL`,
		},
		{
			name: "unsupported scheme",
			urls: []string{"ftp://a.example", "https://b.example"},
			want: `upstreams[0]: endpoint scheme "ftp" is not an Ethereum RPC transport`,
		},
		{
			name: "duplicate endpoint",
			urls: []string{"https://a.example", "https://a.example"},
			want: `upstreams[1]: endpoint duplicates an earlier entry`,
		},
		{
			name: "duplicate after trim",
			urls: []string{"https://a.example", " https://a.example "},
			want: "duplicates an earlier entry",
		},
		{
			name: "valid pair derives ids and transports",
			urls: []string{"https://a.example", "wss://b.example"},
			extra: func(t *testing.T, cfg Config) {
				t.Helper()
				if len(cfg.Upstreams) != 2 {
					t.Fatalf("upstreams = %d, want 2", len(cfg.Upstreams))
				}
				if cfg.Upstreams[0].ID != "rpc-1" || cfg.Upstreams[1].ID != "rpc-2" {
					t.Fatalf("derived ids = %q, %q; want rpc-1, rpc-2", cfg.Upstreams[0].ID, cfg.Upstreams[1].ID)
				}
				if cfg.Upstreams[0].Transport != TransportNativeHTTP {
					t.Fatalf("https transport = %q, want native-http", cfg.Upstreams[0].Transport)
				}
				if cfg.Upstreams[1].Transport != TransportWSS {
					t.Fatalf("wss transport = %q, want wss", cfg.Upstreams[1].Transport)
				}
			},
		},
		{
			name: "single endpoint is valid for the explicit Forwarded entry",
			urls: []string{"http://only.example:8545"},
			extra: func(t *testing.T, cfg Config) {
				t.Helper()
				if len(cfg.Upstreams) != 1 || cfg.Upstreams[0].Transport != TransportNativeHTTP {
					t.Fatalf("single-source config = %+v", cfg.Upstreams)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := FromEndpoints(tc.urls...)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("expected valid, got %v", err)
				}
				if err := cfg.Validate(); err != nil {
					t.Fatalf("returned config must pass Validate unchanged, got %v", err)
				}
				if tc.extra != nil {
					tc.extra(t, cfg)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}
