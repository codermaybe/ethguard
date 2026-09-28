package methods

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ethguard/rpc"
	accesslist "ethguard/rpc/internal/objects/accesslist"
)

const (
	accessAddress = "0x0000000000000000000000000000000000000abc"
	accessKey     = "0x0000000000000000000000000000000000000000000000000000000000000001"
	accessKeyTwo  = "0x0000000000000000000000000000000000000000000000000000000000000002"
)

func TestAccessListSpellingsShareOneCanonicalValue(t *testing.T) {
	policy := CreateAccessListPolicy(func(context.Context) (string, error) { return accessKey, nil }, rpc.WithQuorum(1))
	a := rpc.SourceRef{Name: "a", ID: "a"}
	b := rpc.SourceRef{Name: "b", ID: "b"}
	upper := `{"vendor":"kept-out","gasUsed":"0x5208","error":"execution reverted","accessList":[{"storageKeys":["0x0000000000000000000000000000000000000000000000000000000000000001","0x0000000000000000000000000000000000000000000000000000000000000002"],"address":"0x0000000000000000000000000000000000000ABC"}]}`
	reordered := `{"accessList":[{"address":"0x0000000000000000000000000000000000000abc","storageKeys":["0x0000000000000000000000000000000000000000000000000000000000000001","0x0000000000000000000000000000000000000000000000000000000000000002"]}],"error":"execution reverted","gasUsed":"0x5208"}`
	first := observation(a, upper, `{"from":"a"}`)
	second := observation(b, reordered, `{"from":"b"}`)
	want := `{"accessList":[{"address":"` + accessAddress + `","storageKeys":["` + accessKey + `","` + accessKeyTwo + `"]}],"error":"execution reverted","gasUsed":"0x5208"}`

	resolve := func(order []rpc.Observation) rpc.Decision {
		t.Helper()
		decision, err := policy.(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 1}, order)
		if err != nil {
			t.Fatal(err)
		}
		if decision.Status != rpc.Corroborated || string(decision.Value) != want {
			t.Fatalf("decision = %s status %v, want %s", decision.Value, decision.Status, want)
		}
		return decision
	}
	forward := resolve([]rpc.Observation{first, second})
	backward := resolve([]rpc.Observation{second, first})
	if forward.Representative != a || backward.Representative != b {
		t.Fatalf("representatives = %+v %+v", forward.Representative, backward.Representative)
	}
	if !bytes.Equal(forward.Evidence, json.RawMessage(`{"from":"a"}`)) || !bytes.Equal(backward.Evidence, json.RawMessage(`{"from":"b"}`)) {
		t.Fatal("evidence did not follow the first captured supporter")
	}
	if bytes.Contains(forward.Value, []byte("vendor")) || bytes.Contains(backward.Value, []byte("vendor")) {
		t.Fatal("a field outside the mask entered Value")
	}
}

func TestAccessListMissingStorageKeysStaysInTheDenominator(t *testing.T) {
	policy := CreateAccessListPolicy(func(context.Context) (string, error) { return accessKey, nil }, rpc.WithQuorum(2))
	good := accessListBody(accessAddress, []string{accessKey}, "0x5208", "")
	other := accessListBody(accessAddress, []string{accessKeyTwo}, "0x5208", "")
	incomplete := `{"accessList":[{"address":"` + accessAddress + `"}],"gasUsed":"0x5208"}`
	a := observation(rpc.SourceRef{Name: "a", ID: "a"}, good, "")
	b := observation(rpc.SourceRef{Name: "b", ID: "b"}, good, "")
	d := observation(rpc.SourceRef{Name: "d", ID: "d"}, other, "")
	c := observation(rpc.SourceRef{Name: "c", ID: "c"}, incomplete, "")
	decision, err := policy.(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 2}, []rpc.Observation{a, b, d, c})
	if !errors.Is(err, rpc.ErrConflict) {
		t.Fatalf("err = %v, want the floor to hold against the unreduced denominator", err)
	}
	if decision.Status != rpc.Unknown || len(decision.Support) != 2 {
		t.Fatalf("status %v support %d, want unknown with the two encodable sources", decision.Status, len(decision.Support))
	}
	if len(decision.Faults) != 1 || !strings.Contains(decision.Faults[0].Error(), "encode:") {
		t.Fatalf("faults = %v", decision.Faults)
	}
}

func TestAccessListMissingAddressIsAnEncodeFault(t *testing.T) {
	policy := CreateAccessListPolicy(func(context.Context) (string, error) { return accessKey, nil }, rpc.WithQuorum(1))
	incomplete := `{"accessList":[{"storageKeys":["` + accessKey + `"]}],"gasUsed":"0x5208"}`
	decision, err := policy.(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 1}, []rpc.Observation{
		observation(rpc.SourceRef{Name: "a", ID: "a"}, incomplete, ""),
	})
	if !errors.Is(err, rpc.ErrNoValidResponse) || decision.Status != rpc.Unknown {
		t.Fatalf("decision = %+v err %v", decision, err)
	}
	if len(decision.Faults) != 1 || !strings.Contains(decision.Faults[0].Error(), "missing address") {
		t.Fatalf("faults = %v", decision.Faults)
	}
}

