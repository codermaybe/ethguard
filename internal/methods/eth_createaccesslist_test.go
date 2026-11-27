package methods

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/internal/rpcengine"

	accesslist "github.com/codermaybe/ethguard/internal/ethobject/accesslist"
	"github.com/codermaybe/ethguard/rpc"
)

const (
	accessAddress = "0x0000000000000000000000000000000000000abc"
	accessKey     = "0x0000000000000000000000000000000000000000000000000000000000000001"
	accessKeyTwo  = "0x0000000000000000000000000000000000000000000000000000000000000002"
)

func TestAccessListSpellingsShareOneCanonicalValue(t *testing.T) {
	policy := planResolver(t, MethodCreateAccessList, rpcengine.WithQuorum(1))
	a := rpc.SourceRef{Name: "a", ID: "a"}
	b := rpc.SourceRef{Name: "b", ID: "b"}
	upper := `{"vendor":"kept-out","gasUsed":"0x5208","error":"execution reverted","accessList":[{"storageKeys":["0x0000000000000000000000000000000000000000000000000000000000000001","0x0000000000000000000000000000000000000000000000000000000000000002"],"address":"0x0000000000000000000000000000000000000ABC"}]}`
	reordered := `{"accessList":[{"address":"0x0000000000000000000000000000000000000abc","storageKeys":["0x0000000000000000000000000000000000000000000000000000000000000001","0x0000000000000000000000000000000000000000000000000000000000000002"]}],"error":"execution reverted","gasUsed":"0x5208"}`
	first := observation(a, upper, `{"from":"a"}`)
	second := observation(b, reordered, `{"from":"b"}`)
	want := `{"accessList":[{"address":"` + accessAddress + `","storageKeys":["` + accessKey + `","` + accessKeyTwo + `"]}],"error":"execution reverted","gasUsed":"0x5208"}`

	resolve := func(order []rpc.Observation) rpc.Decision {
		t.Helper()
		decision, err := policy.(rpcengine.CorroboratingResolver).ResolveCorroborated(rpcengine.Corroboration{Floor: 1}, order)
		if err != nil {
			t.Fatal(err)
		}
		if decision.Decision.Status != rpc.Corroborated || string(decision.Decision.Value) != want {
			t.Fatalf("decision = %s status %v, want %s", decision.Decision.Value, decision.Decision.Status, want)
		}
		return decision.Decision
	}
	forward := resolve([]rpc.Observation{first, second})
	backward := resolve([]rpc.Observation{second, first})
	if forward.Representative != a || backward.Representative != b {
		t.Fatalf("representatives = %+v %+v", forward.Representative, backward.Representative)
	}
	if !bytes.Equal(forward.Provenance, json.RawMessage(`{"from":"a"}`)) || !bytes.Equal(backward.Provenance, json.RawMessage(`{"from":"b"}`)) {
		t.Fatal("evidence did not follow the first captured supporter")
	}
	if bytes.Contains(forward.Value, []byte("vendor")) || bytes.Contains(backward.Value, []byte("vendor")) {
		t.Fatal("a field outside the mask entered Value")
	}
}

