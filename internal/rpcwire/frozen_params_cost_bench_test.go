package rpcwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// encodeRequestWithPreparedArray measures reuse of an already-prepared
// params array instead of rebuilding it from frozen elements per source.
// The envelope still uses json.Marshal, including its RawMessage compaction;
// this is not a byte-splicing prototype. Its caller owns preparation of the
// valid params array, just as FreezeParams does before a production fan-out.
func encodeRequestWithPreparedArray(id json.RawMessage, method string, paramsJSON json.RawMessage) ([]byte, error) {
	if _, ok := parseID(id); !ok {
		return nil, errors.New("rpcwire: request id must be a JSON number or string")
	}
	if method == "" {
		return nil, errors.New("rpcwire: request method must not be empty")
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
		Params:  paramsJSON,
	}
	out, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("rpcwire: encode request: %w", err)
	}
	return out, nil
}

func frozenParamsJSON(size int) json.RawMessage {
	rawTx := strings.Repeat("ab", max(0, (size-10)/2))
	return json.RawMessage(`["0x02f8` + rawTx + `"]`)
}

func frozenParamsElements(tb testing.TB, paramsJSON json.RawMessage) []any {
	tb.Helper()
	if paramsJSON == nil {
		return nil
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(paramsJSON, &rows); err != nil || rows == nil {
		tb.Fatalf("params fixture must be an array: %s, %v", paramsJSON, err)
	}
	elements := make([]any, len(rows))
	for i, row := range rows {
		elements[i] = row
	}
	return elements
}

func assertPreparedArrayRequestEqual(tb testing.TB, id json.RawMessage, method string, paramsJSON json.RawMessage) {
	tb.Helper()
	want, wantErr := EncodeRequest(id, method, frozenParamsElements(tb, paramsJSON))
	got, gotErr := encodeRequestWithPreparedArray(id, method, paramsJSON)
	if (gotErr == nil) != (wantErr == nil) || gotErr != nil && gotErr.Error() != wantErr.Error() {
		tb.Fatalf("prepared-array error = %v, want %v", gotErr, wantErr)
	}
	if !bytes.Equal(got, want) {
		tb.Fatalf("prepared-array request = %s, want %s", got, want)
	}
}

func TestPreparedArrayBenchmarkUsesEquivalentRequests(t *testing.T) {
	for _, fixture := range []struct {
		name   string
		params json.RawMessage
	}{
		{name: "omitted"},
		{name: "empty", params: json.RawMessage(`[]`)},
		{name: "transaction", params: frozenParamsJSON(512)},
		{name: "multiple", params: json.RawMessage(`["<>&\u2028",{"value":12345678901234567890},null,[true,false]]`)},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			for _, id := range []json.RawMessage{json.RawMessage(`1`), json.RawMessage(`"a\"b"`), json.RawMessage(`null`), json.RawMessage(`{}`)} {
				for _, method := range []string{"eth_sendRawTransaction", "", "method\"<>&"} {
					assertPreparedArrayRequestEqual(t, id, method, fixture.params)
				}
			}
		})
	}
}

// BenchmarkRequestEncoding quantifies the per-source re-encoding of frozen
// elements against reusing a prepared array, with the same synthetic string
// parameter at three sizes. Array preparation and byte-equivalence checks
// stay outside timing. Production marshals the element array once and joins
// it to a separately marshaled header. The prepared-array prototype still
// marshals the complete envelope; production does not consume that array API.
func BenchmarkRequestEncoding(b *testing.B) {
	for _, size := range []int{64, 512, 131072} {
		paramsJSON := frozenParamsJSON(size)
		elements := frozenParamsElements(b, paramsJSON)
		assertPreparedArrayRequestEqual(b, json.RawMessage(`1`), "eth_sendRawTransaction", paramsJSON)
		b.Run(fmt.Sprintf("elements-remarshal-%dB", size), func(b *testing.B) {
			ids := NewIDGen()
			b.SetBytes(int64(len(paramsJSON)))
			b.ReportAllocs()
			for range b.N {
				if _, err := EncodeRequest(ids.Next(), "eth_sendRawTransaction", elements); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("prepared-array-remarshal-%dB", size), func(b *testing.B) {
			ids := NewIDGen()
			b.SetBytes(int64(len(paramsJSON)))
			b.ReportAllocs()
			for range b.N {
				if _, err := encodeRequestWithPreparedArray(ids.Next(), "eth_sendRawTransaction", paramsJSON); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
