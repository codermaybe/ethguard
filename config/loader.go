package config

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultCollectTimeout = 30 * time.Second
	defaultTipInterval    = 10 * time.Second
	defaultTipMaxBehind   = 32
	defaultFinalizedLag   = 32
)

// CanonicalEndpoint reduces one endpoint to the identity the upstream pool
// deduplicates by: lowercase scheme and host, an explicit default port
// stripped (80 for http/ws, 443 for https/wss), and a lone "/" path dropped.
// The rule mirrors pool's normalizeEndpointID; the two must stay in agreement,
// because a configuration whose declared upstreams collapse into fewer pool
// members would silently shrink the voting basis below what the operator
// believes they configured. Non-default ports and distinct paths stay
// significant — they address different listeners or nodes.
func CanonicalEndpoint(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("endpoint must include a scheme and host")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	if isDefaultPort(u.Scheme, u.Port()) {
		u.Host = u.Hostname()
		if strings.Contains(u.Host, ":") {
			u.Host = "[" + u.Host + "]" // IPv6 literal without port
		}
	}
	if u.Path == "/" {
		u.Path = ""
	}
	return u.String(), nil
}

// isDefaultPort reports whether port is the conventional listener for
// scheme. Empty means unspecified, never a default.
func isDefaultPort(scheme, port string) bool {
	switch scheme {
	case "http", "ws":
		return port == "80"
	case "https", "wss":
		return port == "443"
	default:
		return false
	}
}

// LoadFile reads and parses a YAML configuration file.
func LoadFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("config: read %s: %w", path, err)
	}
	return ParseYAML(data)
}

