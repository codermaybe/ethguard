package contract

import (
	"encoding/json"
	"testing"
)

// The harness is part of the contract surface: its own machinery must be
// correct for every adapter verdict to mean anything.
func TestEnvelopeEchoesRecordedRequestID(t *testing.T) {
	status, body := envelope([]byte(`{"jsonrpc":"2.0","id":17,"method":"eth_chainId"}`), `"0x1"`)
	if status != 0 {
		t.Fatalf("status = %d, want 0 (implicit 200)", status)
	}
	var wire struct {
		ID     json.RawMessage `json:"id"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatalf("envelope body %s is not JSON: %v", body, err)
	}
	if string(wire.ID) != "17" || string(wire.Result) != `"0x1"` {
		t.Fatalf("envelope = %s, want id 17 echoed with the result", body)
	}
}

func TestRequestIDMissingOrBrokenEchoesNull(t *testing.T) {
	for _, body := range []string{`{}`, `not json`, `{"id":null}`} {
		if got := requestID([]byte(body)); got != "null" {
			t.Fatalf("requestID(%s) = %s, want null", body, got)
		}
	}
}