func TestAccessListEncodeOmitsAbsentFieldsAndKeepsEmptyArrays(t *testing.T) {
	policy := CreateAccessListPolicy(func(context.Context) (string, error) { return accessKey, nil }, rpc.WithQuorum(1))
	empty := `{"accessList":[{"address":"` + accessAddress + `","storageKeys":[]}],"gasUsed":"0x0"}`
	split, err := policy.(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 1}, []rpc.Observation{
		observation(rpc.SourceRef{Name: "a", ID: "a"}, empty, ""),
		observation(rpc.SourceRef{Name: "b", ID: "b"}, `{"accessList":[]}`, ""),
	})
	if !errors.Is(err, rpc.ErrConflict) || split.Status != rpc.Unknown || len(split.Support) != 1 {
		t.Fatalf("split = %+v err %v, want a conflict that keeps both shapes", split, err)
	}
	kept, err := policy.(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 1}, []rpc.Observation{
		observation(rpc.SourceRef{Name: "a", ID: "a"}, empty, ""),
		observation(rpc.SourceRef{Name: "b", ID: "b"}, empty, ""),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"accessList":[{"address":"` + accessAddress + `","storageKeys":[]}],"gasUsed":"0x0"}`
	if string(kept.Value) != want || bytes.Contains(kept.Value, []byte(`"error"`)) {
		t.Fatalf("value = %s", kept.Value)
	}
	none, err := policy.(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 1}, []rpc.Observation{
		observation(rpc.SourceRef{Name: "a", ID: "a"}, `{"accessList":[]}`, ""),
		observation(rpc.SourceRef{Name: "b", ID: "b"}, `{"accessList":[]}`, ""),
	})
	if err != nil || string(none.Value) != `{"accessList":[]}` {
		t.Fatalf("empty = %s err %v", none.Value, err)
	}
}

func TestAccessListCanonicalBytesDisagreeWithinCluster(t *testing.T) {
	injected := rpc.Strict(normalizeAccessList, rpc.WithQuorum(1), rpc.WithCanonicalObject(func(raw json.RawMessage) (json.RawMessage, error) {
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
	decision, err := injected.(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 1}, []rpc.Observation{
		observation(rpc.SourceRef{Name: "a", ID: "a"}, `{"mark":1,`+body[1:], ""),
		observation(rpc.SourceRef{Name: "b", ID: "b"}, body, ""),
	})
	if !errors.Is(err, rpc.ErrMalformedResponse) || !strings.Contains(err.Error(), "result encoding disagrees within cluster") {
		t.Fatalf("err = %v", err)
	}
	if decision.Status != rpc.Unknown || len(decision.Value) != 0 {
		t.Fatalf("decision = %s status %v", decision.Value, decision.Status)
	}
	if decision.Representative != (rpc.SourceRef{Name: "a", ID: "a"}) {
		t.Fatalf("representative = %+v, want the first captured supporter", decision.Representative)
	}
}

func TestAccessListPendingEncodesTheSelectedSource(t *testing.T) {
	policy := CreateAccessListPolicy(func(context.Context) (string, error) { return accessKey, nil })
	local := policy.(rpc.LocalObservationPolicy)
	selected := accessListBody(strings.ToUpper(accessAddress), []string{strings.ToUpper(accessKey)}, "0x5208", "")
	decision, err := local.ResolveLocal([]rpc.Observation{
		{Source: rpc.SourceRef{Name: "a", ID: "a"}, Value: json.RawMessage(selected)},
		{Source: rpc.SourceRef{Name: "b", ID: "b"}, Value: json.RawMessage(accessListBody(accessAddress, []string{accessKeyTwo}, "0x1", ""))},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := accessListBody(accessAddress, []string{accessKey}, "0x5208", "")
	if decision.Status != rpc.Selected || string(decision.Value) != want {
		t.Fatalf("pending = %s status %v", decision.Value, decision.Status)
	}
	if decision.Representative != (rpc.SourceRef{}) || len(decision.Support) != 1 {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestAccessListRejectsLeadingZeroQuantity(t *testing.T) {
	policy := CreateAccessListPolicy(func(context.Context) (string, error) { return accessKey, nil }, rpc.WithQuorum(1))
	bad := accessListBody(accessAddress, []string{accessKey}, "0x05208", "")
	decision, err := policy.(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 1}, []rpc.Observation{
		observation(rpc.SourceRef{Name: "a", ID: "a"}, bad, ""),
	})
	if err == nil || decision.Status != rpc.Unknown {
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