// FromEndpoints builds a Config whose upstreams dial exactly the given RPC
// endpoints — the programmatic counterpart of a minimal YAML file. Every
// entry gets a derived display id ("rpc-1", "rpc-2", ...) and the transport
// its scheme implies (http(s) → native-http, ws(s) → wss), then the result
// passes the same Validate as the YAML path, so a Config returned here is
// usable by ethguard.New without a second failure mode.
//
// Errors carry the field path, the offending endpoint's index, and a fix
// hint: "config: upstreams[1]: endpoint scheme "ftp" is not an Ethereum RPC
// transport: use http, https, ws, or wss". Duplicate endpoints fail for the
// same reason the YAML path rejects them — exact duplicates silently shrink
// the voting basis below what the caller believes it configured.
func FromEndpoints(urls ...string) (Config, error) {
	if len(urls) == 0 {
		return Config{}, fmt.Errorf("config: upstreams: at least one endpoint is required: list two or more distinct endpoints for corroboration, or dial one endpoint explicitly with ethguard.DialUncorroborated")
	}
	cfg := Config{Upstreams: make([]Upstream, 0, len(urls))}
	seen := make(map[string]struct{}, len(urls))
	for i, raw := range urls {
		endpoint := strings.TrimSpace(raw)
		if endpoint == "" {
			return Config{}, fmt.Errorf("config: upstreams[%d]: endpoint is empty: provide an http(s) or ws(s) RPC URL, e.g. https://node.example:8545", i)
		}
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Host == "" {
			return Config{}, fmt.Errorf("config: upstreams[%d]: endpoint %q is not a valid URL: include a scheme and host, e.g. https://node.example:8545", i, endpoint)
		}
		var transport Transport
		switch parsed.Scheme {
		case "http", "https":
			transport = TransportNativeHTTP
		case "ws", "wss":
			transport = TransportWSS
		default:
			return Config{}, fmt.Errorf("config: upstreams[%d]: endpoint scheme %q is not an Ethereum RPC transport: use http, https, ws, or wss", i, parsed.Scheme)
		}
		canonical, err := CanonicalEndpoint(endpoint)
		if err != nil {
			return Config{}, fmt.Errorf("config: upstreams[%d]: endpoint %q: %v", i, endpoint, err)
		}
		if _, dup := seen[canonical]; dup {
			return Config{}, fmt.Errorf("config: upstreams[%d]: endpoint %q duplicates an earlier entry after normalization: use distinct independent providers — a repeated URL (including only case, default-port, or trailing-slash spelling differences) shrinks the voting basis without adding a source", i, endpoint)
		}
		seen[canonical] = struct{}{}
		cfg.Upstreams = append(cfg.Upstreams, Upstream{
			ID:        fmt.Sprintf("rpc-%d", i+1),
			Endpoint:  endpoint,
			Transport: transport,
		})
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// ParseYAML unmarshals YAML bytes into Config and runs basic validation.
// Unknown fields are rejected: silently ignoring a misspelled security
// knob (decision 0004) is worse than failing fast at startup.
func ParseYAML(data []byte) (Config, error) {
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("config: parse yaml: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate checks that Config is usable by the upstream pool and that every
// option has the same domain accepted by ethguard.New.
func (c Config) Validate() error {
	if len(c.Upstreams) == 0 {
		return fmt.Errorf("config: at least one upstream is required")
	}
	seenIDs := make(map[string]struct{}, len(c.Upstreams))
	seenEndpoints := make(map[string]int, len(c.Upstreams))
	for i, upstream := range c.Upstreams {
		if strings.TrimSpace(upstream.ID) == "" {
			return fmt.Errorf("config: upstreams[%d]: id is required", i)
		}
		if strings.TrimSpace(upstream.Endpoint) == "" {
			return fmt.Errorf("config: upstreams[%d]: endpoint is required", i)
		}
		endpoint, parseErr := url.Parse(upstream.Endpoint)
		if parseErr != nil || endpoint.Host == "" {
			return fmt.Errorf("config: upstreams[%d]: endpoint must include a valid scheme and host", i)
		}
		if upstream.Transport != "" {
			switch upstream.Transport {
			case TransportNativeHTTP:
				if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
					return fmt.Errorf("config: upstreams[%d]: transport %q requires an http(s) endpoint", i, upstream.Transport)
				}
			case TransportWSS:
				if endpoint.Scheme != "ws" && endpoint.Scheme != "wss" {
					return fmt.Errorf("config: upstreams[%d]: transport %q requires a ws(s) endpoint", i, upstream.Transport)
				}
			default:
				return fmt.Errorf("config: upstreams[%d]: unknown transport %q (want native-http or wss)", i, upstream.Transport)
			}
		} else if endpoint.Scheme != "http" && endpoint.Scheme != "https" && endpoint.Scheme != "ws" && endpoint.Scheme != "wss" {
			return fmt.Errorf("config: upstreams[%d]: endpoint scheme %q requires an explicit supported transport", i, endpoint.Scheme)
		}
		if _, exists := seenIDs[upstream.ID]; exists {
			return fmt.Errorf("config: upstreams[%d]: duplicate id %q", i, upstream.ID)
		}
		seenIDs[upstream.ID] = struct{}{}
		// Duplicate endpoints silently collapse to one pool member at
		// construction, shrinking the voting basis below what the operator
		// configured. Reject duplicates by the same identity the pool
		// deduplicates by — normalization folds scheme and host case, default
		// ports (80/443) and a trailing "/" — not by the raw string, which
		// would let spelling variants of one node pass as independent
		// sources. Pool-side URL normalization remains pool's domain; this
		// check must mirror it, which TestNormalizeEndpointIDMatchesConfigCanonical pins.
		canonical, canonErr := CanonicalEndpoint(upstream.Endpoint)
		if canonErr != nil {
			return fmt.Errorf("config: upstreams[%d]: endpoint %q: %v", i, upstream.Endpoint, canonErr)
		}
		if first, exists := seenEndpoints[canonical]; exists {
			return fmt.Errorf("config: upstreams[%d]: endpoint %q is the same member as upstreams[%d] after normalization: use distinct independent providers — a repeated URL shrinks the voting basis without adding a source", i, upstream.Endpoint, first)
		}
		seenEndpoints[canonical] = i
	}
	if c.Anchoring != nil {
		preset := strings.ToLower(strings.TrimSpace(c.Anchoring.Preset))
		if preset != "" && preset != "strict" && preset != "finalized" {
			return fmt.Errorf("config: anchoring.preset must be strict or finalized, got %q", c.Anchoring.Preset)
		}
	}
	if c.AnchorCacheTTL != "" {
		if d, err := time.ParseDuration(c.AnchorCacheTTL); err != nil || d <= 0 {
			return fmt.Errorf("config: anchor_cache_ttl must be a positive duration: %q", c.AnchorCacheTTL)
		}
	}
	if c.CollectTimeout != "" {
		if d, err := time.ParseDuration(c.CollectTimeout); err != nil || d <= 0 {
			return fmt.Errorf("config: collect_timeout must be a positive duration: %q", c.CollectTimeout)
		}
	}
	if c.TipTracking != nil && c.TipTracking.Interval != "" {
		if d, err := time.ParseDuration(c.TipTracking.Interval); err != nil || d <= 0 {
			return fmt.Errorf("config: tip_tracking.interval must be a positive duration: %q", c.TipTracking.Interval)
		}
	}
	return nil
}

// DefaultCollectTimeout is the collect window applied when configuration
// omits collect_timeout. Composition roots should use this rather than
// duplicating the constant.
func DefaultCollectTimeout() time.Duration { return defaultCollectTimeout }

// DefaultTipInterval is the tip-sampling period applied when configuration
// omits tip_tracking.interval.
func DefaultTipInterval() time.Duration { return defaultTipInterval }

// DefaultTipMaxBehind is the lag, in blocks, beyond which a source is
// treated as stale when configuration omits tip_tracking.max_behind.
func DefaultTipMaxBehind() uint64 { return defaultTipMaxBehind }

// DefaultFinalizedLag is the maximum distance from the fleet's finalized
// reference that an anchored read may lag when configuration omits
// anchoring.max_lag.
func DefaultFinalizedLag() uint64 { return defaultFinalizedLag }
