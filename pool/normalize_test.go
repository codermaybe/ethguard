package pool

import "testing"

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
