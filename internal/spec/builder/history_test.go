package builder

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var coverageAdded = []string{"net_listening", "net_peerCount", "eth_getBlockTransactionCountByHash", "eth_getBlockTransactionCountByNumber", "eth_getTransactionByBlockHashAndIndex"}

// Historical receipts are immutable. Reconstruct previous envelopes from
// unchanged source nodes, and retain retired overlay definitions as evidence.
func historicalRead(t *testing.T, name string, headers bool) []byte {
	t.Helper()
	disk := name
	if strings.HasPrefix(name, "errata/") {
		disk = "migrations/historical-errata/" + strings.TrimPrefix(name, "errata/")
	}
	if name == "vectors/SOURCE.json" {
		disk = "migrations/981aa66d-vector-source.json"
	}
	raw, err := os.ReadFile(filepath.Join("..", disk))
	if err != nil {
		t.Fatal(err)
	}
	if name != "methods.json" && name != "requests.json" {
		return raw
	}
	var b struct {
		Version  string                     `json:"version"`
		Results  map[string]json.RawMessage `json:"results,omitempty"`
		Requests map[string]json.RawMessage `json:"requests,omitempty"`
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatal(err)
	}
	for _, method := range coverageAdded {
		delete(b.Results, method)
	}
	if !headers {
		delete(b.Results, "eth_getHeaderByHash")
		delete(b.Results, "eth_getHeaderByNumber")
	}
	delete(b.Requests, "net_listening")
	delete(b.Requests, "net_peerCount")
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetIndent("", " ")
	if err := enc.Encode(b); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
