package rpcwire

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// frozenRequest measures the per-source request-encoding cost the frozen
// params route would remove. FreezeParams marshals every parameter exactly
// once at prepare time and hands the caller both the frozen elements and the
// joined array bytes; the production EncodeRequest then re-marshals the
// element list for every source, re-running the raw-JSON compaction on each
// element. encodeRequestWithFrozenArray is the splice prototype: the
// envelope is built once around the prepared array bytes, with only the
// per-transport request ID regenerated.
func encodeRequestWithFrozenArray(id json.RawMessage, method string, paramsJSON json.RawMessage) ([]byte, error) {
	var paramsField json.RawMessage
	if paramsJSON != nil {
		paramsField = paramsJSON
	}
	msg := struct {
		Version string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params,omitempty"`
	}{
		Version: Version,
		ID:      append(json.RawMessage(nil), id...),
		Method:  method,
		Params:  paramsField,
	}
	return json.Marshal(msg)
}

func frozenParamsJSON(size int) json.RawMessage {
	rawTx := strings.Repeat("ab", max(0, size/2-8))
	return json.RawMessage(`["0x02f8` + rawTx + `"]`)
}

func frozenParamsElements(paramsJSON json.RawMessage) []any {
	return []any{paramsJSON}
}

// BenchmarkRequestEncoding quantifies the per-source re-encoding of frozen
// elements against the prepared-array splice at the parameter sizes the
// fleet actually re-sends: small reads (address + tag), a receipt hash, and
// the transaction-blob scale where every broadcast member re-encodes the
// same payload.
func BenchmarkRequestEncoding(b *testing.B) {
	ids := NewIDGen()
	for _, size := range []int{64, 512, 131072} {
		paramsJSON := frozenParamsJSON(size)
		elements := frozenParamsElements(paramsJSON)
		b.Run(fmt.Sprintf("elements-remarshal-%dB", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			for range b.N {
				if _, err := EncodeRequest(ids.Next(), "eth_sendRawTransaction", elements); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("frozen-array-splice-%dB", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			for range b.N {
				if _, err := encodeRequestWithFrozenArray(ids.Next(), "eth_sendRawTransaction", paramsJSON); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
