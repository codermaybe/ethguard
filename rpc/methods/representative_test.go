package methods

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common/hexutil"

	"github.com/codermaybe/ethguard/rpc"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

var errExecutionReverted = errors.New("execution reverted")

// TestRepresentativeIsFirstCapturedSource pins the source selection rule:
// when two sources share a comparison key but spell the payload differently,
// Value, Provenance, and the withheld data all come from the first source in
// the captured order. Swapping the order must change the Value bytes. The
// test must not assert that reordering leaves the bytes unchanged.
func TestRepresentativeIsFirstCapturedSource(t *testing.T) {
	a := rpc.SourceRef{Name: "a", ID: "a"}
	b := rpc.SourceRef{Name: "b", ID: "b"}

	t.Run("strict", func(t *testing.T) {
		policy := rpc.Strict(identityKey, rpc.WithQuorum(2))
		first := observation(a, `"0xA"`, `{"from":"a"}`)
		second := observation(b, `"0xa"`, `{"from":"b"}`)
		assertFirstSource(t, policy, first, second)
	})

	t.Run("call revert data returned", func(t *testing.T) {
		policy := CallPolicy(nil, rpc.WithQuorum(2))
		first := revertObservation(a, `"0xDEAD"`, `{"from":"a"}`)
		second := revertObservation(b, `"0xdead"`, `{"from":"b"}`)
		decision := resolvePair(t, policy, first, second)
		if string(decision.Value) != `{"data":"0xDEAD"}` || decision.Revert == nil || decision.Revert.Code != 3 {
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
		policy := CallPolicy(nil, rpc.WithQuorum(2))
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
		policy := rpc.Strict(identityKey, rpc.WithQuorum(2))
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
		policy := CallPolicy(nil, rpc.WithQuorum(2))
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
	item := rpc.Observation{Source: source, Value: json.RawMessage(value)}
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
	item := rpc.Observation{Source: source, Err: errExecutionReverted, RPCError: body}
	if evidence != "" {
		item.Evidence = json.RawMessage(evidence)
	}
	return item
}

func assertFirstSource(t *testing.T, policy rpc.Policy, first, second rpc.Observation) {
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

func resolvePair(t *testing.T, policy rpc.Policy, obs ...rpc.Observation) rpc.Decision {
	t.Helper()
	resolver, ok := policy.(rpc.CorroboratingPolicy)
	if !ok {
		t.Fatalf("%T has no ResolveCorroborated", policy)
	}
	decision, err := resolver.ResolveCorroborated(rpc.Corroboration{Floor: 2}, obs)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return decision
}

func payloadOf(item rpc.Observation) json.RawMessage {
	if item.RPCError != nil {
		return item.RPCError.Raw()
	}
	return item.Value
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
		policy rpc.Policy
		first  string
		second string
		want   string
	}{
		{"chain id", ChainIDPolicy(rpc.WithQuorum(2)), `"0xA"`, `"0xa"`, `"0xa"`},
		{"balance", GetBalancePolicy(nil), `"0xA"`, `"0xa"`, `"0xa"`},
		{"nonce", GetTransactionCountPolicy(nil), `"0xA"`, `"0xa"`, `"0xa"`},
		{"code", GetCodePolicy(nil), `"0xAB"`, `"0xab"`, `"0xab"`},
		{"storage", GetStorageAtPolicy(nil), wordA, wordB, wordCanon},
		{"net version", NetVersionPolicy(rpc.WithQuorum(2)), `"01"`, `"1"`, `"1"`},
		{"call success", CallPolicy(nil, rpc.WithQuorum(2)), `"0xAB"`, `"0xab"`, `"0xab"`},
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
	policy := ChainIDPolicy(rpc.WithQuorum(2))
	a := rpc.SourceRef{Name: "a", ID: "a"}
	b := rpc.SourceRef{Name: "b", ID: "b"}
	decision := resolvePair(t, policy,
		observation(a, `"0xA"`, ""),
		observation(b, `"0xa"`, ""),
	)
	if string(decision.Value) != `"0xa"` {
		t.Fatalf("value = %s, want canonical 0xa", decision.Value)
	}
	got := typedQuantity(t, policy, decision.Value)
	want := typedQuantity(t, policy, json.RawMessage(`"0xa"`))
	if got.Cmp(want) != 0 {
		t.Fatalf("typed value = %s, typed canonical = %s", got, want)
	}
}

func TestTypedDataReadsCanonicalValue(t *testing.T) {
	policy := GetCodePolicy(nil)
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
	policy := CallPolicy(nil, rpc.WithQuorum(2))
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
	if _, err := wire.EncodeQuantity(json.RawMessage(`"0x01"`)); err == nil {
		t.Fatal("leading-zero quantity encoded")
	}
	if _, err := wire.EncodeData(json.RawMessage(`"0xa"`)); err == nil {
		t.Fatal("odd-length data encoded")
	}
	short, err := wire.EncodeData(json.RawMessage(`"0xab"`))
	if err != nil {
		t.Fatal(err)
	}
	padded, err := wire.EncodeData(json.RawMessage(`"0x00ab"`))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(short, padded) {
		t.Fatal("dropping a protected data byte manufactured equality")
	}

	a := rpc.SourceRef{Name: "a", ID: "a"}
	b := rpc.SourceRef{Name: "b", ID: "b"}
	policy := GetCodePolicy(nil)
	resolver := policy.(rpc.CorroboratingPolicy)
	decision, err := resolver.ResolveCorroborated(rpc.Corroboration{Floor: 2}, []rpc.Observation{
		observation(a, `"0x00ab"`, ""),
		observation(b, `"0xab"`, ""),
	})
	if !errors.Is(err, rpc.ErrConflict) || decision.Status == rpc.Corroborated {
		t.Fatalf("different data agreed: %v %+v", err, decision)
	}

	broken := rpc.SourceRef{Name: "c", ID: "c"}
	decision, err = ChainIDPolicy(rpc.WithQuorum(1)).(rpc.CorroboratingPolicy).ResolveCorroborated(
		rpc.Corroboration{Floor: 1},
		[]rpc.Observation{
			observation(a, `"0x1"`, ""),
			observation(broken, `"0x01"`, ""),
		},
	)
	if err == nil || !errors.Is(err, rpc.ErrConflict) || !strings.Contains(err.Error(), "of 2") {
		t.Fatalf("leading zero left the denominator: %v %+v", err, decision)
	}
	if decision.Status == rpc.Selected || decision.Status == rpc.Corroborated {
		t.Fatalf("status = %v, want failure", decision.Status)
	}
	for _, source := range decision.Support {
		if source.ID == broken.ID {
			t.Fatal("rejected quantity voted")
		}
	}
	if len(decision.Faults) == 0 {
		t.Fatal("encode failure was not a fault")
	}
}

func TestCanonicalDisagreementDoesNotRevote(t *testing.T) {
	_, err := rpc.CommitCanonical([]json.RawMessage{
		json.RawMessage(`"0xA"`),
		json.RawMessage(`"0xa"`),
	})
	if !errors.Is(err, rpc.ErrMalformedResponse) || !strings.Contains(err.Error(), "result encoding disagrees within cluster") {
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
	policy := rpc.Strict(normalize, rpc.WithQuorum(2), rpc.WithCanonicalValue(encode))
	a := rpc.SourceRef{Name: "a", ID: "a"}
	b := rpc.SourceRef{Name: "b", ID: "b"}
	decision, err := policy.(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 2}, []rpc.Observation{
		observation(a, `"0xA"`, ""),
		observation(b, `"0xa"`, ""),
	})
	if !errors.Is(err, rpc.ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	if decision.Status == rpc.Corroborated || decision.Status == rpc.Selected || len(decision.Support) != 1 {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestMigratedQuantityTieUsesCanonicalKey(t *testing.T) {
	policy := ChainIDPolicy(rpc.WithQuorum(1))
	a := rpc.SourceRef{Name: "a", ID: "a"}
	b := rpc.SourceRef{Name: "b", ID: "b"}
	c := rpc.SourceRef{Name: "c", ID: "c"}
	d := rpc.SourceRef{Name: "d", ID: "d"}
	decision, err := policy.(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 1}, []rpc.Observation{
		observation(a, `"0x2"`, `{"from":"a"}`),
		observation(b, `"0xA"`, `{"from":"b"}`),
		observation(c, `"0x2"`, `{"from":"c"}`),
		observation(d, `"0xa"`, `{"from":"d"}`),
	})
	if !errors.Is(err, rpc.ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	if string(decision.Value) != `"0x2"` || decision.Representative != a {
		t.Fatalf("value = %s representative = %+v, want canonical 0x2 from a", decision.Value, decision.Representative)
	}
	if !bytes.Equal(decision.Provenance, json.RawMessage(`{"from":"a"}`)) {
		t.Fatalf("evidence = %s", decision.Provenance)
	}
}

func TestMedianSkipsUnencodableQuantity(t *testing.T) {
	policy := BlockNumberPolicy()
	good := rpc.SourceRef{Name: "a", ID: "a"}
	bad := rpc.SourceRef{Name: "b", ID: "b"}
	high := rpc.SourceRef{Name: "c", ID: "c"}
	decision, err := policy.Resolve([]rpc.Observation{
		{Source: good, Value: json.RawMessage(`"0x1"`)},
		{Source: bad, Value: json.RawMessage(`"0x02"`)},
		{Source: high, Value: json.RawMessage(`"0x3"`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != rpc.Selected || string(decision.Value) != `"0x1"` {
		t.Fatalf("median = %s status %v, want selected 0x1", decision.Value, decision.Status)
	}
	if decision.Representative != good {
		t.Fatalf("representative = %+v, want the first encodable median", decision.Representative)
	}
	for _, source := range decision.Support {
		if source.ID == bad.ID {
			t.Fatal("unencodable sample entered the median")
		}
	}
	if len(decision.Faults) == 0 {
		t.Fatal("expected an encode fault")
	}
}

func TestLocalPeerCountEncodesOnlyTheSelectedSource(t *testing.T) {
	policy := PeerCountPolicy()
	first := rpc.SourceRef{Name: "a", ID: "a"}
	second := rpc.SourceRef{Name: "b", ID: "b"}
	decision, err := policy.Resolve([]rpc.Observation{
		{Source: first, Value: json.RawMessage(`"0x0A"`)},
		{Source: second, Value: json.RawMessage(`"0x1"`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != rpc.Selected || string(decision.Value) != `"0xa"` {
		t.Fatalf("local = %s status %v", decision.Value, decision.Status)
	}
	if decision.Representative != (rpc.SourceRef{}) {
		t.Fatalf("representative = %+v, want zero", decision.Representative)
	}
	if len(decision.Support) != 1 || decision.Support[0] != first {
		t.Fatalf("support = %+v", decision.Support)
	}
}

func TestResolveLocalKeepsRepresentativeZero(t *testing.T) {
	policy := GetBalancePolicy(nil).(rpc.LocalObservationPolicy)
	first := rpc.SourceRef{Name: "a", ID: "a"}
	decision, err := policy.ResolveLocal([]rpc.Observation{
		{Source: first, Value: json.RawMessage(`"0xA"`)},
		{Source: rpc.SourceRef{Name: "b", ID: "b"}, Value: json.RawMessage(`"0x1"`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != rpc.Selected || string(decision.Value) != `"0xa"` {
		t.Fatalf("pending = %s status %v", decision.Value, decision.Status)
	}
	if decision.Representative != (rpc.SourceRef{}) || len(decision.Support) != 1 || decision.Support[0] != first {
		t.Fatalf("decision = %+v", decision)
	}
}

func typedQuantity(t *testing.T, policy rpc.Policy, raw json.RawMessage) *big.Int {
	t.Helper()
	text, err := policy.Normalize(raw)
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
