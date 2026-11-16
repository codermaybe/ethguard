package log

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

// The admission stage of eth_getLogs runs the shared kernel, then the request
// binding. An optional member whose canonical encoding fails stays out of that
// stage entirely: a binding error must not be masked by, or upgraded from, a
// timestamp the encode stage would later reject.

func logWithBadTimestamp() json.RawMessage {
	return json.RawMessage(`{"address":"0x` + strings.Repeat("11", 20) + `","topics":[],"data":"0x","blockNumber":"0x1",` +
		`"transactionHash":"0x` + strings.Repeat("22", 32) + `","transactionIndex":"0x0",` +
		`"blockHash":"0x` + strings.Repeat("44", 32) + `","logIndex":"0x0","removed":false,"blockTimestamp":"oops"}`)
}

func TestRequestBindingErrorPrecedesOptionalTimestamp(t *testing.T) {
	request := rpc.Request{Params: []any{map[string]any{
		"blockHash": "0x" + strings.Repeat("ff", 32), // does not match the log's blockHash
	}}}
	err := ValidateResponse(request, []byte(`[`+string(logWithBadTimestamp())+`]`))
	if err == nil || !strings.Contains(err.Error(), "does not match requested blockHash") {
		t.Fatalf("binding error must surface first, got %v", err)
	}
	if strings.Contains(err.Error(), "blockTimestamp") {
		t.Fatalf("the encode-stage timestamp must not mask the binding error, got %v", err)
	}
}

func TestAdmissionAcceptsLogWhoseOptionalTimestampIsMalformed(t *testing.T) {
	request := rpc.Request{Params: []any{map[string]any{
		"blockHash": "0x" + strings.Repeat("44", 32), // matches
	}}}
	if err := ValidateResponse(request, []byte(`[`+string(logWithBadTimestamp())+`]`)); err != nil {
		t.Fatalf("a binding-correct log with a malformed optional timestamp must pass admission, got %v", err)
	}
}

func TestBindingErrorsWithoutTimestampsKeepTheirClass(t *testing.T) {
	request := rpc.Request{Params: []any{map[string]any{
		"blockHash": "0x" + strings.Repeat("ff", 32),
	}}}
	cleanLog := strings.Replace(string(logWithBadTimestamp()), `,"blockTimestamp":"oops"`, "", 1)
	err := ValidateResponse(request, []byte(`[`+cleanLog+`]`))
	if err == nil || !strings.Contains(err.Error(), "does not match requested blockHash") {
		t.Fatalf("binding error class must not depend on the optional member, got %v", err)
	}
}
