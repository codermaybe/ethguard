package chainlist

import (
	"os"
	"strings"
	"testing"
)

func TestParseAcceptsBothEntryShapes(t *testing.T) {
	f, err := os.Open("testdata/rpcs_fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	chains, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(chains) != 2 {
		t.Fatalf("expected 2 chains, got %d", len(chains))
	}
	mainnet := chains[0]
	if mainnet.ChainID != 1 || len(mainnet.RPCs) != 4 {
		t.Fatalf("unexpected mainnet entry: %+v", mainnet)
	}
	// String-form and object-form entries both survive; duplicate URLs are
	// the registry's data and stay as-is.
	if mainnet.RPCs[1].URL != "https://a.example.org" || mainnet.RPCs[1].Tracking != "" {
		t.Fatalf("string-form entry lost: %+v", mainnet.RPCs[1])
	}
	if mainnet.RPCs[3].Tracking != "yes" {
		t.Fatalf("object-form tracking lost: %+v", mainnet.RPCs[3])
	}
}

func TestForChainIsDeterministicAndCapped(t *testing.T) {
	f, err := os.Open("testdata/rpcs_fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	chains, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	eps := ForChain(chains, 1, 2)
	if len(eps) != 2 {
		t.Fatalf("expected cap of 2, got %d", len(eps))
	}
	if eps[0].URL != "https://a.example.org" || eps[1].URL != "https://a.example.org" {
		t.Fatalf("expected URL-sorted selection, got %+v", eps)
	}
	if got := ForChain(chains, 137, 0); len(got) != 1 || got[0].URL != "https://polygon.example.org" {
		t.Fatalf("unexpected other-chain selection: %+v", got)
	}
	if got := ForChain(chains, 999, 0); len(got) != 0 {
		t.Fatalf("expected empty for unknown chain, got %+v", got)
	}
}

func TestParseRejectsUnrecognizedEntries(t *testing.T) {
	// An entry that is neither an object with a URL nor a bare string must
	// fail the parse loudly — silently dropping endpoints would quietly
	// shrink the fleet under test.
	bad := strings.NewReader(`[{"name":"X","chainId":1,"rpc":[{"note":"neither"}]}]`)
	if _, err := Parse(bad); err == nil {
		t.Fatal("expected unrecognized rpc entry to fail parsing")
	}
}
