package ethguard_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/codermaybe/ethguard"
	"github.com/codermaybe/ethguard/rpctest"
)

// TestAnchorBlockHashForTagKeepsTagLevels pins the D5 adapter seam at the
// root: each moving tag anchors at its own level (observable on the wire as
// the original tag, with per-tag pins), while non-anchorable tags fail
// locally before any upstream contact.
func TestAnchorBlockHashForTagKeepsTagLevels(t *testing.T) {
	client, nodes := fleet(t, 1, 1, 3)
	pins := map[string]string{
		"latest":    "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"safe":      "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"finalized": "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
	}
	heights := map[string]string{"latest": "0x10", "safe": "0x20", "finalized": "0x18"}
	for _, node := range nodes {
		node.Handle("eth_getBlockByNumber", func(c rpctest.Call) (any, error) {
			var tag string
			if err := json.Unmarshal(c.Params[0], &tag); err != nil {
				return nil, err
			}
			pin, ok := pins[tag]
			if !ok {
				return nil, rpctest.RPCError(-32602, "unexpected anchor tag "+tag)
			}
			return map[string]any{"number": heights[tag], "hash": pin}, nil
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for tag, want := range pins {
		got, err := client.AnchorBlockHashForTag(ctx, tag)
		if err != nil {
			t.Fatalf("AnchorBlockHashForTag(%q): %v", tag, err)
		}
		if got != want {
			t.Fatalf("AnchorBlockHashForTag(%q) = %s, want %s (level downgraded?)", tag, got, want)
		}
	}
	seen := map[string]bool{}
	for _, node := range nodes {
		for _, c := range node.Calls() {
			if c.Method != "eth_getBlockByNumber" {
				continue
			}
			var tag string
			json.Unmarshal(c.Params[0], &tag)
			seen[tag] = true
		}
	}
	for tag := range pins {
		if !seen[tag] {
			t.Fatalf("no anchor establishment carrying tag %q", tag)
		}
	}

	for _, tag := range []string{"pending", "earliest", "0x10", "nope", ""} {
		if _, err := client.AnchorBlockHashForTag(ctx, tag); !errors.Is(err, ethguard.ErrInvalidRequest) {
			t.Fatalf("AnchorBlockHashForTag(%q): got %v, want ErrInvalidRequest", tag, err)
		}
	}
}
