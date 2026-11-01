package ethguard

import (
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/config"
	nativehttp "github.com/codermaybe/ethguard/internal/transport/http"
)

func TestExplicitSourceTransportCannotBeOverriddenByInjectedDialer(t *testing.T) {
	for _, upstream := range []config.Upstream{
		{ID: "http", Endpoint: "http://127.0.0.1:1", Transport: config.TransportNativeHTTP},
		{ID: "wss", Endpoint: "ws://127.0.0.1:1", Transport: config.TransportWSS},
	} {
		t.Run(upstream.ID, func(t *testing.T) {
			cfg := config.Config{Upstreams: []config.Upstream{upstream}}
			if err := cfg.Validate(); err != nil {
				t.Fatal(err)
			}
			client, err := New(cfg, DialerOption(nativehttp.NewDialer()))
			if client != nil {
				client.Close()
				t.Fatal("ambiguous adapter selection returned a client")
			}
			if err == nil {
				t.Fatal("injected dialer silently overrode explicit source transport")
			}
		})
	}
}

func TestDefaultClientRequiresExplicitWSSSelection(t *testing.T) {
	cfg := config.Config{Upstreams: []config.Upstream{{ID: "ws", Endpoint: "wss://provider.example"}}}
	if _, err := New(cfg); err == nil || !strings.Contains(err.Error(), "requires transport: wss") {
		t.Fatalf("New error = %v, want explicit WSS transport diagnostic", err)
	}
}
