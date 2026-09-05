package config

import (
	"strings"
	"testing"
)

func TestValidateRejectsDuplicateUpstreamDisplayNames(t *testing.T) {
	cfg := Config{Upstreams: []Upstream{
		{ID: "same", Endpoint: "https://a.example"},
		{ID: "same", Endpoint: "https://b.example"},
	}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate id") {
		t.Fatalf("expected duplicate display name error, got %v", err)
	}
}
