package config

// Config is the top-level EthGuard configuration. The same fields are
// available to YAML users and are mapped to the corresponding Go options by
// ethguard.New.
type Config struct {
	// ChainID optionally constrains upstreams to one Ethereum network. Zero
	// leaves chain identity validation to the caller.
	ChainID uint64 `yaml:"chain_id" json:"chain_id"`
	// Upstreams is the list of Ethereum JSON-RPC providers to manage.
	Upstreams []Upstream `yaml:"upstreams" json:"upstreams"`
	// Anchoring selects the moving-tag reference strategy. An omitted section
	// means strict anchoring with the default finalized lag.
	Anchoring *AnchoringConfig `yaml:"anchoring,omitempty" json:"anchoring,omitempty"`
	// AnchorCacheTTL enables reuse of an established anchor for this duration.
	// An empty value disables caching.
	AnchorCacheTTL string `yaml:"anchor_cache_ttl,omitempty" json:"anchor_cache_ttl,omitempty"`
	// TipTracking enables freshness-based source eligibility when present.
	TipTracking *TipTrackingConfig `yaml:"tip_tracking,omitempty" json:"tip_tracking,omitempty"`
	// Quorum is the corroboration support floor H. Zero selects the default
	// floor (DefaultSupportFloor, currently 2); positive values raise or
	// lower it explicitly.
	Quorum int `yaml:"quorum,omitempty" json:"quorum,omitempty"`
	// Corroboration selects the adjudication mode: "majority" (default),
	// "unanimity", or intentional single-source "forwarded". Spelling is case-insensitive and surrounding whitespace
	// is ignored. An empty value selects majority.
	Corroboration string `yaml:"corroboration,omitempty" json:"corroboration,omitempty"`
	// CollectTimeout bounds one aggregate collection. Empty uses 30 seconds.
	CollectTimeout string `yaml:"collect_timeout,omitempty" json:"collect_timeout,omitempty"`
}

// AnchoringConfig selects a moving-tag anchoring preset.
type AnchoringConfig struct {
	// Preset is "strict" (default) or "finalized"; spelling is
	// case-insensitive and surrounding whitespace is ignored. An empty
	// value selects strict.
	Preset string `yaml:"preset,omitempty" json:"preset,omitempty"`
	// MaxLag bounds how far a source's finalized view may trail the fleet's
	// reference before its view is excluded as stale. Zero selects the
	// documented default (32 blocks).
	MaxLag uint64 `yaml:"max_lag,omitempty" json:"max_lag,omitempty"`
}

// TipTrackingConfig configures the source freshness sampler.
type TipTrackingConfig struct {
	Interval  string `yaml:"interval,omitempty" json:"interval,omitempty"`
	MaxBehind uint64 `yaml:"max_behind,omitempty" json:"max_behind,omitempty"`
}

// Upstream describes a single RPC provider.
type Upstream struct {
	// ID is a stable, human-readable identifier for diagnostics and metrics.
	ID string `yaml:"id" json:"id"`
	// Endpoint is the RPC dial target (http(s) or ws(s)).
	Endpoint string `yaml:"endpoint" json:"endpoint"`
}
