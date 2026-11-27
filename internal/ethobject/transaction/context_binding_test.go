package transaction

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

func TestCandidateBindingsPreserveExactContextKeys(t *testing.T) {
	raw, err := fixtureBytes()
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := Prepare(raw)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	var hash, blockHash, index string
	for key, target := range map[string]*string{"hash": &hash, "blockHash": &blockHash, "transactionIndex": &index} {
		if err := json.Unmarshal(fields[key], target); err != nil {
			t.Fatal(err)
		}
	}
	byHash := rpc.Request{Params: []any{hash}}
	inBlock := rpc.Request{Params: []any{blockHash, index}}
	for _, key := range []string{"hash", "from", "blockHash", "blockNumber", "transactionIndex"} {
		alias := strings.ToUpper(key)
		for _, first := range []bool{true, false} {
			t.Run(alias+map[bool]string{true: "/first", false: "/last"}[first], func(t *testing.T) {
				base := bytes.TrimSpace(raw)
				addition := []byte(`"` + alias + `":"invalid"`)
				var withAlias []byte
				if first {
					withAlias = append(append(append([]byte{'{'}, addition...), ','), base[1:]...)
				} else {
					withAlias = append(append(append(bytes.Clone(base[:len(base)-1]), ','), addition...), '}')
				}
				candidate, err := Prepare(withAlias)
				if err != nil || !bytes.Equal(candidate.Canonical(), baseline.Canonical()) {
					t.Fatalf("case alias changed the exact-key view: %v", err)
				}
				if err := candidate.AdmitByHash(byHash); err != nil {
					t.Fatal(err)
				}
				if err := candidate.AdmitInBlock(inBlock); err != nil {
					t.Fatal(err)
				}
			})
		}
		original := fields[key]
		delete(fields, key)
		fields[alias] = original
		missing, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Prepare(missing); !errors.Is(err, rpc.ErrMalformedResponse) {
			t.Fatalf("alias %q supplied missing exact key %q: %v", alias, key, err)
		}
		delete(fields, alias)
		fields[key] = original
	}
}

func TestCandidateInBlockRequestErrorPrecedesPayloadValidation(t *testing.T) {
	forged, err := editFixture(func(fields map[string]json.RawMessage) {
		fields["hash"] = json.RawMessage(`"0x` + strings.Repeat("11", 32) + `"`)
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := Prepare(forged)
	if err != nil {
		t.Fatal(err)
	}
	for _, params := range [][]any{nil, {"invalid", "0x0"}, {"0x" + strings.Repeat("22", 32), "invalid"}} {
		if err := candidate.AdmitInBlock(rpc.Request{Params: params}); !errors.Is(err, rpc.ErrInvalidRequest) || errors.Is(err, rpc.ErrMalformedResponse) {
			t.Fatalf("request grammar lost priority: %v", err)
		}
	}
}
