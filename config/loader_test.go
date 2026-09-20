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

// TestValidateDomainMatchesEthguardNew pins the contract both construction
// paths share: a Config that passes Validate must be usable by ethguard.New
// without a second failure mode, so every domain decision lives here.
func TestValidateDomainMatchesEthguardNew(t *testing.T) {
	upstreams := []Upstream{
		{ID: "a", Endpoint: "https://a.example"},
		{ID: "b", Endpoint: "https://b.example"},
	}
	cases := []struct {
		name string
		cfg  Config
		want string // substring of the expected error; empty means valid
	}{
		{"missing upstreams", Config{}, "at least one upstream"},
		{"missing id", Config{Upstreams: []Upstream{{Endpoint: "https://a.example"}}}, "id is required"},
		{"whitespace id", Config{Upstreams: []Upstream{{ID: "  ", Endpoint: "https://a.example"}}}, "id is required"},
		{"missing endpoint", Config{Upstreams: []Upstream{{ID: "a"}}}, "endpoint is required"},
		{"whitespace endpoint", Config{Upstreams: []Upstream{{ID: "a", Endpoint: "  "}}}, "endpoint is required"},
		{"duplicate endpoint", Config{Upstreams: []Upstream{
			{ID: "a", Endpoint: "https://a.example"},
			{ID: "b", Endpoint: "https://a.example"},
		}}, "duplicate endpoint"},
		{"negative quorum", Config{Upstreams: upstreams, Quorum: -1}, "non-negative"},
		{"unknown preset", Config{Upstreams: upstreams, Anchoring: &AnchoringConfig{Preset: "aggressive"}}, "must be strict or finalized"},
		{"unknown corroboration", Config{Upstreams: upstreams, Corroboration: "weighted"}, "majority or unanimity"},
		{"valid corroboration spellings", Config{Upstreams: upstreams, Corroboration: " UNANIMITY "}, ""},
		{"non-positive anchor ttl", Config{Upstreams: upstreams, AnchorCacheTTL: "-5s"}, "anchor_cache_ttl"},
		{"garbage anchor ttl", Config{Upstreams: upstreams, AnchorCacheTTL: "soon"}, "anchor_cache_ttl"},
		{"non-positive collect timeout", Config{Upstreams: upstreams, CollectTimeout: "0s"}, "collect_timeout"},
		{"garbage collect timeout", Config{Upstreams: upstreams, CollectTimeout: "later"}, "collect_timeout"},
		{"non-positive tip interval", Config{Upstreams: upstreams, TipTracking: &TipTrackingConfig{Interval: "0s"}}, "tip_tracking.interval"},
		{"garbage tip interval", Config{Upstreams: upstreams, TipTracking: &TipTrackingConfig{Interval: "sometimes"}}, "tip_tracking.interval"},
		{"valid minimal", Config{Upstreams: upstreams}, ""},
		{"valid preset spellings", Config{Upstreams: upstreams, Anchoring: &AnchoringConfig{Preset: " FINALIZED "}}, ""},
		{"valid every knob", Config{
			Upstreams:      upstreams,
			Anchoring:      &AnchoringConfig{Preset: "strict", MaxLag: 16},
			AnchorCacheTTL: "30s",
			TipTracking:    &TipTrackingConfig{Interval: "5s", MaxBehind: 8},
			Quorum:         2,
			Corroboration:  "majority",
			CollectTimeout: "10s",
		}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("expected valid, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

// TestParseYAMLValidates pins that the YAML path cannot bypass the
// domain checks the programmatic path enforces (decision 0004).
func TestParseYAMLValidates(t *testing.T) {
	if _, err := ParseYAML([]byte("upstreams: []")); err == nil {
		t.Fatal("expected empty-upstream YAML to fail validation")
	}
	cfg, err := ParseYAML([]byte(
		"chain_id: 1\n" +
			"upstreams:\n  - id: a\n    endpoint: https://a.example\n"))
	if err != nil || cfg.ChainID != 1 || len(cfg.Upstreams) != 1 {
		t.Fatalf("expected minimal YAML to load, got %+v err=%v", cfg, err)
	}
}

// TestParseYAMLRejectsUnknownFields pins strict decoding: a misspelled
// security knob must fail fast instead of silently loading defaults.
func TestParseYAMLRejectsUnknownFields(t *testing.T) {
	input := "chain_id: 1\ncorroboratin: majority\nupstreams:\n  - id: a\n    endpoint: https://a.example\n"
	if _, err := ParseYAML([]byte(input)); err == nil || !strings.Contains(err.Error(), "parse yaml") {
		t.Fatalf("expected unknown-field YAML to fail parsing, got %v", err)
	}
}

func TestDefaultSupportFloorIsTwo(t *testing.T) {
	if got := DefaultSupportFloor(); got != 2 {
		t.Fatalf("decision 0029's default H is 2, got %d", got)
	}
}
