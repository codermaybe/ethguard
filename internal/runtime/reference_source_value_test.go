package runtime

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/internal/methods"
	"github.com/codermaybe/ethguard/internal/rpcengine"
	"github.com/codermaybe/ethguard/rpc"
)

func TestForwardedReferenceRestoresSourceWireSpelling(t *testing.T) {
	raw, hash := equivHeader(t, 0, "0xabcdef")
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	obj["hash"], _ = json.Marshal("0x" + strings.ToUpper(hash[2:]))
	raw, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := methods.SpecFor(methods.MethodGetBlockByNumber)
	resolver, err := methods.CompileResolver(spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	source := rpc.SourceRef{ID: "node", Name: "node", URL: "http://node"}
	response := rpc.Response{
		Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted},
		Answer:   rpc.Answer{Kind: rpc.AnswerResult, Result: raw},
	}
	policy := headAnchorPolicy{resolver: resolver.(rpcengine.CorroboratingResolver)}
	outcome, err := policy.ResolveCorroborated(rpcengine.Corroboration{Mode: rpc.CorroborationForwarded}, []rpc.Observation{{Attempted: true, Source: source, Response: response}})
	if err != nil {
		t.Fatal(err)
	}
	decision := outcome.Decision
	if decision.Status != rpc.Forwarded || decision.Representative != source || !bytes.Equal(decision.Value, raw) {
		t.Fatalf("forwarded reference lost source spelling or identity: %+v, value=%s", decision, decision.Value)
	}
	gotHash, gotHeight, err := blockRefFromDecision(decision)
	if err != nil || gotHash != hash || gotHeight != 0 {
		t.Fatalf("reference = %q/%d, %v; want %q/0", gotHash, gotHeight, err, hash)
	}
}
