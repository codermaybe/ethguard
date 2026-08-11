package config

// Config is the top-level EthGuard configuration.
type Config struct {
	// Upstreams is the list of Ethereum JSON-RPC providers to manage.
	Upstreams []Upstream `yaml:"upstreams" json:"upstreams"`
}

// Upstream describes a single RPC provider.
type Upstream struct {
	// ID is a stable, human-readable identifier for diagnostics and metrics.
	ID string `yaml:"id" json:"id"`
	// Endpoint is the RPC dial target (http(s) or ws(s)).
	Endpoint string `yaml:"endpoint" json:"endpoint"`
}
