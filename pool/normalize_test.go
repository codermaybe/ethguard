package pool

import (
	"testing"

	"github.com/codermaybe/ethguard/config"
)

func TestNormalizeEndpointIDDedupsDefaultPorts(t *testing.T) {
	same := [][2]string{
		{"https://example.com/x", "https://example.com:443/x"},
		{"http://example.com", "http://example.com:80"},
		{"ws://example.com/node", "ws://example.com:80/node"},
		{"wss://example.com/node", "wss://example.com:443/node"},
		{"HTTPS://Example.COM/", "https://example.com"},
		{"https://example.com/", "https://example.com"},
	}
	for _, pair := range same {
		a, err := normalizeEndpointID(pair[0])
		if err != nil {
			t.Fatalf("normalize %q: %v", pair[0], err)
		}
		b, err := normalizeEndpointID(pair[1])
		if err != nil {
			t.Fatalf("normalize %q: %v", pair[1], err)
		}
		if a != b {
			t.Fatalf("expected %q and %q to share an id, got %q vs %q", pair[0], pair[1], a, b)
		}
	}

	distinct := [][2]string{
		{"https://example.com:8443/x", "https://example.com/x"},
		{"https://example.com/a", "https://example.com/b"},
		{"https://example.com", "http://example.com"},
	}
	for _, pair := range distinct {
		a, err := normalizeEndpointID(pair[0])
		if err != nil {
			t.Fatalf("normalize %q: %v", pair[0], err)
		}
		b, err := normalizeEndpointID(pair[1])
		if err != nil {
			t.Fatalf("normalize %q: %v", pair[1], err)
		}
		if a == b {
			t.Fatalf("expected %q and %q to differ, both got %q", pair[0], pair[1], a)
		}
	}

	for _, raw := range []string{"", "://bad", "example.com/no-scheme", "ftp://example.com/x", "http://"} {
		if _, err := normalizeEndpointID(raw); err == nil {
			t.Fatalf("expected %q to fail normalization", raw)
		}
	}
}

// TestNormalizeEndpointIDMatchesConfigCanonical pins the cross-package
// identity agreement: config's canonicalEndpoint must reduce endpoints to the
// same identity pool deduplicates by, or the composition backstop (declared
// upstream count vs pool membership) would fire on valid configurations.
func TestNormalizeEndpointIDMatchesConfigCanonical(t *testing.T) {
	same := [][2]string{
		{"https://example.com/x", "https://example.com:443/x"},
		{"http://example.com", "http://example.com:80"},
		{"HTTPS://Example.COM/", "https://example.com"},
		{"ws://example.com/node", "ws://example.com:80/node"},
		{"https://[2001:db8::1]:443", "https://[2001:db8::1]"},
	}
	for _, pair := range same {
		a, err := normalizeEndpointID(pair[0])
		if err != nil {
			t.Fatalf("normalize %q: %v", pair[0], err)
		}
		b, err := config.CanonicalEndpoint(pair[1])
		if err != nil {
			t.Fatalf("config canonical %q: %v", pair[1], err)
		}
		if a != b {
			t.Fatalf("pool and config disagree: %q -> %q vs %q -> %q", pair[0], a, pair[1], b)
		}
	}
}
