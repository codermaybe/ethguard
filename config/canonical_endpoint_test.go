package config

import (
	"strings"
	"testing"
)

// TestValidateRejectsNormalizedDuplicateEndpoints pins the duplicate-endpoint
// identity: entries that pool folds into one member (case, default port,
// trailing slash) must fail validation, or the declared voting basis silently
// shrinks below what the operator configured.
func TestValidateRejectsNormalizedDuplicateEndpoints(t *testing.T) {
	cases := []struct {
		name  string
		urls  [2]string
		valid bool
	}{
		{"default https port", [2]string{"https://node.example", "https://node.example:443"}, false},
		{"default http port", [2]string{"http://node.example", "http://node.example:80"}, false},
		{"default ws port", [2]string{"ws://node.example/x", "ws://node.example:80/x"}, false},
		{"default wss port", [2]string{"wss://node.example/x", "wss://node.example:443/x"}, false},
		{"scheme and host case", [2]string{"HTTPS://NODE.EXAMPLE/", "https://node.example"}, false},
		{"trailing slash", [2]string{"https://node.example/", "https://node.example"}, false},
		{"non-default port stays distinct", [2]string{"https://node.example:8443", "https://node.example"}, true},
		{"path stays distinct", [2]string{"https://node.example/a", "https://node.example/b"}, true},
		{"scheme stays distinct", [2]string{"https://node.example", "http://node.example"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{Upstreams: []Upstream{
				{ID: "a", Endpoint: tc.urls[0]},
				{ID: "b", Endpoint: tc.urls[1]},
			}}
			err := cfg.Validate()
			if tc.valid {
				if err != nil {
					t.Fatalf("expected valid, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "same member as upstreams[0]") {
				t.Fatalf("expected normalized-duplicate rejection naming the first index, got %v", err)
			}
		})
	}
}

// TestFromEndpointsRejectsNormalizedDuplicates keeps the programmatic path on
// the same identity rule.
func TestFromEndpointsRejectsNormalizedDuplicates(t *testing.T) {
	if _, err := FromEndpoints("HTTPS://NODE.EXAMPLE/", "https://node.example:443"); err == nil ||
		!strings.Contains(err.Error(), "duplicates an earlier entry after normalization") {
		t.Fatalf("expected normalized-duplicate rejection, got %v", err)
	}
}
