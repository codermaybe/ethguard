package ethguard

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/config"
	"github.com/codermaybe/ethguard/rpctest"
)

// TestTipTrackingConfigPathMatchesGoOptionPath pins decision 0004 at the
// behavioral level: a tip_tracking section in the configuration and the
// equivalent Go option must gate the same fleet identically — the lagging
// source falls out of fan-outs under either construction path.
func TestTipTrackingConfigPathMatchesGoOptionPath(t *testing.T) {
	// The same fleet must be gated identically after real YAML parsing and
	// Go construction. A 10-block lag survives the default 32 but not 4.
	addresses := []string{"0x64", "0x64", "0x5a"}
	fromYAML := func(cfg config.Config, maxBehind uint64, opts ...Option) (*Client, error) {
		var input strings.Builder
		fmt.Fprintf(&input, "chain_id: %d\ntip_tracking:\n  max_behind: %d\nupstreams:\n", cfg.ChainID, maxBehind)
		for _, upstream := range cfg.Upstreams {
			fmt.Fprintf(&input, "  - id: %q\n    endpoint: %q\n", upstream.ID, upstream.Endpoint)
		}
		parsed, err := config.ParseYAML([]byte(input.String()))
		if err != nil {
			return nil, err
		}
		return New(parsed, opts...)
	}
	paths := map[string]struct {
		build func(config.Config) (*Client, error)
		want  int
	}{
		"yaml explicit": {func(cfg config.Config) (*Client, error) { return fromYAML(cfg, 4) }, 2},
		"go explicit": {func(cfg config.Config) (*Client, error) {
			return New(cfg, WithTipTracking(0, 4))
		}, 2},
		"yaml defaults": {func(cfg config.Config) (*Client, error) { return fromYAML(cfg, 0) }, 3},
		"go defaults": {func(cfg config.Config) (*Client, error) {
			return New(cfg, WithTipTracking(0, 0))
		}, 3},
		"go overrides yaml": {func(cfg config.Config) (*Client, error) {
			return fromYAML(cfg, 0, WithTipTracking(0, 4))
		}, 2},
		"go zero restores default": {func(cfg config.Config) (*Client, error) {
			return fromYAML(cfg, 4, WithTipTracking(0, 0))
		}, 3},
	}
	for name, path := range paths {
		t.Run(name, func(t *testing.T) {
			cfg := config.Config{ChainID: 1}
			for i, addr := range addresses {
				node := rpctest.NewNode(1)
				t.Cleanup(node.Close)
				node.Return("eth_blockNumber", addr)
				cfg.Upstreams = append(cfg.Upstreams, config.Upstream{
					ID:       fmt.Sprintf("node-%c", 'a'+i),
					Endpoint: node.URL(),
				})
			}
			client, err := path.build(cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(client.Close)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			// First contact warms the connections; the probe observes them
			// and samples the tips that arm the freshness gate.
			if _, err := client.Sample(ctx, "eth_blockNumber"); err != nil {
				t.Fatalf("warm-up collect: %v", err)
			}
			client.pool.RefreshTips(ctx)

			ems, err := client.Sample(ctx, "eth_blockNumber")
			if err != nil {
				t.Fatalf("gated collect: %v", err)
			}
			if len(ems) != path.want {
				t.Fatalf("%s path: expected %d eligible sources, got %+v", name, path.want, ems)
			}
		})
	}
}
