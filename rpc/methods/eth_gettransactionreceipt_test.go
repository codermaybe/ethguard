package methods

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"ethguard/internal/rpcsample/replay"
	"ethguard/rpc"
	receiptobj "ethguard/rpc/internal/objects/receipt"
)

func TestReceiptSpellingsShareOneCanonicalValue(t *testing.T) {
	policy := TransactionReceiptPolicy(rpc.WithQuorum(1))
	raw := sampleReceipt(t)
	reordered := reorderReceipt(t, raw)
	upper := foldReceiptHex(t, raw)
	a := rpc.SourceRef{Name: "a", ID: "a"}
	b := rpc.SourceRef{Name: "b", ID: "b"}
	first := observation(a, string(upper), `{"from":"a"}`)
	second := observation(b, string(reordered), `{"from":"b"}`)

	resolve := func(order []rpc.Observation) rpc.Decision {
		t.Helper()
		decision, err := policy.(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 1}, order)
		if err != nil {
			t.Fatal(err)
		}
		if decision.Status != rpc.Corroborated || bytes.Contains(decision.Value, []byte(`"from"`)) {
			t.Fatalf("decision = %s status %v", decision.Value, decision.Status)
		}
		return decision
	}
	forward := resolve([]rpc.Observation{first, second})
	backward := resolve([]rpc.Observation{second, first})
	if !bytes.Equal(forward.Value, backward.Value) {
		t.Fatalf("value changed with order:\n%s\n%s", forward.Value, backward.Value)
	}
	if forward.Representative != a || backward.Representative != b {
		t.Fatalf("representatives = %+v %+v", forward.Representative, backward.Representative)
	}
	if !bytes.Equal(forward.Evidence, json.RawMessage(`{"from":"a"}`)) || !bytes.Equal(backward.Evidence, json.RawMessage(`{"from":"b"}`)) {
		t.Fatal("evidence did not follow the first captured supporter")
	}
}

func TestReceiptFromDisagreementStaysOneCluster(t *testing.T) {
	policy := TransactionReceiptPolicy(rpc.WithQuorum(1))
	raw := sampleReceipt(t)
	other := replaceReceiptField(t, raw, "from", `"0x0000000000000000000000000000000000000002"`)
	decision, err := policy.(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 1}, []rpc.Observation{
		observation(rpc.SourceRef{Name: "a", ID: "a"}, string(raw), ""),
		observation(rpc.SourceRef{Name: "b", ID: "b"}, string(other), ""),
	})
	if err != nil || decision.Status != rpc.Corroborated || bytes.Contains(decision.Value, []byte(`"from"`)) {
		t.Fatalf("from split the receipt or entered Value: %s %v", decision.Value, err)
	}
}

func TestReceiptMissingLogsStaysInTheDenominator(t *testing.T) {
	policy := TransactionReceiptPolicy(rpc.WithQuorum(2))
	good := string(sampleReceipt(t))
	other := string(replaceReceiptField(t, sampleReceipt(t), "gasUsed", `"0x1"`))
	incomplete := string(deleteReceiptField(t, sampleReceipt(t), "logs"))
	decision, err := policy.(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 2}, []rpc.Observation{
		observation(rpc.SourceRef{Name: "a", ID: "a"}, good, ""),
		observation(rpc.SourceRef{Name: "b", ID: "b"}, good, ""),
		observation(rpc.SourceRef{Name: "d", ID: "d"}, other, ""),
		observation(rpc.SourceRef{Name: "c", ID: "c"}, incomplete, ""),
	})
	if !errors.Is(err, rpc.ErrConflict) {
		t.Fatalf("err = %v, want the floor to hold against the unreduced denominator", err)
	}
	if decision.Status != rpc.Unknown || len(decision.Support) != 2 {
		t.Fatalf("status %v support %d", decision.Status, len(decision.Support))
	}
	if len(decision.Faults) != 1 || !strings.Contains(decision.Faults[0].Error(), "encode:") {
		t.Fatalf("faults = %v", decision.Faults)
	}
}

func TestReceiptNullEncodesAsNull(t *testing.T) {
	policy := TransactionReceiptPolicy(rpc.WithQuorum(1))
	decision, err := policy.(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 1}, []rpc.Observation{
		observation(rpc.SourceRef{Name: "a", ID: "a"}, `null`, ""),
		observation(rpc.SourceRef{Name: "b", ID: "b"}, `null`, ""),
	})
	if err != nil || string(decision.Value) != "null" || decision.Status != rpc.Corroborated {
		t.Fatalf("null = %s status %v err %v", decision.Value, decision.Status, err)
	}
}

func TestReceiptCanonicalBytesDisagreeWithinCluster(t *testing.T) {
	injected := rpc.Strict(normalizeReceiptObject, rpc.WithQuorum(1), rpc.WithCanonicalObject(func(raw json.RawMessage) (json.RawMessage, error) {
		encoded, err := receiptobj.Encode(raw)
		if err != nil {
			return nil, err
		}
		if bytes.Contains(raw, []byte(`"mark"`)) {
			return append(json.RawMessage(nil), append(encoded, ' ')...), nil
		}
		return encoded, nil
	}))
	body := sampleReceipt(t)
	marked := injectReceiptField(t, body, "mark", "1")
	decision, err := injected.(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 1}, []rpc.Observation{
		observation(rpc.SourceRef{Name: "a", ID: "a"}, string(marked), ""),
		observation(rpc.SourceRef{Name: "b", ID: "b"}, string(body), ""),
	})
	if !errors.Is(err, rpc.ErrMalformedResponse) || !strings.Contains(err.Error(), "result encoding disagrees within cluster") {
		t.Fatalf("err = %v", err)
	}
	if decision.Status != rpc.Unknown || len(decision.Value) != 0 {
		t.Fatalf("decision = %s status %v", decision.Value, decision.Status)
	}
}

func sampleReceipt(t *testing.T) json.RawMessage {
	t.Helper()
	_, records, err := replay.LoadBatch(filepath.Join("..", "..", "internal", "rpcsample", "testdata", "block-receipt-tx", "2026-09-23"))
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.RequestID != "receipt-one" || record.Source.ID != "s1" {
			continue
		}
		_, body, err := replay.BodyOf(record)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(body, &doc); err != nil {
			t.Fatal(err)
		}
		return append(json.RawMessage(nil), doc.Result...)
	}
	t.Fatal("receipt sample missing")
	return nil
}

func reorderReceipt(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	keys := []string{"type", "to", "status", "logsBloom", "logs", "gasUsed", "from", "effectiveGasPrice", "cumulativeGasUsed", "contractAddress", "transactionIndex", "transactionHash", "blockNumber", "blockHash"}
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		encoded, err := json.Marshal(key)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(encoded)
		buf.WriteByte(':')
		buf.Write(obj[key])
	}
	buf.WriteByte('}')
	return buf.Bytes()
}

func foldReceiptHex(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"transactionHash", "blockHash", "to", "from"} {
		obj[key] = strings.ToUpper(obj[key].(string))
	}
	out, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func replaceReceiptField(t *testing.T, raw json.RawMessage, key, value string) json.RawMessage {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	obj[key] = json.RawMessage(value)
	out, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func deleteReceiptField(t *testing.T, raw json.RawMessage, key string) json.RawMessage {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	delete(obj, key)
	out, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func injectReceiptField(t *testing.T, raw json.RawMessage, key, value string) json.RawMessage {
	t.Helper()
	return replaceReceiptField(t, raw, key, value)
}