func TestAccessListMissingStorageKeysStaysInTheDenominator(t *testing.T) {
	policy := planResolver(t, MethodCreateAccessList, rpcengine.WithQuorum(2))
	good := accessListBody(accessAddress, []string{accessKey}, "0x5208", "")
	other := accessListBody(accessAddress, []string{accessKeyTwo}, "0x5208", "")
	incomplete := `{"accessList":[{"address":"` + accessAddress + `"}],"gasUsed":"0x5208"}`
	a := observation(rpc.SourceRef{Name: "a", ID: "a"}, good, "")
	b := observation(rpc.SourceRef{Name: "b", ID: "b"}, good, "")
	d := observation(rpc.SourceRef{Name: "d", ID: "d"}, other, "")
	c := observation(rpc.SourceRef{Name: "c", ID: "c"}, incomplete, "")
	decision, err := policy.(rpcengine.CorroboratingResolver).ResolveCorroborated(rpcengine.Corroboration{Floor: 2}, []rpc.Observation{a, b, d, c})
	if !errors.Is(err, rpc.ErrConflict) {
		t.Fatalf("err = %v, want the floor to hold against the unreduced denominator", err)
	}
	if decision.Decision.Status != rpc.Unknown || len(decision.Decision.Support) != 2 {
		t.Fatalf("status %v support %d, want unknown with the two encodable sources", decision.Decision.Status, len(decision.Decision.Support))
	}
	if len(decision.Decision.Faults) != 1 || !strings.Contains(decision.Decision.Faults[0].Error(), "encode:") {
		t.Fatalf("faults = %v", decision.Decision.Faults)
	}
}

func TestAccessListMissingAddressIsAnEncodeFault(t *testing.T) {
	policy := planResolver(t, MethodCreateAccessList, rpcengine.WithQuorum(1))
	incomplete := `{"accessList":[{"storageKeys":["` + accessKey + `"]}],"gasUsed":"0x5208"}`
	decision, err := policy.(rpcengine.CorroboratingResolver).ResolveCorroborated(rpcengine.Corroboration{Floor: 1}, []rpc.Observation{
		observation(rpc.SourceRef{Name: "a", ID: "a"}, incomplete, ""),
	})
	if !errors.Is(err, rpc.ErrNoValidResponse) || decision.Decision.Status != rpc.Unknown {
		t.Fatalf("decision = %+v err %v", decision, err)
	}
	if len(decision.Decision.Faults) != 1 || !strings.Contains(decision.Decision.Faults[0].Error(), "missing address") {
		t.Fatalf("faults = %v", decision.Decision.Faults)
	}
}

