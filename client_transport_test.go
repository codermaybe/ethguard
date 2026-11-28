package ethguard

import (
	"testing"

	"github.com/codermaybe/ethguard/config"
)

func TestDefaultClientInfersWSSSelection(t *testing.T) {
	cfg := config.Config{Upstreams: []config.Upstream{{ID: "ws", Endpoint: "wss://provider.example"}}}
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New must infer WSS transport: %v", err)
	}
	t.Cleanup(client.Close)
}
