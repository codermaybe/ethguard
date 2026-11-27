package methods

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/internal/ethcodec"
	"github.com/codermaybe/ethguard/internal/rpcengine"
	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpctest"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

// TestRepresentativeIsFirstCapturedSource pins the source selection rule:
// when two sources share a comparison key but spell the payload differently,
// Representative, Provenance, and withheld data come from the first source in
// captured order. A non-canonical value (identityKey) also keeps that source's
// bytes in Value, so swapping moves the bytes. eth_call's accepted data
// projection is canonical (0061): Representative moves but Value does not.
func TestRepresentativeIsFirstCapturedSource(t *testing.T) {
	a := rpc.SourceRef{Name: "a", ID: "a"}
	b := rpc.SourceRef{Name: "b", ID: "b"}

	t.Run("strict", func(t *testing.T) {
		policy := rpcengine.Strict(identityKey, rpcengine.WithQuorum(2))
		first := observation(a, `"0xA"`, `{"from":"a"}`)
		second := observation(b, `"0xa"`, `{"from":"b"}`)
		assertFirstSource(t, policy, first, second)
	})

	t.Run("call revert data returned", func(t *testing.T) {
		policy := planResolver(t, MethodCall, rpcengine.WithQuorum(2))
		first := revertObservation(a, `"0xDEAD"`, `{"from":"a"}`)
		second := revertObservation(b, `"0xdead"`, `{"from":"b"}`)
		decision := resolvePair(t, policy, first, second)
		if string(decision.Value) != `{"data":"0xdead"}` || decision.Revert == nil || decision.Revert.Code != 3 {
			t.Fatalf("value/context = %s/%+v", decision.Value, decision.Revert)
		}
		swapped := resolvePair(t, policy, second, first)
		if string(swapped.Value) != `{"data":"0xdead"}` || swapped.Revert == nil || swapped.Revert.Code != 3 {
			t.Fatalf("swapped value/context = %s/%+v", swapped.Value, swapped.Revert)
		}
		if decision.Representative != a || swapped.Representative != b {
			t.Fatalf("representatives = %+v/%+v", decision.Representative, swapped.Representative)
		}
	})

	t.Run("call revert data withheld", func(t *testing.T) {
		policy := planResolver(t, MethodCall, rpcengine.WithQuorum(2))
		// a and b assert the same data, so they form one data cluster; c and d
		// assert different data. N is 4, so the quorum is 3: the cluster of
		// two ranks first but falls short, and its data is withheld. The
		// representative is the first source of that cluster.
		c := rpc.SourceRef{Name: "c", ID: "c"}
		d := rpc.SourceRef{Name: "d", ID: "d"}
		first := revertObservation(a, `"0xAAAA"`, `{"from":"a"}`)
		second := revertObservation(b, `"0xaaaa"`, `{"from":"b"}`)
		other := revertObservation(c, `"0xBEEF"`, `{"from":"c"}`)
		fourth := revertObservation(d, `"0xbeef"`, `{"from":"d"}`)
		decision := resolvePair(t, policy, first, second, other, fourth)
		if bytes.Contains(decision.Value, []byte("0xAAAA")) || bytes.Contains(decision.Value, []byte("0xaaaa")) || bytes.Contains(decision.Value, []byte("0xBEEF")) {
			t.Fatalf("concrete data returned: %s", decision.Value)
		}
		if decision.Representative != a {
			t.Fatalf("representative = %+v, want a", decision.Representative)
		}
		if !bytes.Contains(decision.Provenance, []byte("0xAAAA")) || bytes.Contains(decision.Provenance, []byte("0xaaaa")) || bytes.Contains(decision.Provenance, []byte("0xBEEF")) {
			t.Fatalf("withheld = %s, want only a's data", decision.Provenance)
		}

		swapped := resolvePair(t, policy, second, first, other, fourth)
		if swapped.Representative != b {
			t.Fatalf("swapped representative = %+v, want b", swapped.Representative)
		}
		if !bytes.Contains(swapped.Provenance, []byte("0xaaaa")) || bytes.Contains(swapped.Provenance, []byte("0xAAAA")) {
			t.Fatalf("swapped withheld = %s, want only b's data", swapped.Provenance)
		}
		if bytes.Equal(decision.Provenance, swapped.Provenance) {
			t.Fatalf("swapping the captured order left the withheld bytes unchanged: %s", swapped.Provenance)
		}
	})
}

