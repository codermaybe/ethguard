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
	// TipTracking enables freshness-based source eligibility when present.
	TipTracking *TipTrackingConfig `yaml:"tip_tracking,omitempty" json:"tip_tracking,omitempty"`
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

// Transport selects a source's raw-call adapter at construction.
type Transport string

const (
	TransportNativeHTTP Transport = "native-http"
	TransportWSS        Transport = "wss"
	TransportIPC        Transport = "ipc"
)

// Upstream describes a single RPC provider.
type Upstream struct {
	// ID is a human-readable identifier for diagnostics and metrics. Empty
	// programmatic values and omitted YAML values receive an unused rpc-N name.
	ID string `yaml:"id" json:"id"`
	// Endpoint is a network URL or an explicit local IPC path. Configuration
	// loading freezes relative paths to an absolute target against process cwd.
	Endpoint string `yaml:"endpoint" json:"endpoint"`
	// Transport is inferred when empty. An explicit selection must match Endpoint.
	Transport Transport `yaml:"transport,omitempty" json:"transport,omitempty"`
}
