package config

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/codermaybe/ethguard/internal/endpoint"

	"gopkg.in/yaml.v3"
)

const (
	defaultCollectTimeout = 30 * time.Second
	defaultTipInterval    = 10 * time.Second
	defaultTipMaxBehind   = 32
	defaultFinalizedLag   = 32
)

// CanonicalEndpoint returns the shared endpoint identity used by configuration
// duplicate checks and pool membership. Relative IPC paths resolve against cwd.
func CanonicalEndpoint(raw string) (string, error) {
	parsed, err := endpoint.Parse(raw)
	if err != nil {
		return "", err
	}
	return parsed.CanonicalID(), nil
}

// LoadFile reads and parses a YAML configuration file.
func LoadFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("config: read %s: %w", path, err)
	}
	return ParseYAML(data)
}

// FromEndpoints builds and normalizes a configuration from distinct endpoints.
// It infers transports and diagnostic ids and freezes relative IPC paths now.
// IPC configuration is recognized before its concrete transport is available.
func FromEndpoints(urls ...string) (Config, error) {
	if len(urls) == 0 {
		return Config{}, fmt.Errorf("config: upstreams: at least one endpoint is required: one endpoint selects Forwarded mode; two or more distinct endpoints select Majority mode")
	}
	cfg := Config{Upstreams: make([]Upstream, len(urls))}
	for i, raw := range urls {
		cfg.Upstreams[i].Endpoint = raw
	}
	return Normalize(cfg)
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
	// String fields alone cannot distinguish an omitted id from an explicit
	// empty/null id. Preserve that YAML distinction only while loading; the
	// returned Config has one normalized representation and no hidden cache.
	var presence struct {
		Upstreams []map[string]any `yaml:"upstreams"`
	}
	if err := yaml.Unmarshal(data, &presence); err != nil {
		return Config{}, fmt.Errorf("config: parse yaml: %w", err)
	}
	for i, fields := range presence.Upstreams {
		if _, explicit := fields["id"]; explicit && strings.TrimSpace(cfg.Upstreams[i].ID) == "" {
			return Config{}, fmt.Errorf("config: upstreams[%d]: explicit id must not be empty", i)
		}
	}
	return Normalize(cfg)
}

// Validate checks the same domain as Normalize without modifying the receiver.
// To retain inferred fields and freeze a relative IPC path, use Normalize.
func (c Config) Validate() error {
	_, err := Normalize(c)
	return err
}

// Normalize returns an independently owned configuration with inferred ids and
// transports and frozen dial targets. Existing absolute targets remain stable
// across later validation/construction even if process cwd changes. No parsed
// cache can become inconsistent when a caller edits the returned public fields.
func Normalize(c Config) (Config, error) {
	if len(c.Upstreams) == 0 {
		return Config{}, fmt.Errorf("config: at least one upstream is required")
	}
	c.Upstreams = append([]Upstream(nil), c.Upstreams...)
	if c.Anchoring != nil {
		copy := *c.Anchoring
		c.Anchoring = &copy
	}
	if c.TipTracking != nil {
		copy := *c.TipTracking
		c.TipTracking = &copy
	}
	seenIDs := make(map[string]struct{}, len(c.Upstreams))
	for i, upstream := range c.Upstreams {
		if upstream.ID == "" {
			continue
		}
		if strings.TrimSpace(upstream.ID) == "" {
			return Config{}, fmt.Errorf("config: upstreams[%d]: id is required when explicitly provided", i)
		}
		if _, exists := seenIDs[upstream.ID]; exists {
			return Config{}, fmt.Errorf("config: upstreams[%d]: duplicate id %q", i, upstream.ID)
		}
		seenIDs[upstream.ID] = struct{}{}
	}
	seenEndpoints := make(map[string]int, len(c.Upstreams))
	nextID := 1
	for i := range c.Upstreams {
		upstream := &c.Upstreams[i]
		parsed, err := endpoint.Parse(upstream.Endpoint)
		if err != nil {
			return Config{}, fmt.Errorf("config: upstreams[%d]: %w", i, err)
		}
		inferred := Transport(parsed.Transport())
		if upstream.Transport != "" && upstream.Transport != inferred {
			switch upstream.Transport {
			case TransportNativeHTTP:
				return Config{}, fmt.Errorf("config: upstreams[%d]: transport %q requires an http(s) endpoint", i, upstream.Transport)
			case TransportWSS:
				return Config{}, fmt.Errorf("config: upstreams[%d]: transport %q requires a ws(s) endpoint", i, upstream.Transport)
			case TransportIPC:
				return Config{}, fmt.Errorf("config: upstreams[%d]: transport %q requires an IPC endpoint", i, upstream.Transport)
			default:
				return Config{}, fmt.Errorf("config: upstreams[%d]: unknown transport %q (want native-http, wss or ipc)", i, upstream.Transport)
			}
		}
		if first, exists := seenEndpoints[parsed.CanonicalID()]; exists {
			return Config{}, fmt.Errorf("config: upstreams[%d]: endpoint duplicates an earlier entry after normalization (same member as upstreams[%d]): use distinct independent providers", i, first)
		}
		seenEndpoints[parsed.CanonicalID()] = i
		upstream.Endpoint, upstream.Transport = parsed.DialTarget(), inferred
		if upstream.ID == "" {
			for {
				candidate := fmt.Sprintf("rpc-%d", nextID)
				nextID++
				if _, occupied := seenIDs[candidate]; occupied {
					continue
				}
				upstream.ID = candidate
				seenIDs[candidate] = struct{}{}
				break
			}
		}
	}
	if c.Anchoring != nil {
		preset := strings.ToLower(strings.TrimSpace(c.Anchoring.Preset))
		if preset != "" && preset != "strict" && preset != "finalized" {
			return Config{}, fmt.Errorf("config: anchoring.preset must be strict or finalized, got %q", c.Anchoring.Preset)
		}
	}

	if c.CollectTimeout != "" {
		if d, err := time.ParseDuration(c.CollectTimeout); err != nil || d <= 0 {
			return Config{}, fmt.Errorf("config: collect_timeout must be a positive duration: %q", c.CollectTimeout)
		}
	}
	if c.TipTracking != nil && c.TipTracking.Interval != "" {
		if d, err := time.ParseDuration(c.TipTracking.Interval); err != nil || d <= 0 {
			return Config{}, fmt.Errorf("config: tip_tracking.interval must be a positive duration: %q", c.TipTracking.Interval)
		}
	}
	return c, nil
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
