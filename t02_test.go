package ethguard_test

import (
	"strings"
	"testing"

	"ethguard"
	"ethguard/config"
	"ethguard/rpc"
)

func TestT02SingleSourceDefaultsToForwarded(t *testing.T) {
	c, err := ethguard.New(config.Config{Upstreams: []config.Upstream{{ID: "solo", Endpoint: "http://solo.example"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if got := c.Mode(); got != rpc.CorroborationForwarded {
		t.Fatalf("single source default mode = %s, want forwarded", got)
	}
	if got := c.Corroboration().Floor; got != 0 {
		t.Fatalf("forwarded mode floor = %d, want zero", got)
	}
}

func TestT02SingleSourceExplicitCorroborationIsConfigurationError(t *testing.T) {
	cases := []config.Config{
		{Upstreams: []config.Upstream{{ID: "solo", Endpoint: "http://solo.example"}}, Corroboration: "majority"},
		{Upstreams: []config.Upstream{{ID: "solo", Endpoint: "http://solo.example"}}, Corroboration: "unanimity"},
		{Upstreams: []config.Upstream{{ID: "solo", Endpoint: "http://solo.example"}}, Quorum: 2},
	}
	for _, cfg := range cases {
		if _, err := ethguard.New(cfg); err == nil || !strings.Contains(err.Error(), "at least two") {
			t.Fatalf("config %+v: got %v, want insufficient-membership error", cfg, err)
		}
	}
}

func TestT02ForwardedHasNoSupportFloor(t *testing.T) {
	if _, err := ethguard.New(config.Config{
		Corroboration: "forwarded",
		Quorum:        2,
		Upstreams:     []config.Upstream{{ID: "a", Endpoint: "http://a.example"}, {ID: "b", Endpoint: "http://b.example"}},
	}); err == nil || !strings.Contains(err.Error(), "cannot specify quorum") {
		t.Fatalf("forwarded plus quorum: got %v", err)
	}
}

func TestT02RuntimeModeSwitchIsExplicit(t *testing.T) {
	c, err := ethguard.New(config.Config{Upstreams: []config.Upstream{
		{ID: "a", Endpoint: "http://a.example"},
		{ID: "b", Endpoint: "http://b.example"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if got := c.Mode(); got != rpc.CorroborationMajority {
		t.Fatalf("multi-source default mode = %s, want majority", got)
	}
	if err := c.SetCorroboration(rpc.CorroborationForwarded, 0); err != nil {
		t.Fatal(err)
	}
	if got := c.Mode(); got != rpc.CorroborationForwarded {
		t.Fatalf("after explicit switch mode = %s, want forwarded", got)
	}
	if err := c.SetCorroboration(rpc.CorroborationMajority, 2); err != nil {
		t.Fatal(err)
	}
	if got := c.Mode(); got != rpc.CorroborationMajority || c.Corroboration().Floor != 2 {
		t.Fatalf("switch back did not replace snapshot: %+v", c.Corroboration())
	}
}
