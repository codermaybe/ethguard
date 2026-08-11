package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
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

// Validate checks that Config is usable by the upstream pool.
func (c Config) Validate() error {
	if len(c.Upstreams) == 0 {
		return fmt.Errorf("config: at least one upstream is required")
	}
	for i, upstream := range c.Upstreams {
		if upstream.Endpoint == "" {
			return fmt.Errorf("config: upstreams[%d]: endpoint is required", i)
		}
	}
	return nil
}
