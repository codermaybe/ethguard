package main

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/codermaybe/ethguard/internal/rpcwire"
)

func TestClassifyMapsProjectSentinels(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{name: "id-mismatch", err: rpcwire.ErrIDMismatch, want: "id_mismatch"},
		{name: "malformed", err: rpcwire.ErrMalformedResponse, want: "malformed_response"},
		{name: "empty", err: rpcwire.ErrEmptyResponse, want: "empty_response"},
		{name: "other", err: errors.New("dial tcp: refused"), want: "transport_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classify(tc.err); got != tc.want {
				t.Fatalf("classify = %q, want %q", got, tc.want)
			}
		})
	}
}

func okCall(name string) callResult {
	return callResult{Call: name, OK: true, Value: `"0x1"`}
}

func failCall(name, cls string) callResult {
	return callResult{Call: name, OK: false, ErrClass: cls, ErrMsg: "boom"}
}

func TestCompareVerdicts(t *testing.T) {
	calls := []string{"eth_chainId", "eth_blockNumber", "eth_getBlockByNumber", "eth_call_revert_probe"}
	mk := func(nativeFn, gethFn func(name string) callResult) providerReport {
		native := &adapterReport{}
		geth := &adapterReport{}
		for _, name := range calls {
			native.Calls = append(native.Calls, nativeFn(name))
			geth.Calls = append(geth.Calls, gethFn(name))
		}
		pr := providerReport{Adapters: map[string]*adapterReport{"native": native, "geth": geth}}
		pr.Conflicts, pr.Verdict = compare(pr)
		return pr
	}

	t.Run("identical-outcomes-are-compatible", func(t *testing.T) {
		pr := mk(func(name string) callResult { return okCall(name) },
			func(name string) callResult { return okCall(name) })
		if pr.Verdict != "compatible" || len(pr.Conflicts) != 0 {
			t.Fatalf("verdict = %s conflicts = %+v", pr.Verdict, pr.Conflicts)
		}
	})
	t.Run("identical-failures-are-unreachable", func(t *testing.T) {
		pr := mk(func(name string) callResult { return failCall(name, "transport_error") },
			func(name string) callResult { return failCall(name, "transport_error") })
		if pr.Verdict != "unreachable" || len(pr.Conflicts) != 0 {
			t.Fatalf("verdict = %s conflicts = %+v", pr.Verdict, pr.Conflicts)
		}
	})
	t.Run("geth-ok-native-fail-is-contract-conflict", func(t *testing.T) {
		pr := mk(func(name string) callResult { return failCall(name, "id_mismatch") },
			func(name string) callResult { return okCall(name) })
		if pr.Verdict != "contract_conflict" || len(pr.Conflicts) != len(calls) {
			t.Fatalf("verdict = %s conflicts = %d", pr.Verdict, len(pr.Conflicts))
		}
		if pr.Conflicts[0].Class != "id_mismatch" {
			t.Fatalf("class = %s, want id_mismatch", pr.Conflicts[0].Class)
		}
	})
	t.Run("divergent-failures-are-recorded", func(t *testing.T) {
		// Both fail, but differently: the ankr shape — geth sees a protocol
		// error on a 200 body, native rejects the null id.
		pr := mk(func(name string) callResult { return failCall(name, "id_mismatch") },
			func(name string) callResult { return failCall(name, "jsonrpc_error") })
		if pr.Verdict != "contract_conflict" {
			t.Fatalf("verdict = %s, want contract_conflict", pr.Verdict)
		}
		for _, c := range pr.Conflicts {
			if c.Class != "divergent_failure" {
				t.Fatalf("conflict class = %s, want divergent_failure", c.Class)
			}
		}
	})
	t.Run("geth-only-failure-is-not-a-contract-conflict", func(t *testing.T) {
		pr := mk(func(name string) callResult { return okCall(name) },
			func(name string) callResult { return failCall(name, "transport_error") })
		if pr.Verdict != "mixed" || len(pr.Conflicts) != len(calls) {
			t.Fatalf("verdict = %s conflicts = %d", pr.Verdict, len(pr.Conflicts))
		}
		if pr.Conflicts[0].Class != "geth_only_failure" {
			t.Fatalf("class = %s, want geth_only_failure", pr.Conflicts[0].Class)
		}
	})
}

func TestCompareSingleAdapter(t *testing.T) {
	pr := providerReport{Adapters: map[string]*adapterReport{"native": {}}}
	conflicts, verdict := compare(pr)
	if verdict != "single_adapter" || conflicts != nil {
		t.Fatalf("verdict = %s conflicts = %+v", verdict, conflicts)
	}
}

func TestTruncateSanitizesControlCharacters(t *testing.T) {
	got := truncate("line1\nline2\twith\rtab", 100)
	if got != "line1 line2 with tab" {
		t.Fatalf("truncate = %q", got)
	}
	long := truncate(strings.Repeat("a", 50), 10)
	if !strings.HasPrefix(long, "aaaaaaaaaa") || !strings.HasSuffix(long, "…") || utf8.RuneCountInString(long) != 11 {
		t.Fatalf("truncate = %q", long)
	}
}
