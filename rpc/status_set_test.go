package rpc

import (
	"os"
	"strings"
	"testing"
)

// TestStatusStringSetExcludesFailed locks the confidence enum. Adding a
// Failed constant still compiles, and this test fails: "failed" belongs to
// observe.DecisionEvent, not to Status.
func TestStatusStringSetExcludesFailed(t *testing.T) {
	raw, err := os.ReadFile("types.go")
	if err != nil {
		t.Fatal(err)
	}
	const marker = "Unknown Status = iota"
	start := strings.Index(string(raw), marker)
	if start < 0 {
		t.Fatal("Status iota block not found")
	}
	rest := string(raw)[start:]
	end := strings.Index(rest, ")")
	if end < 0 {
		t.Fatal("Status iota block is unclosed")
	}
	var names []string
	for _, line := range strings.Split(rest[:end], "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		names = append(names, strings.Fields(line)[0])
	}
	wantNames := []string{"Unknown", "Corroborated", "Selected", "Forwarded"}
	if strings.Join(names, ",") != strings.Join(wantNames, ",") {
		t.Fatalf("Status constants = %v, want %v", names, wantNames)
	}

	got := map[string]Status{}
	for _, status := range []Status{Unknown, Corroborated, Selected, Forwarded} {
		name := status.String()
		if prev, ok := got[name]; ok {
			t.Fatalf("Status %s and %s both stringify to %q", prev, status, name)
		}
		got[name] = status
	}
	want := []string{"unknown", "corroborated", "selected", "forwarded"}
	if len(got) != len(want) {
		t.Fatalf("Status.String set = %v, want %v", got, want)
	}
	for _, name := range want {
		if _, ok := got[name]; !ok {
			t.Fatalf("Status.String set missing %q", name)
		}
	}
	if _, ok := got["failed"]; ok {
		t.Fatal("Status.String returned failed; that string is event-layer only")
	}
	if next := Status(Forwarded + 1).String(); next != "unknown" {
		t.Fatalf("Status(%d).String() = %q, want the default unknown", Forwarded+1, next)
	}
}
