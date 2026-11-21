package methods

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
	receiptobj "github.com/codermaybe/ethguard/rpc/internal/objects/receipt"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
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
	policy := planResolver(t, MethodGetBlockReceipts, rpc.WithQuorum(1))
	var baseline json.RawMessage
	for i := range variants {
		a := observation(rpc.SourceRef{ID: "a"}, string(variants[0]), `{"source":"a"}`)
		b := observation(rpc.SourceRef{ID: "b"}, string(variants[i]), `{"source":"b"}`)
		for _, order := range [][]rpc.Observation{{a, b}, {b, a}} {
			decision, err := policy.(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 1}, order)
			if err != nil || decision.Decision.Status != rpc.Corroborated || len(decision.Decision.Support) != 2 {
				t.Fatalf("variant %d decision = %+v err %v", i, decision, err)
			}
			if baseline == nil {
				baseline = decision.Decision.Value
			} else if !bytes.Equal(baseline, decision.Decision.Value) {
				t.Fatal("capture order/spelling changed Value")
			}
			if decision.Decision.Representative != order[0].Source || !bytes.Equal(decision.Decision.Provenance, order[0].Evidence) {
				t.Fatalf("representative/evidence = %+v", decision)
			}
		}
	}
}

func TestBlockReceiptsNullEmptyAndArrayOrder(t *testing.T) {
	first := sampleReceipt(t)
	second := replaceReceiptField(t, first, "gasUsed", `"0x1"`)
	policy := planResolver(t, MethodGetBlockReceipts, rpc.WithQuorum(1)).(rpc.CorroboratingPolicy)
	for _, body := range []string{`null`, `[]`} {
		d, err := policy.ResolveCorroborated(rpc.Corroboration{Floor: 1}, []rpc.Observation{observation(rpc.SourceRef{ID: "a"}, body, ""), observation(rpc.SourceRef{ID: "b"}, body, "")})
		if err != nil || string(d.Decision.Value) != body || d.Decision.Status != rpc.Corroborated {
			t.Fatalf("body %s = %+v err %v", body, d, err)
		}
	}
	for _, pair := range [][2]string{
		{`null`, `[]`},
		{string(receiptArray(t, first, second)), string(receiptArray(t, second, first))},
		{string(receiptArray(t, first)), string(receiptArray(t, second))},
	} {
		d, err := policy.ResolveCorroborated(rpc.Corroboration{Floor: 1}, []rpc.Observation{observation(rpc.SourceRef{ID: "a"}, pair[0], ""), observation(rpc.SourceRef{ID: "b"}, pair[1], "")})
		if !errors.Is(err, rpc.ErrConflict) || d.Decision.Status != rpc.Unknown || len(d.Decision.Support) != 1 || len(d.Decision.Reject) != 1 {
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
		d, err := planResolver(t, MethodGetBlockReceipts, rpc.WithQuorum(2)).(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 2}, []rpc.Observation{
			observation(rpc.SourceRef{ID: "a"}, string(good), ""), observation(rpc.SourceRef{ID: "b"}, string(good), ""), observation(rpc.SourceRef{ID: "c"}, string(other), ""), observation(rpc.SourceRef{ID: "d"}, string(bad), ""),
		})
		if !errors.Is(err, rpc.ErrConflict) || d.Decision.Status != rpc.Unknown || len(d.Decision.Support) != 2 || len(d.Decision.Reject) != 1 || len(d.Decision.Faults) != 1 || !strings.Contains(d.Decision.Faults[0].Error(), "encode:") {
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
	if !errors.Is(err, rpc.ErrInvalidCanonicalResult) || !strings.Contains(err.Error(), "result encoding disagrees within cluster") || d.Decision.Status != rpc.Unknown || len(d.Decision.Value) != 0 || len(d.Decision.Support) != 3 {
		t.Fatalf("decision = %+v err %v", d, err)
	}
}

// TestBlockReceiptComparisonKeyIsASingleCanonicalPass pins the comparison
// key's ownership: the key is exactly the pruner's canonical bytes, with no
// second normalization pass that could drift from the committed value's
// encoder. A provider that reorders object keys must not change the key.
func TestBlockReceiptComparisonKeyIsASingleCanonicalPass(t *testing.T) {
	raw := receiptArray(t, sampleReceipt(t))
	key, err := normalizeBlockReceiptList(raw)
	if err != nil {
		t.Fatal(err)
	}
	direct, err := receiptobj.NormalizeBlock(raw, wire.PruneMethod, MethodGetBlockReceipts)
	if err != nil {
		t.Fatal(err)
	}
	if key != direct {
		t.Fatalf("comparison key drifted from the pruner's canonical bytes:\nkey    = %s\ndirect = %s", key, direct)
	}

	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	reordered := make([]json.RawMessage, 0, len(rows))
	for _, row := range rows {
		names := make([]string, 0, len(row))
		for name := range row {
			names = append(names, name)
		}
		sort.Strings(names)
		var buf bytes.Buffer
		buf.WriteByte('{')
		for i, name := range names {
			if i > 0 {
				buf.WriteByte(',')
			}
			nameJSON, err := json.Marshal(name)
			if err != nil {
				t.Fatal(err)
			}
			buf.Write(nameJSON)
			buf.WriteByte(':')
			buf.Write(row[name])
		}
		buf.WriteByte('}')
		reordered = append(reordered, json.RawMessage(buf.Bytes()))
	}
	reorderedRaw, err := json.Marshal(reordered)
	if err != nil {
		t.Fatal(err)
	}
	again, err := normalizeBlockReceiptList(reorderedRaw)
	if err != nil {
		t.Fatal(err)
	}
	if again != key {
		t.Fatalf("key order changed the comparison key:\nordered  = %s\nshuffled = %s", key, again)
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
