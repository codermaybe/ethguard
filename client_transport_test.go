package ethguard

import (
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/config"
)

func TestDefaultClientRequiresExplicitWSSSelection(t *testing.T) {
	cfg := config.Config{Upstreams: []config.Upstream{{ID: "ws", Endpoint: "wss://provider.example"}}}
	if _, err := New(cfg); err == nil || !strings.Contains(err.Error(), "requires transport: wss") {
		t.Fatalf("New error = %v, want explicit WSS transport diagnostic", err)
	}
}