func TestAccessListEncodeOmitsAbsentFieldsAndKeepsEmptyArrays(t *testing.T) {
	policy := planResolver(t, MethodCreateAccessList, rpcengine.WithQuorum(1))
	empty := `{"accessList":[{"address":"` + accessAddress + `","storageKeys":[]}],"gasUsed":"0x0"}`
	split, err := policy.(rpcengine.CorroboratingResolver).ResolveCorroborated(rpcengine.Corroboration{Floor: 1}, []rpc.Observation{
		observation(rpc.SourceRef{Name: "a", ID: "a"}, empty, ""),
		observation(rpc.SourceRef{Name: "b", ID: "b"}, `{"accessList":[]}`, ""),
	})
	if !errors.Is(err, rpc.ErrConflict) || split.Decision.Status != rpc.Unknown || len(split.Decision.Support) != 1 {
		t.Fatalf("split = %+v err %v, want a conflict that keeps both shapes", split, err)
	}
	kept, err := policy.(rpcengine.CorroboratingResolver).ResolveCorroborated(rpcengine.Corroboration{Floor: 1}, []rpc.Observation{
		observation(rpc.SourceRef{Name: "a", ID: "a"}, empty, ""),
		observation(rpc.SourceRef{Name: "b", ID: "b"}, empty, ""),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"accessList":[{"address":"` + accessAddress + `","storageKeys":[]}],"gasUsed":"0x0"}`
	if string(kept.Decision.Value) != want || bytes.Contains(kept.Decision.Value, []byte(`"error"`)) {
		t.Fatalf("value = %s", kept.Decision.Value)
	}
	none, err := policy.(rpcengine.CorroboratingResolver).ResolveCorroborated(rpcengine.Corroboration{Floor: 1}, []rpc.Observation{
		observation(rpc.SourceRef{Name: "a", ID: "a"}, `{"accessList":[]}`, ""),
		observation(rpc.SourceRef{Name: "b", ID: "b"}, `{"accessList":[]}`, ""),
	})
	if err != nil || string(none.Decision.Value) != `{"accessList":[]}` {
		t.Fatalf("empty = %s err %v", none.Decision.Value, err)
	}
}

func TestAccessListCanonicalBytesDisagreeWithinCluster(t *testing.T) {
	injected := rpcengine.Strict(normalizeAccessList, rpcengine.WithQuorum(1), rpcengine.WithCanonicalObject(func(raw json.RawMessage) (json.RawMessage, error) {
		encoded, err := accesslist.Encode(raw)
		if err != nil {
			return nil, err
		}
		if bytes.Contains(raw, []byte(`"mark"`)) {
			return append(json.RawMessage(nil), append(encoded, ' ')...), nil
		}
		return encoded, nil
	}))
	body := accessListBody(accessAddress, []string{accessKey}, "0x5208", "")
	decision, err := injected.(rpcengine.CorroboratingResolver).ResolveCorroborated(rpcengine.Corroboration{Floor: 1}, []rpc.Observation{
		observation(rpc.SourceRef{Name: "a", ID: "a"}, `{"mark":1,`+body[1:], ""),
		observation(rpc.SourceRef{Name: "b", ID: "b"}, body, ""),
	})
	if !errors.Is(err, rpc.ErrInvalidCanonicalResult) || !strings.Contains(err.Error(), "result encoding disagrees within cluster") {
		t.Fatalf("err = %v", err)
	}
	if decision.Decision.Status != rpc.Unknown || len(decision.Decision.Value) != 0 {
		t.Fatalf("decision = %s status %v", decision.Decision.Value, decision.Decision.Status)
	}
	if decision.Decision.Representative != (rpc.SourceRef{Name: "a", ID: "a"}) {
		t.Fatalf("representative = %+v, want the first captured supporter", decision.Decision.Representative)
	}
}

func TestAccessListPendingEncodesTheSelectedSource(t *testing.T) {
	plan := compiledPlan(t, MethodCreateAccessList)
	selected := accessListBody(strings.ToUpper(accessAddress), []string{strings.ToUpper(accessKey)}, "0x5208", "")
	decision, err := rpcengine.ResolveLocalObservation(plan.Resolver, []rpc.Observation{
		{Attempted: true, Source: rpc.SourceRef{Name: "a", ID: "a"}, Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(selected)}}},
		{Attempted: true, Source: rpc.SourceRef{Name: "b", ID: "b"}, Response: rpc.Response{
			Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted},
			Answer:   rpc.Answer{Kind: rpc.AnswerResult, Result: json.RawMessage(accessListBody(accessAddress, []string{accessKeyTwo}, "0x1", ""))},
		},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := accessListBody(accessAddress, []string{accessKey}, "0x5208", "")
	if decision.Decision.Status != rpc.Selected || string(decision.Decision.Value) != want {
		t.Fatalf("pending = %s status %v", decision.Decision.Value, decision.Decision.Status)
	}
	if decision.Decision.Representative != (rpc.SourceRef{}) || len(decision.Decision.Support) != 1 {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestAccessListRejectsLeadingZeroQuantity(t *testing.T) {
	policy := planResolver(t, MethodCreateAccessList, rpcengine.WithQuorum(1))
	bad := accessListBody(accessAddress, []string{accessKey}, "0x05208", "")
	decision, err := policy.(rpcengine.CorroboratingResolver).ResolveCorroborated(rpcengine.Corroboration{Floor: 1}, []rpc.Observation{
		observation(rpc.SourceRef{Name: "a", ID: "a"}, bad, ""),
	})
	if err == nil || decision.Decision.Status != rpc.Unknown {
		t.Fatalf("leading zero accepted: %+v %v", decision, err)
	}
}

func accessListBody(address string, keys []string, gas, message string) string {
	quoted := make([]string, len(keys))
	for i, key := range keys {
		quoted[i] = `"` + key + `"`
	}
	body := `{"accessList":[{"address":"` + address + `","storageKeys":[` + strings.Join(quoted, ",") + `]}]`
	if message != "" {
		body += `,"error":"` + message + `"`
	}
	if gas != "" {
		body += `,"gasUsed":"` + gas + `"`
	}
	return body + `}`
}

func TestAccessListHexErrorTextCannotSilentlyMerge(t *testing.T) {
	policy := planResolver(t, MethodCreateAccessList, rpcengine.WithQuorum(1))
	first := json.RawMessage(`{"accessList":[],"error":"0xAB"}`)
	second := json.RawMessage(`{"accessList":[],"error":"0xab"}`)
	firstKey, err := policy.Normalize(first)
	if err != nil {
		t.Fatal(err)
	}
	secondKey, err := policy.Normalize(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstKey == secondKey {
		t.Fatal("error text casing was folded in the comparison key")
	}
	decision, err := policy.(rpcengine.CorroboratingResolver).ResolveCorroborated(rpcengine.Corroboration{Floor: 1}, []rpc.Observation{
		observation(rpc.SourceRef{ID: "a"}, string(first), ""), observation(rpc.SourceRef{ID: "b"}, string(second), ""),
	})
	if !errors.Is(err, rpc.ErrConflict) || decision.Decision.Status != rpc.Unknown || len(decision.Decision.Support) != 1 || len(decision.Decision.Reject) != 1 {
		t.Fatalf("decision = %+v err %v", decision, err)
	}
}

func TestAccessListHexErrorEscapesKeepTextSemantics(t *testing.T) {
	policy := planResolver(t, MethodCreateAccessList, rpcengine.WithQuorum(1))
	first := accessListBody(strings.ToUpper(accessAddress), []string{strings.ToUpper(accessKey)}, "0x5208", "0xAB")
	second := accessListBody(accessAddress, []string{accessKey}, "0x5208", `0x\u0041B`)
	decision, err := policy.(rpcengine.CorroboratingResolver).ResolveCorroborated(rpcengine.Corroboration{Floor: 1}, []rpc.Observation{
		observation(rpc.SourceRef{ID: "a"}, first, ""), observation(rpc.SourceRef{ID: "b"}, second, ""),
	})
	if err != nil || decision.Decision.Status != rpc.Corroborated || len(decision.Decision.Support) != 2 || !bytes.Contains(decision.Decision.Value, []byte(`"error":"0xAB"`)) {
		t.Fatalf("decision = %+v err %v", decision, err)
	}
}

func TestAccessListNullErrorDoesNotBecomeEmptyText(t *testing.T) {
	policy := planResolver(t, MethodCreateAccessList, rpcengine.WithQuorum(1))
	null := `{"accessList":[],"error":null}`
	empty := `{"accessList":[],"error":""}`
	decision, err := policy.(rpcengine.CorroboratingResolver).ResolveCorroborated(rpcengine.Corroboration{Floor: 1}, []rpc.Observation{
		observation(rpc.SourceRef{ID: "a"}, null, ""), observation(rpc.SourceRef{ID: "b"}, empty, ""),
	})
	if !errors.Is(err, rpc.ErrConflict) || decision.Decision.Status != rpc.Unknown || len(decision.Decision.Support) != 1 || len(decision.Decision.Reject) != 1 {
		t.Fatalf("decision = %+v err %v", decision, err)
	}
	decision, err = policy.(rpcengine.CorroboratingResolver).ResolveCorroborated(rpcengine.Corroboration{Floor: 1}, []rpc.Observation{
		observation(rpc.SourceRef{ID: "a"}, null, ""), observation(rpc.SourceRef{ID: "b"}, null, ""),
	})
	if err != nil || decision.Decision.Status != rpc.Corroborated || string(decision.Decision.Value) != `{"accessList":[]}` {
		t.Fatalf("decision = %+v err %v", decision, err)
	}
}
