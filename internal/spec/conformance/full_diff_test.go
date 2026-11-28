package conformance

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCompleteDiffIncludesRequestsAndRepresentation(t *testing.T) {
	beforeResults := []byte(`{"version":"one","results":{"eth_x":{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"boolean"}}}}}`)
	afterResults := []byte(`{"version":"two","results":{"eth_x":{"type":"object","properties":{"b":{"type":"boolean"},"a":{"type":"string"}}}}}`)
	beforeRequests := []byte(`{"version":"one","requests":{"eth_x":[{"name":"first","required":true,"schema":{"type":"string","maxLength":4}},{"name":"second","required":false,"schema":{"type":"boolean"}}]}}`)
	afterRequests := []byte(`{"version":"two","requests":{"eth_x":[{"name":"second","required":false,"schema":{"type":"boolean"}},{"name":"first","required":true,"schema":{"type":"string","maxLength":3}}],"net_x":[]}}`)
	diff, err := DiffComplete(beforeResults, beforeRequests, afterResults, afterRequests)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Results) != 1 || len(diff.Requests) != 2 || !diff.Artifacts[0].Changed || !diff.Artifacts[1].Changed {
		t.Fatalf("incomplete diff: %+v", diff)
	}
	raw, err := json.Marshal(diff)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"object-order-changed", "/properties", "/0/name", "/0/required", "/1/schema/maxLength", "net_x"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("missing %s in %s", want, raw)
		}
	}
}
func TestCompleteDiffRejectsVersionSkewAndDuplicateJSON(t *testing.T) {
	results := []byte(`{"version":"one","results":{"eth_x":{"type":"string"}}}`)
	for _, requests := range []string{`{"version":"two","requests":{}}`, `{"version":"one","requests":{"eth_x":[],"eth_x":[]}}`} {
		if _, err := DiffComplete(results, []byte(requests), results, []byte(requests)); err == nil {
			t.Fatal("accepted skew or duplicate key")
		}
	}
}
