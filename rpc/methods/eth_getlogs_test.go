package methods

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"ethguard/rpc"
)

func TestLogsPolicyRejectsMixedBlockSelectors(t *testing.T) {
	p := LogsPolicy(func(context.Context) (uint64, error) { return 7, nil })
	_, err := p.Prepare(context.Background(), rpc.Request{Method: MethodGetLogs, Params: []any{
		map[string]any{"blockHash": "0xabc", "fromBlock": "0x1"},
	}})
	if !errors.Is(err, rpc.ErrInvalidRequest) {
		t.Fatalf("expected invalid request, got %v", err)
	}
}

func TestNormalizeLogsKeepsOrderAndDuplicates(t *testing.T) {
	value := []byte(`[{"address":"0x0000000000000000000000000000000000000001","topics":[],"data":"0x","blockNumber":"0x1","transactionHash":"0x0000000000000000000000000000000000000000000000000000000000000001","transactionIndex":"0x0","blockHash":"0x0000000000000000000000000000000000000000000000000000000000000002","logIndex":"0x0","removed":false},{"address":"0x0000000000000000000000000000000000000001","topics":[],"data":"0x","blockNumber":"0x1","transactionHash":"0x0000000000000000000000000000000000000000000000000000000000000001","transactionIndex":"0x0","blockHash":"0x0000000000000000000000000000000000000000000000000000000000000002","logIndex":"0x0","removed":false}]`)
	got, err := normalizeLogs(value)
	if err != nil {
		t.Fatal(err)
	}
	if got == "[]" || len(got) == 0 {
		t.Fatalf("expected two normalized logs, got %s", got)
	}
	if got[0] != '[' || got[len(got)-1] != ']' {
		t.Fatalf("not an array: %s", got)
	}
}

func TestNormalizeLogsRejectsMalformedEntries(t *testing.T) {
	if _, err := normalizeLogs([]byte(`[null]`)); err == nil {
		t.Fatal("expected malformed log entry to be rejected")
	}
}

func TestLogsAdmissionRejectsIncompleteLog(t *testing.T) {
	p := LogsPolicy(func(context.Context) (uint64, error) { return 1, nil })
	admit := p.(rpc.AdmissionPolicy)
	_, err := admit.Admit(context.Background(), rpc.Request{Method: MethodGetLogs, Params: []any{map[string]any{}}}, json.RawMessage(`[{"address":"0x0000000000000000000000000000000000000001"}]`), nil)
	if !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("expected malformed response, got %v", err)
	}
}

func TestLogsAdmissionBindsBlockHash(t *testing.T) {
	p := LogsPolicy(func(context.Context) (uint64, error) { return 1, nil })
	row := `{"address":"0x0000000000000000000000000000000000000001","topics":[],"data":"0x","blockNumber":"0x1","transactionHash":"0x0000000000000000000000000000000000000000000000000000000000000001","transactionIndex":"0x0","blockHash":"0x0000000000000000000000000000000000000000000000000000000000000002","logIndex":"0x0","removed":false}`
	admit := p.(rpc.AdmissionPolicy)
	_, err := admit.Admit(context.Background(), rpc.Request{Method: MethodGetLogs, Params: []any{map[string]any{"blockHash": "0x0000000000000000000000000000000000000000000000000000000000000003"}}}, json.RawMessage("["+row+"]"), nil)
	if !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("expected block binding failure, got %v", err)
	}
}

func TestLogsAdmissionBindsRequestedRange(t *testing.T) {
	p := LogsPolicy(func(context.Context) (uint64, error) { return 1, nil })
	row := `{"address":"0x0000000000000000000000000000000000000001","topics":[],"data":"0x","blockNumber":"0x5","transactionHash":"0x0000000000000000000000000000000000000000000000000000000000000001","transactionIndex":"0x0","blockHash":"0x0000000000000000000000000000000000000000000000000000000000000002","logIndex":"0x0","removed":false}`
	admit := p.(rpc.AdmissionPolicy)
	_, err := admit.Admit(context.Background(), rpc.Request{Method: MethodGetLogs, Params: []any{map[string]any{"fromBlock": "0x1", "toBlock": "0x4"}}}, json.RawMessage("["+row+"]"), nil)
	if !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("expected range binding failure, got %v", err)
	}
}
