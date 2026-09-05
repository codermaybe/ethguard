package config

import (
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
func ParseYAML(data []byte) (Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
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
	for i, upstream := range c.Upstreams {
		if upstream.ID == "" {
			return fmt.Errorf("config: upstreams[%d]: id is required", i)
		}
		if upstream.Endpoint == "" {
			return fmt.Errorf("config: upstreams[%d]: endpoint is required", i)
		}
		if _, exists := seenIDs[upstream.ID]; exists {
			return fmt.Errorf("config: upstreams[%d]: duplicate id %q", i, upstream.ID)
		}
		seenIDs[upstream.ID] = struct{}{}
	}
	if c.Quorum < 0 {
		return fmt.Errorf("config: quorum must be non-negative")
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

// Defaults are exported as functions so composition roots can use exactly the
// same defaults as configuration loading without duplicating constants.
func DefaultCollectTimeout() time.Duration { return defaultCollectTimeout }
func DefaultTipInterval() time.Duration    { return defaultTipInterval }
func DefaultTipMaxBehind() uint64          { return defaultTipMaxBehind }
func DefaultFinalizedLag() uint64          { return defaultFinalizedLag }
