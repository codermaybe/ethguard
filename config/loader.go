package config

import (
	"bytes"
	"fmt"
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

// LoadFile reads and parses a YAML configuration file.
func LoadFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("config: read %s: %w", path, err)
	}
	return ParseYAML(data)
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
	seenEndpoints := make(map[string]struct{}, len(c.Upstreams))
	for i, upstream := range c.Upstreams {
		if strings.TrimSpace(upstream.ID) == "" {
			return fmt.Errorf("config: upstreams[%d]: id is required", i)
		}
		if strings.TrimSpace(upstream.Endpoint) == "" {
			return fmt.Errorf("config: upstreams[%d]: endpoint is required", i)
		}
		if _, exists := seenIDs[upstream.ID]; exists {
			return fmt.Errorf("config: upstreams[%d]: duplicate id %q", i, upstream.ID)
		}
		seenIDs[upstream.ID] = struct{}{}
		// Duplicate endpoints silently collapse to one pool member at
		// construction, shrinking the voting basis below what the operator
		// configured. Reject the exact duplicates here; pool-side URL
		// normalization (scheme case, default ports) remains pool's domain
		// and is surfaced by Join, not reimplemented here.
		if _, exists := seenEndpoints[upstream.Endpoint]; exists {
			return fmt.Errorf("config: upstreams[%d]: duplicate endpoint %q", i, upstream.Endpoint)
		}
		seenEndpoints[upstream.Endpoint] = struct{}{}
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
