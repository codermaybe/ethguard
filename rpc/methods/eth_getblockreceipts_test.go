package methods

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ethguard/rpc"
	receiptobj "ethguard/rpc/internal/objects/receipt"
)

func receiptArray(t *testing.T, rows ...json.RawMessage) json.RawMessage {
	t.Helper()
	if rows == nil {
		return json.RawMessage(`[]`)
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestBlockReceiptSpellingsAndFromShareCanonicalValue(t *testing.T) {
	raw := sampleReceipt(t)
	variants := []json.RawMessage{
		receiptArray(t, raw),
		receiptArray(t, reorderReceipt(t, raw)),
		receiptArray(t, foldReceiptHex(t, raw)),
		// Equivalent JSON string escape spelling.
		json.RawMessage(strings.Replace(string(receiptArray(t, raw)), `"0x`, `"\u0030x`, 1)),
	}
	policy := BlockReceiptsPolicy(nil, rpc.WithQuorum(1))
	var baseline json.RawMessage
	for i := range variants {
		a := observation(rpc.SourceRef{ID: "a"}, string(variants[0]), `{"source":"a"}`)
		b := observation(rpc.SourceRef{ID: "b"}, string(variants[i]), `{"source":"b"}`)
		for _, order := range [][]rpc.Observation{{a, b}, {b, a}} {
			decision, err := policy.(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 1}, order)
			if err != nil || decision.Status != rpc.Corroborated || len(decision.Support) != 2 {
				t.Fatalf("variant %d decision = %+v err %v", i, decision, err)
			}
			if baseline == nil {
				baseline = decision.Value
			} else if !bytes.Equal(baseline, decision.Value) {
				t.Fatal("capture order/spelling changed Value")
			}
			if decision.Representative != order[0].Source || !bytes.Equal(decision.Provenance, order[0].Evidence) {
				t.Fatalf("representative/evidence = %+v", decision)
			}
		}
	}
}

func TestBlockReceiptsNullEmptyAndArrayOrder(t *testing.T) {
	first := sampleReceipt(t)
	second := replaceReceiptField(t, first, "gasUsed", `"0x1"`)
	policy := BlockReceiptsPolicy(nil, rpc.WithQuorum(1)).(rpc.CorroboratingPolicy)
	for _, body := range []string{`null`, `[]`} {
		d, err := policy.ResolveCorroborated(rpc.Corroboration{Floor: 1}, []rpc.Observation{observation(rpc.SourceRef{ID: "a"}, body, ""), observation(rpc.SourceRef{ID: "b"}, body, "")})
		if err != nil || string(d.Value) != body || d.Status != rpc.Corroborated {
			t.Fatalf("body %s = %+v err %v", body, d, err)
		}
	}
	for _, pair := range [][2]string{
		{`null`, `[]`},
		{string(receiptArray(t, first, second)), string(receiptArray(t, second, first))},
		{string(receiptArray(t, first)), string(receiptArray(t, second))},
	} {
		d, err := policy.ResolveCorroborated(rpc.Corroboration{Floor: 1}, []rpc.Observation{observation(rpc.SourceRef{ID: "a"}, pair[0], ""), observation(rpc.SourceRef{ID: "b"}, pair[1], "")})
		if !errors.Is(err, rpc.ErrConflict) || d.Status != rpc.Unknown || len(d.Support) != 1 || len(d.Reject) != 1 {
			t.Fatalf("pair = %+v err %v", d, err)
		}
	}
	encoded, err := receiptobj.EncodeBlock(receiptArray(t, first, second))
	if err != nil {
		t.Fatal(err)
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(encoded, &rows); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(rows[1], []byte(`"gasUsed":"0x1"`)) || bytes.Contains(rows[0], []byte(`"gasUsed":"0x1"`)) {
		t.Fatalf("array order changed: %s", encoded)
	}
}

func TestBlockReceiptInvalidElementKeepsWholeSourceInDenominator(t *testing.T) {
	good := receiptArray(t, sampleReceipt(t))
	other := receiptArray(t, replaceReceiptField(t, sampleReceipt(t), "gasUsed", `"0x1"`))
	for _, bad := range []json.RawMessage{receiptArray(t, sampleReceipt(t), json.RawMessage(`null`)), receiptArray(t, sampleReceipt(t), deleteReceiptField(t, sampleReceipt(t), "logs"))} {
		d, err := BlockReceiptsPolicy(nil, rpc.WithQuorum(2)).(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 2}, []rpc.Observation{
			observation(rpc.SourceRef{ID: "a"}, string(good), ""), observation(rpc.SourceRef{ID: "b"}, string(good), ""), observation(rpc.SourceRef{ID: "c"}, string(other), ""), observation(rpc.SourceRef{ID: "d"}, string(bad), ""),
		})
		if !errors.Is(err, rpc.ErrConflict) || d.Status != rpc.Unknown || len(d.Support) != 2 || len(d.Reject) != 1 || len(d.Faults) != 1 || !strings.Contains(d.Faults[0].Error(), "encode:") {
			t.Fatalf("decision = %+v err %v", d, err)
		}
	}
}

func TestBlockReceiptCanonicalDisagreementFailsWithoutRevote(t *testing.T) {
	injected := rpc.Strict(normalizeBlockReceiptList, rpc.WithCanonicalObject(func(raw json.RawMessage) (json.RawMessage, error) {
		encoded, err := receiptobj.EncodeBlock(raw)
		if err != nil {
			return nil, err
		}
		if bytes.Contains(raw, []byte(`"mark"`)) {
			return append(encoded, ' '), nil
		}
		return encoded, nil
	}))
	a := receiptArray(t, injectReceiptField(t, sampleReceipt(t), "mark", "1"))
	b := receiptArray(t, sampleReceipt(t))
	d, err := injected.(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 2}, []rpc.Observation{
		observation(rpc.SourceRef{ID: "a"}, string(a), ""), observation(rpc.SourceRef{ID: "b"}, string(b), ""), observation(rpc.SourceRef{ID: "c"}, string(b), ""),
	})
	if !errors.Is(err, rpc.ErrMalformedResponse) || !strings.Contains(err.Error(), "result encoding disagrees within cluster") || d.Status != rpc.Unknown || len(d.Value) != 0 || len(d.Support) != 3 {
		t.Fatalf("decision = %+v err %v", d, err)
	}
}

