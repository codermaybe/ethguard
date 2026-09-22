package methods

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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

func TestLogsPolicyRejectsPendingBounds(t *testing.T) {
	p := LogsPolicy(func(context.Context) (uint64, error) { return 7, nil })
	for _, key := range []string{"fromBlock", "toBlock"} {
		_, err := p.Prepare(context.Background(), rpc.Request{Method: MethodGetLogs, Params: []any{
			map[string]any{key: "pending"},
		}})
		if !errors.Is(err, rpc.ErrInvalidRequest) {
			t.Fatalf("%s=pending: expected invalid request, got %v", key, err)
		}
	}
}

func TestLogsRejectPendingRangeLocally(t *testing.T) {
	p := LogsPolicy(func(context.Context) (uint64, error) {
		t.Fatal("pending range consulted the anchor")
		return 0, nil
	})
	filters := []map[string]any{
		{"fromBlock": "pending"},
		{"toBlock": "pending"},
		{"fromBlock": "pending", "toBlock": "pending"},
		{"fromBlock": "Pending", "toBlock": "0x10"},
		{"fromBlock": "0x1", "toBlock": "PENDING"},
		{"fromBlock": " pending ", "toBlock": "latest"},
		{"address": "0x0000000000000000000000000000000000000001", "fromBlock": "pending", "topics": []any{}},
		{"blockHash": "pending"},
		{"blockHash": "PENDING"},
		{"blockHash": "0xabc", "fromBlock": "pending"},
		{"blockHash": "0xabc", "toBlock": "Pending"},
		{"fromBlock": json.RawMessage(`"pending"`)},
		{"toBlock": json.RawMessage(`"PENDING"`)},
		{"fromBlock": map[string]any{"blockNumber": "pending"}},
	}
	for _, filter := range filters {
		_, err := p.Prepare(context.Background(), rpc.Request{Method: MethodGetLogs, Params: []any{filter}})
		if !errors.Is(err, rpc.ErrInvalidRequest) {
			t.Fatalf("filter %v: got %v, want ErrInvalidRequest", filter, err)
		}
		if err == nil || !strings.Contains(err.Error(), "no stable corroboration semantics") {
			t.Fatalf("filter %v: error %v must name the missing pending-range corroboration semantics", filter, err)
		}
		if errors.Is(err, rpc.ErrConflict) || errors.Is(err, rpc.ErrNoValidResponse) || errors.Is(err, rpc.ErrMalformedResponse) {
			t.Fatalf("filter %v: pending rejection classified as a business or provider failure: %v", filter, err)
		}
	}
}

func TestLogsRejectAbnormalPendingBoundsLocally(t *testing.T) {
	p := LogsPolicy(func(context.Context) (uint64, error) {
		t.Fatal("abnormal log bound consulted the anchor")
		return 0, nil
	})
	filters := []map[string]any{
		{"fromBlock": 1},
		{"toBlock": nil},
		{"fromBlock": ""},
		{"toBlock": "   "},
		{"fromBlock": true},
		{"fromBlock": json.RawMessage(`null`)},
		{"toBlock": json.RawMessage(`1`)},
		{"fromBlock": json.RawMessage(`not-json`)},
	}
	for _, filter := range filters {
		_, err := p.Prepare(context.Background(), rpc.Request{Method: MethodGetLogs, Params: []any{filter}})
		if !errors.Is(err, rpc.ErrInvalidRequest) {
			t.Fatalf("filter %v: got %v, want ErrInvalidRequest", filter, err)
		}
		if strings.Contains(err.Error(), "no stable corroboration semantics") {
			t.Fatalf("filter %v: non-pending bound mislabeled as pending: %v", filter, err)
		}
		if strings.Contains(err.Error(), "anchor:") {
			t.Fatalf("filter %v: local rejection escaped into anchor: %v", filter, err)
		}
	}
}

func TestLogsPendingDoesNotConsultAnchor(t *testing.T) {
	calls := 0
	p := LogsPolicy(func(context.Context) (uint64, error) {
		calls++
		return 7, nil
	})
	_, err := p.Prepare(context.Background(), rpc.Request{Method: MethodGetLogs, Params: []any{
		map[string]any{"fromBlock": "latest", "toBlock": "Pending"},
	}})
	if !errors.Is(err, rpc.ErrInvalidRequest) {
		t.Fatalf("got %v, want ErrInvalidRequest", err)
	}
	if calls != 0 {
		t.Fatalf("anchor calls = %d, want 0", calls)
	}
}

func TestLogsHistoricalRangePreparesWithoutPendingRejection(t *testing.T) {
	p := LogsPolicy(func(context.Context) (uint64, error) {
		t.Fatal("explicit historical range consulted the anchor")
		return 0, nil
	})
	prepared, err := p.Prepare(context.Background(), rpc.Request{Method: MethodGetLogs, Params: []any{
		map[string]any{"fromBlock": "0x1", "toBlock": "0x10", "address": "0x0000000000000000000000000000000000000001"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	filter := prepared.Params[0].(map[string]any)
	if filter["fromBlock"] != "0x1" || filter["toBlock"] != "0x10" {
		t.Fatalf("historical range rewritten: %+v", filter)
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