// TestRepresentativeWithoutEvidenceStaysEmpty pins the empty-evidence rule:
// when the first captured supporter has no admission evidence, the decision's
// evidence stays empty even though a later supporter of the same key has some.
func TestRepresentativeWithoutEvidenceStaysEmpty(t *testing.T) {
	a := rpc.SourceRef{Name: "a", ID: "a"}
	b := rpc.SourceRef{Name: "b", ID: "b"}

	t.Run("strict", func(t *testing.T) {
		policy := rpcengine.Strict(identityKey, rpcengine.WithQuorum(2))
		decision := resolvePair(t, policy,
			observation(a, `"0xA"`, ""),
			observation(b, `"0xa"`, `{"from":"b"}`),
		)
		if len(decision.Provenance) != 0 {
			t.Fatalf("evidence = %s, want empty", decision.Provenance)
		}
		if decision.Representative != a {
			t.Fatalf("representative = %+v, want a", decision.Representative)
		}
	})

	t.Run("call revert", func(t *testing.T) {
		policy := planResolver(t, MethodCall, rpcengine.WithQuorum(2))
		decision := resolvePair(t, policy,
			revertObservation(a, `"0xDEAD"`, ""),
			revertObservation(b, `"0xdead"`, `{"from":"b"}`),
		)
		if len(decision.Provenance) != 0 {
			t.Fatalf("evidence = %s, want empty", decision.Provenance)
		}
		if decision.Representative != a {
			t.Fatalf("representative = %+v, want a", decision.Representative)
		}
	})
}

func identityKey(value json.RawMessage) (string, error) {
	var text string
	if err := json.Unmarshal(value, &text); err != nil {
		return "", err
	}
	out := make([]byte, len(text))
	for i := range text {
		c := text[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out), nil
}

func observation(source rpc.SourceRef, value, evidence string) rpc.Observation {
	item := rpc.Observation{Attempted: true, Source: source, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(value)}}}
	if evidence != "" {
		item.Evidence = json.RawMessage(evidence)
	}
	return item
}

func revertObservation(source rpc.SourceRef, data, evidence string) rpc.Observation {
	body := &rpc.RPCError{Code: 3, Message: "execution reverted"}
	if data != "" {
		body.Data = json.RawMessage(data)
	}
	item := rpc.Observation{Attempted: true, Source: source, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerProtocolError, RPCError: body, Cause: rpctest.RPCError(3, "execution reverted")}}}
	if evidence != "" {
		item.Evidence = json.RawMessage(evidence)
	}
	return item
}

func assertFirstSource(t *testing.T, policy rpcengine.Resolver, first, second rpc.Observation) {
	t.Helper()
	decision := resolvePair(t, policy, first, second)
	if !bytes.Equal(decision.Value, payloadOf(first)) {
		t.Fatalf("value = %s, want first source %s", decision.Value, payloadOf(first))
	}
	if !bytes.Equal(decision.Provenance, first.Evidence) {
		t.Fatalf("evidence = %s, want %s", decision.Provenance, first.Evidence)
	}
	if decision.Representative != first.Source {
		t.Fatalf("representative = %+v, want %+v", decision.Representative, first.Source)
	}

	swapped := resolvePair(t, policy, second, first)
	if bytes.Equal(swapped.Value, decision.Value) {
		t.Fatalf("swapping the captured order left the value bytes unchanged: %s", swapped.Value)
	}
	if !bytes.Equal(swapped.Value, payloadOf(second)) {
		t.Fatalf("swapped value = %s, want %s", swapped.Value, payloadOf(second))
	}
	if swapped.Representative != second.Source {
		t.Fatalf("swapped representative = %+v, want %+v", swapped.Representative, second.Source)
	}
}

