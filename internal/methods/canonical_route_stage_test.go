package methods

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

// These witnesses explain why local object reuse does not also move Block
// and Logs to ProcessCanonical: their existing admission and encoding hooks
// intentionally reject different sets of inputs.
func TestBlockHashWithoutFullTransactionsKeepsEncodingFailureStage(t *testing.T) {
	spec := getBlockByHashSpec()
	if spec.ProcessCanonical != nil || spec.Admit == nil || spec.Encode == nil {
		t.Fatal("block hash route no longer preserves distinct admission and encoding")
	}
	raw := json.RawMessage(`{"number":"0x1","hash":"bad","transactions":false}`)
	for _, fullTx := range []any{false, json.RawMessage(`false`)} {
		req := rpc.Request{Method: MethodGetBlockByHash, Params: []any{"0x" + strings.Repeat("11", 32), fullTx}}
		if _, err := spec.Admit(t.Context(), req, raw, nil); err != nil {
			t.Fatalf("hash-only request rejected during admission: %v", err)
		}
		if _, err := spec.Encode(raw); err == nil {
			t.Fatal("malformed source escaped the encoding check")
		}
	}
}

func TestLogsTimestampKeepsEncodingFailureStage(t *testing.T) {
	spec := getLogsSpec()
	if spec.ProcessCanonical != nil || spec.Admit == nil || spec.Encode == nil {
		t.Fatal("logs route no longer preserves distinct admission and encoding")
	}
	for _, name := range []string{"blockTimestamp", "BlockTimestamp"} {
		raw := json.RawMessage(strings.Replace(canonicalLogValue(), `"removed":false`, `"removed":false,"`+name+`":"bad"`, 1))
		req := rpc.Request{Method: MethodGetLogs, Params: []any{map[string]any{}}}
		if _, err := spec.Admit(t.Context(), req, raw, nil); err != nil {
			t.Fatalf("%s rejected during admission: %v", name, err)
		}
		if _, err := spec.Encode(raw); err == nil || !strings.Contains(err.Error(), "blockTimestamp") {
			t.Fatalf("%s encoding error=%v", name, err)
		}
	}
}
