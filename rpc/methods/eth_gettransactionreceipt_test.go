package methods

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

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
		if decision.Status != rpc.Corroborated || !bytes.Contains(decision.Value, []byte(`"from"`)) {
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

func TestReceiptFromDisagreementSplitsTheCluster(t *testing.T) {
	policy := TransactionReceiptPolicy(rpc.WithQuorum(1))
	raw := sampleReceipt(t)
	other := replaceReceiptField(t, raw, "from", `"0x0000000000000000000000000000000000000002"`)
	decision, err := policy.(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 1}, []rpc.Observation{
		observation(rpc.SourceRef{Name: "a", ID: "a"}, string(raw), ""),
		observation(rpc.SourceRef{Name: "b", ID: "b"}, string(other), ""),
	})
	if !errors.Is(err, rpc.ErrConflict) || decision.Status != rpc.Unknown {
		t.Fatalf("from disagreement was accepted: decision=%+v err=%v", decision, err)
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

// Real captured receipt bytes are reused in memory; these benchmarks do not
// introduce provider samples or infer resource limits.
func BenchmarkReceiptCanonicalValue(b *testing.B) {
	raw := benchmarkReceipt(b)
	for _, size := range []int{0, 100, 1000} {
		b.Run(fmt.Sprintf("logs-%d", size), func(b *testing.B) {
			var r types.Receipt
			if err := json.Unmarshal(raw, &r); err != nil {
				b.Fatal(err)
			}
			r.Logs = make([]*types.Log, size)
			for i := range r.Logs {
				r.Logs[i] = &types.Log{Address: common.HexToAddress("0x1234"), Topics: []common.Hash{common.HexToHash("0xab")}, Data: make([]byte, 32), BlockNumber: r.BlockNumber.Uint64(), TxHash: r.TxHash, BlockHash: r.BlockHash, Index: uint(i)}
			}
			r.Bloom = types.CreateBloom(&r)
			body, err := json.Marshal(&r)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := receiptobj.Encode(body); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func benchmarkReceipt(b *testing.B) json.RawMessage {
	b.Helper()
	_, records, err := replay.LoadBatch(filepath.Join("..", "..", "internal", "rpcsample", "testdata", "block-receipt-tx", "2026-09-23"))
	if err != nil {
		b.Fatal(err)
	}
	for _, record := range records {
		if record.RequestID != "receipt-one" || record.Source.ID != "s1" {
			continue
		}
		_, body, err := replay.BodyOf(record)
		if err != nil {
			b.Fatal(err)
		}
		var doc struct {
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(body, &doc); err != nil {
			b.Fatal(err)
		}
		return doc.Result
	}
	b.Fatal("receipt sample missing")
	return nil
}

func TestReceiptEncodingOptionalFieldsAndStatusRoot(t *testing.T) {
	raw := sampleReceipt(t)
	for _, name := range []string{"contractAddress", "to", "blobGasUsed", "blobGasPrice", "effectiveGasPrice", "type"} {
		t.Run(name, func(t *testing.T) {
			missing := deleteReceiptField(t, raw, name)
			encoded, err := receiptobj.Encode(missing)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(encoded, []byte(`"`+name+`"`)) {
				t.Fatalf("missing optional entered Value: %s", encoded)
			}
			encoded, err = receiptobj.Encode(replaceReceiptField(t, missing, name, "null"))
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(encoded, []byte(`"`+name+`"`)) {
				t.Fatalf("null optional entered Value: %s", encoded)
			}
		})
	}
	both := replaceReceiptField(t, raw, "root", `"0x`+strings.Repeat("ab", 32)+`"`)
	encoded, err := receiptobj.Encode(both)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(`"root"`)) || !bytes.Contains(encoded, []byte(`"status"`)) {
		t.Fatalf("status priority = %s", encoded)
	}
	rootOnly := deleteReceiptField(t, both, "status")
	encoded, err = receiptobj.Encode(rootOnly)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"root"`)) || bytes.Contains(encoded, []byte(`"status"`)) {
		t.Fatalf("root = %s", encoded)
	}
	if _, err := receiptobj.Encode(deleteReceiptField(t, rootOnly, "root")); err == nil {
		t.Fatal("missing status and root encoded")
	}
}

func TestReceiptBloomMismatchIsEncodeFault(t *testing.T) {
	raw := replaceReceiptField(t, sampleReceipt(t), "logsBloom", `"0x`+strings.Repeat("00", 255)+`01"`)
	policy := TransactionReceiptPolicy(rpc.WithQuorum(1))
	decision, err := policy.(rpc.CorroboratingPolicy).ResolveCorroborated(rpc.Corroboration{Floor: 1}, []rpc.Observation{
		observation(rpc.SourceRef{ID: "a"}, string(raw), ""),
	})
	if !errors.Is(err, rpc.ErrNoValidResponse) || decision.Status != rpc.Unknown || len(decision.Faults) != 1 || !strings.Contains(decision.Faults[0].Error(), "logsBloom disagrees with logs") {
		t.Fatalf("decision = %+v err %v", decision, err)
	}
}

func BenchmarkBlockReceiptsCanonicalValue(b *testing.B) {
	raw := benchmarkReceipt(b)
	for _, count := range []int{0, 1, 100, 1000} {
		b.Run(fmt.Sprintf("receipts-%d", count), func(b *testing.B) {
			rows := make([]json.RawMessage, count)
			for i := range rows {
				rows[i] = raw
			}
			body, err := json.Marshal(rows)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := receiptobj.EncodeBlock(body); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