func TestBlockReceiptEscapesPreserveAdmissionChecks(t *testing.T) {
	raw := sampleReceipt(t)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	var hash string
	if err := json.Unmarshal(fields["blockHash"], &hash); err != nil {
		t.Fatal(err)
	}
	escape := func(value json.RawMessage) json.RawMessage {
		return json.RawMessage(strings.ReplaceAll(string(value), `"0x`, `"\u0030x`))
	}
	req := rpc.Request{Method: MethodGetBlockReceipts, Params: []any{hash}}
	if _, err := admitBlockReceipts(context.Background(), req, escape(receiptArray(t, raw)), nil); err != nil {
		t.Fatal(err)
	}
	foreign := rpc.Request{Method: MethodGetBlockReceipts, Params: []any{"0x" + strings.Repeat("34", 32)}}
	if _, err := admitBlockReceipts(context.Background(), foreign, escape(receiptArray(t, raw)), nil); !errors.Is(err, rpc.ErrMalformedResponse) {
		t.Fatalf("foreign block = %v", err)
	}
	badBloom := replaceReceiptField(t, raw, "logsBloom", `"0x`+strings.Repeat("00", 255)+`01"`)
	if _, err := admitBlockReceipts(context.Background(), req, escape(receiptArray(t, badBloom)), nil); !errors.Is(err, rpc.ErrMalformedResponse) || !strings.Contains(err.Error(), "logsBloom disagrees with logs") {
		t.Fatalf("bloom = %v", err)
	}
}