func resolvePair(t *testing.T, policy rpcengine.Resolver, obs ...rpc.Observation) rpc.Decision {
	t.Helper()
	resolver, ok := policy.(rpcengine.CorroboratingResolver)
	if !ok {
		t.Fatalf("%T has no ResolveCorroborated", policy)
	}
	decision, err := resolver.ResolveCorroborated(rpcengine.Corroboration{Floor: 2}, obs)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return decision.Decision
}

func payloadOf(item rpc.Observation) json.RawMessage {
	if item.Response.Answer.RPCError != nil {
		return item.Response.Answer.RPCError.Raw()
	}
	return item.Response.Answer.Result

}

// TestMigratedScalarValueIsCanonical pins RV1 for quantities and successful
// DATA. Swapping the captured order does not change Value. Representative
// still follows the first supporter. Unmigrated methods stay on
// TestRepresentativeIsFirstCapturedSource and must keep changing Value.
func TestMigratedScalarValueIsCanonical(t *testing.T) {
	a := rpc.SourceRef{Name: "a", ID: "a"}
	b := rpc.SourceRef{Name: "b", ID: "b"}
	wordA := `"0x` + strings.Repeat("00", 31) + `AB"`
	wordB := `"0x` + strings.Repeat("00", 31) + `ab"`
	wordCanon := `"0x` + strings.Repeat("00", 31) + `ab"`

	cases := []struct {
		name   string
		policy rpcengine.Resolver
		first  string
		second string
		want   string
	}{
		{"chain id", planResolver(t, MethodChainID, rpcengine.WithQuorum(2)), `"0xA"`, `"0xa"`, `"0xa"`},
		{"balance", planResolver(t, MethodGetBalance), `"0xA"`, `"0xa"`, `"0xa"`},
		{"nonce", planResolver(t, MethodGetTransactionCount), `"0xA"`, `"0xa"`, `"0xa"`},
		{"code", planResolver(t, MethodGetCode), `"0xAB"`, `"0xab"`, `"0xab"`},
		{"storage", planResolver(t, MethodGetStorageAt), wordA, wordB, wordCanon},
		{"net version", planResolver(t, MethodNetVersion, rpcengine.WithQuorum(2)), `"01"`, `"1"`, `"1"`},
		{"call success", planResolver(t, MethodCall, rpcengine.WithQuorum(2)), `"0xAB"`, `"0xab"`, `"0xab"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			first := observation(a, tc.first, `{"from":"a"}`)
			second := observation(b, tc.second, `{"from":"b"}`)
			decision := resolvePair(t, tc.policy, first, second)
			swapped := resolvePair(t, tc.policy, second, first)
			if string(decision.Value) != tc.want || string(swapped.Value) != tc.want {
				t.Fatalf("value = %s / %s, want %s", decision.Value, swapped.Value, tc.want)
			}
			if !bytes.Equal(decision.Value, swapped.Value) {
				t.Fatalf("reordering changed canonical value %s -> %s", decision.Value, swapped.Value)
			}
			if bytes.Contains(decision.Value, []byte("ok|")) {
				t.Fatalf("comparison prefix leaked into value %s", decision.Value)
			}
			if decision.Representative != a || swapped.Representative != b {
				t.Fatalf("representative = %+v / %+v, want a then b", decision.Representative, swapped.Representative)
			}
			if !bytes.Equal(decision.Provenance, first.Evidence) || !bytes.Equal(swapped.Provenance, second.Evidence) {
				t.Fatalf("evidence did not follow the first supporter: %s / %s", decision.Provenance, swapped.Provenance)
			}
		})
	}
}

func TestTypedQuantityReadsCanonicalValue(t *testing.T) {
	policy := planResolver(t, MethodChainID, rpcengine.WithQuorum(2))
	a := rpc.SourceRef{Name: "a", ID: "a"}
	b := rpc.SourceRef{Name: "b", ID: "b"}
	decision := resolvePair(t, policy,
		observation(a, `"0xA"`, ""),
		observation(b, `"0xa"`, ""),
	)
	if string(decision.Value) != `"0xa"` {
		t.Fatalf("value = %s, want canonical 0xa", decision.Value)
	}
	got := typedQuantity(t, compiledPlan(t, MethodChainID).Normalize, decision.Value)
	want := typedQuantity(t, compiledPlan(t, MethodChainID).Normalize, json.RawMessage(`"0xa"`))
	if got.Cmp(want) != 0 {
		t.Fatalf("typed value = %s, typed canonical = %s", got, want)
	}
}

func TestTypedDataReadsCanonicalValue(t *testing.T) {
	policy := planResolver(t, MethodGetCode)
	a := rpc.SourceRef{Name: "a", ID: "a"}
	b := rpc.SourceRef{Name: "b", ID: "b"}
	decision := resolvePair(t, policy,
		observation(a, `"0xAB"`, ""),
		observation(b, `"0xab"`, ""),
	)
	if string(decision.Value) != `"0xab"` {
		t.Fatalf("value = %s, want 0xab", decision.Value)
	}
	got := typedData(t, decision.Value)
	want := typedData(t, json.RawMessage(`"0xab"`))
	if !bytes.Equal(got, want) {
		t.Fatalf("typed value = %x, typed canonical = %x", got, want)
	}
}

func TestTypedCallSuccessReadsCanonicalData(t *testing.T) {
	policy := planResolver(t, MethodCall, rpcengine.WithQuorum(2))
	a := rpc.SourceRef{Name: "a", ID: "a"}
	b := rpc.SourceRef{Name: "b", ID: "b"}
	decision := resolvePair(t, policy,
		observation(a, `"0xAB"`, ""),
		observation(b, `"0xab"`, ""),
	)
	if string(decision.Value) != `"0xab"` || bytes.Contains(decision.Value, []byte("ok|")) {
		t.Fatalf("value = %s", decision.Value)
	}
	got := typedData(t, decision.Value)
	want := typedData(t, json.RawMessage(`"0xab"`))
	if !bytes.Equal(got, want) {
		t.Fatalf("typed call = %x, want %x", got, want)
	}
}

func TestScalarMutationCannotManufactureEquality(t *testing.T) {
	if _, err := ethcodec.EncodeQuantity(json.RawMessage(`"0x01"`)); err == nil {
		t.Fatal("leading-zero quantity encoded")
	}
	if _, err := ethcodec.EncodeData(json.RawMessage(`"0xa"`)); err == nil {
		t.Fatal("odd-length data encoded")
	}
	short, err := ethcodec.EncodeData(json.RawMessage(`"0xab"`))
	if err != nil {
		t.Fatal(err)
	}
	padded, err := ethcodec.EncodeData(json.RawMessage(`"0x00ab"`))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(short, padded) {
		t.Fatal("dropping a protected data byte manufactured equality")
	}

	a := rpc.SourceRef{Name: "a", ID: "a"}
	b := rpc.SourceRef{Name: "b", ID: "b"}
	policy := planResolver(t, MethodGetCode)
	resolver := policy.(rpcengine.CorroboratingResolver)
	decision, err := resolver.ResolveCorroborated(rpcengine.Corroboration{Floor: 2}, []rpc.Observation{
		observation(a, `"0x00ab"`, ""),
		observation(b, `"0xab"`, ""),
	})
	if !errors.Is(err, rpc.ErrConflict) || decision.Decision.Status == rpc.Corroborated {
		t.Fatalf("different data agreed: %v %+v", err, decision)
	}

	broken := rpc.SourceRef{Name: "c", ID: "c"}
	decision, err = planResolver(t, MethodChainID, rpcengine.WithQuorum(1)).(rpcengine.CorroboratingResolver).ResolveCorroborated(rpcengine.Corroboration{Floor: 1},
		[]rpc.Observation{
			observation(a, `"0x1"`, ""),
			observation(broken, `"0x01"`, ""),
		},
	)
	if err == nil || !errors.Is(err, rpc.ErrInsufficientSupport) || !strings.Contains(err.Error(), "of 2") {
		t.Fatalf("leading zero left the denominator: %v %+v", err, decision)
	}
	if decision.Decision.Status == rpc.Selected || decision.Decision.Status == rpc.Corroborated {
		t.Fatalf("status = %v, want failure", decision.Decision.Status)
	}
	for _, source := range decision.Decision.Support {
		if source.ID == broken.ID {
			t.Fatal("rejected quantity voted")
		}
	}
	if len(decision.Decision.Faults) == 0 {
		t.Fatal("encode failure was not a fault")
	}
}

func TestCanonicalDisagreementDoesNotRevote(t *testing.T) {
	_, err := rpcengine.CommitCanonical([]json.RawMessage{
		json.RawMessage(`"0xA"`),
		json.RawMessage(`"0xa"`),
	})
	if !errors.Is(err, rpc.ErrInvalidCanonicalResult) || !strings.Contains(err.Error(), "result encoding disagrees within cluster") {
		t.Fatalf("commit err = %v", err)
	}

	// The cluster key is the encoder output. Distinct bytes are distinct
	// clusters, not one cluster repaired by dropping a source and voting again.
	normalize := func(raw json.RawMessage) (string, error) {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return "", err
		}
		return strings.ToLower(text), nil
	}
	encode := func(raw json.RawMessage) (json.RawMessage, error) {
		return append(json.RawMessage(nil), raw...), nil
	}
	policy := rpcengine.Strict(normalize, rpcengine.WithQuorum(2), rpcengine.WithCanonicalValue(encode))
	a := rpc.SourceRef{Name: "a", ID: "a"}
	b := rpc.SourceRef{Name: "b", ID: "b"}
	decision, err := policy.(rpcengine.CorroboratingResolver).ResolveCorroborated(rpcengine.Corroboration{Floor: 2}, []rpc.Observation{
		observation(a, `"0xA"`, ""),
		observation(b, `"0xa"`, ""),
	})
	if !errors.Is(err, rpc.ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	if decision.Decision.Status == rpc.Corroborated || decision.Decision.Status == rpc.Selected || len(decision.Decision.Support) != 1 {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestMigratedQuantityTieUsesCanonicalKey(t *testing.T) {
	policy := planResolver(t, MethodChainID, rpcengine.WithQuorum(1))
	a := rpc.SourceRef{Name: "a", ID: "a"}
	b := rpc.SourceRef{Name: "b", ID: "b"}
	c := rpc.SourceRef{Name: "c", ID: "c"}
	d := rpc.SourceRef{Name: "d", ID: "d"}
	decision, err := policy.(rpcengine.CorroboratingResolver).ResolveCorroborated(rpcengine.Corroboration{Floor: 1}, []rpc.Observation{
		observation(a, `"0x2"`, `{"from":"a"}`),
		observation(b, `"0xA"`, `{"from":"b"}`),
		observation(c, `"0x2"`, `{"from":"c"}`),
		observation(d, `"0xa"`, `{"from":"d"}`),
	})
	if !errors.Is(err, rpc.ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	if string(decision.Decision.Value) != `"0x2"` || decision.Decision.Representative != a {
		t.Fatalf("value = %s representative = %+v, want canonical 0x2 from a", decision.Decision.Value, decision.Decision.Representative)
	}
	if !bytes.Equal(decision.Decision.Provenance, json.RawMessage(`{"from":"a"}`)) {
		t.Fatalf("evidence = %s", decision.Decision.Provenance)
	}
}

func TestMedianSkipsUnencodableQuantity(t *testing.T) {
	policy := compiledPlan(t, MethodBlockNumber).Resolver
	good := rpc.SourceRef{Name: "a", ID: "a"}
	bad := rpc.SourceRef{Name: "b", ID: "b"}
	high := rpc.SourceRef{Name: "c", ID: "c"}
	decision, err := policy.Resolve([]rpc.Observation{
		{Attempted: true, Source: good, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`"0x1"`)}}},
		{Attempted: true, Source: bad, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`"0x02"`)}}},
		{Attempted: true, Source: high, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`"0x3"`)}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Decision.Status != rpc.Selected || string(decision.Decision.Value) != `"0x1"` {
		t.Fatalf("median = %s status %v, want selected 0x1", decision.Decision.Value, decision.Decision.Status)
	}
	if decision.Decision.Representative != good {
		t.Fatalf("representative = %+v, want the first encodable median", decision.Decision.Representative)
	}
	for _, source := range decision.Decision.Support {
		if source.ID == bad.ID {
			t.Fatal("unencodable sample entered the median")
		}
	}
	if len(decision.Decision.Faults) == 0 {
		t.Fatal("expected an encode fault")
	}
}

func TestLocalPeerCountEncodesOnlyTheSelectedSource(t *testing.T) {
	policy := compiledPlan(t, MethodPeerCount).Resolver
	first := rpc.SourceRef{Name: "a", ID: "a"}
	second := rpc.SourceRef{Name: "b", ID: "b"}
	decision, err := policy.Resolve([]rpc.Observation{
		{Attempted: true, Source: first, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`"0x0A"`)}}},
		{Attempted: true, Source: second, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`"0x1"`)}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Decision.Status != rpc.Selected || string(decision.Decision.Value) != `"0xa"` {
		t.Fatalf("local = %s status %v", decision.Decision.Value, decision.Decision.Status)
	}
	if decision.Decision.Representative != (rpc.SourceRef{}) {
		t.Fatalf("representative = %+v, want zero", decision.Decision.Representative)
	}
	if len(decision.Decision.Support) != 1 || decision.Decision.Support[0] != first {
		t.Fatalf("support = %+v", decision.Decision.Support)
	}
}

func TestResolveLocalKeepsRepresentativeZero(t *testing.T) {
	policy := compiledPlan(t, MethodGetBalance).Resolver
	first := rpc.SourceRef{Name: "a", ID: "a"}
	decision, err := rpcengine.ResolveLocalObservation(compiledPlan(t, MethodGetBalance).Normalize, policy, []rpc.Observation{
		{Attempted: true, Source: first, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`"0xA"`)}}},
		{Attempted: true, Source: rpc.SourceRef{Name: "b", ID: "b"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(`"0x1"`)}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Decision.Status != rpc.Selected || string(decision.Decision.Value) != `"0xa"` {
		t.Fatalf("pending = %s status %v", decision.Decision.Value, decision.Decision.Status)
	}
	if decision.Decision.Representative != (rpc.SourceRef{}) || len(decision.Decision.Support) != 1 || decision.Decision.Support[0] != first {
		t.Fatalf("decision = %+v", decision)
	}
}

func typedQuantity(t *testing.T, normalize func(json.RawMessage) (string, error), raw json.RawMessage) *big.Int {
	t.Helper()
	text, err := normalize(raw)
	if err != nil {
		t.Fatal(err)
	}
	value, ok := new(big.Int).SetString(text, 10)
	if !ok {
		t.Fatalf("quantity %q", text)
	}
	return value
}

func typedData(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	var data hexutil.Bytes
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	return data
}
