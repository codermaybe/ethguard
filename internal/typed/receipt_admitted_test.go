package typed

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	ethereum "github.com/ethereum/go-ethereum"

	"github.com/codermaybe/ethguard/ethapi"
	receiptobject "github.com/codermaybe/ethguard/internal/ethobject/receipt"
	"github.com/codermaybe/ethguard/internal/jsonsafe"
)

func TestAdmittedReceiptProjectionMatchesRawOnCanonicalValues(t *testing.T) {
	base := typedReceiptFixture(t)
	for _, state := range []string{"absent", "null", "address"} {
		t.Run(state, func(t *testing.T) {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(base, &fields); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"to", "contractAddress"} {
				switch state {
				case "absent":
					delete(fields, name)
				case "null":
					fields[name] = json.RawMessage(`null`)
				case "address":
					fields[name] = json.RawMessage(`"0x0000000000000000000000000000000000000000"`)
				}
			}
			raw, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			canonical, err := receiptobject.Encode(raw)
			if err != nil {
				t.Fatal(err)
			}
			original := bytes.Clone(canonical)
			want, wantErr := DecodeReceipt(canonical)
			got, gotErr := DecodeReceiptAdmitted(canonical)
			if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(gotErr, wantErr) {
				t.Fatalf("admitted = %+v, %v; raw = %+v, %v", got, gotErr, want, wantErr)
			}
			batch := append(append([]byte{'['}, canonical...), ']')
			wantRows, wantErr := DecodeReceipts(batch)
			gotRows, gotErr := DecodeReceiptsAdmitted(batch)
			if !reflect.DeepEqual(gotRows, wantRows) || !reflect.DeepEqual(gotErr, wantErr) {
				t.Fatalf("admitted rows = %+v, %v; raw = %+v, %v", gotRows, gotErr, wantRows, wantErr)
			}
			if !bytes.Equal(canonical, original) {
				t.Fatal("admitted projection modified canonical input")
			}
			for i := range canonical {
				canonical[i] = ' '
			}
			for i := range batch {
				batch[i] = ' '
			}
			if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(gotRows, wantRows) {
				t.Fatal("admitted projection aliases input storage")
			}
		})
	}
}

func TestAdmittedReceiptNullAndEmptyBatch(t *testing.T) {
	for _, raw := range []json.RawMessage{[]byte(`null`), []byte(" \tnull\r\n")} {
		if value, err := DecodeReceiptAdmitted(raw); value != nil || !errors.Is(err, ethereum.NotFound) {
			t.Fatalf("receipt null = %+v, %v", value, err)
		}
		if values, err := DecodeReceiptsAdmitted(raw); values != nil || !errors.Is(err, ethereum.NotFound) {
			t.Fatalf("receipts null = %+v, %v", values, err)
		}
	}
	if values, err := DecodeReceiptsAdmitted([]byte(`[]`)); err != nil || values == nil || len(values) != 0 {
		t.Fatalf("empty receipts = %+v, %v", values, err)
	}
}

func TestAdmittedReceiptStillChecksFinalJSONSafety(t *testing.T) {
	tooManyKeys := make(map[string]int, jsonsafe.MaxObjectKeys+1)
	for i := 0; i <= jsonsafe.MaxObjectKeys; i++ {
		tooManyKeys[fmt.Sprint(i)] = i
	}
	keys, err := json.Marshal(tooManyKeys)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []json.RawMessage{
		[]byte(`{"status":"0x1","status":"0x1"}`),
		[]byte(strings.Repeat("[", jsonsafe.MaxNestingDepth+1) + `0` + strings.Repeat("]", jsonsafe.MaxNestingDepth+1)),
		keys, []byte(`{} {}`),
		append(append([]byte{'"'}, bytes.Repeat([]byte{'a'}, int(jsonsafe.MaxBodyBytes)-1)...), '"'),
	} {
		if err := jsonsafe.Validate(raw); err == nil {
			t.Fatal("unsafe fixture accepted")
		}
		_, wantErr := DecodeReceipt(raw)
		_, gotErr := DecodeReceiptAdmitted(raw)
		if !reflect.DeepEqual(gotErr, wantErr) {
			t.Fatalf("receipt safety error = %v; raw = %v", gotErr, wantErr)
		}
		_, wantErr = DecodeReceipts(raw)
		_, gotErr = DecodeReceiptsAdmitted(raw)
		if !reflect.DeepEqual(gotErr, wantErr) {
			t.Fatalf("receipts safety error = %v; raw = %v", gotErr, wantErr)
		}
	}
}

func TestAdmittedReceiptKeepsGeneratedProjectionGate(t *testing.T) {
	canonical, err := receiptobject.Encode(typedReceiptFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(canonical, &fields); err != nil {
		t.Fatal(err)
	}
	delete(fields, "from")
	broken, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if value, err := DecodeReceiptAdmitted(broken); value != nil || err == nil {
		t.Fatalf("missing generated field accepted: %+v, %v", value, err)
	}
	if values, err := DecodeReceiptsAdmitted(append(append([]byte{'['}, broken...), ']')); values != nil || err == nil {
		t.Fatalf("missing batch generated field accepted: %+v, %v", values, err)
	}
}

// Both branches consume the same canonical bytes. This isolates delivery
// boundary work; it is not a before/after estimate for a complete RPC call.
func BenchmarkReceiptProjectionBoundary(b *testing.B) {
	for _, count := range []int{0, 1, 64} {
		b.Run(fmt.Sprintf("logs-%d", count), func(b *testing.B) {
			canonical, err := receiptobject.Encode(projectionReceiptFixture(b, count))
			if err != nil {
				b.Fatal(err)
			}
			for _, entry := range []struct {
				name string
				run  func(json.RawMessage) (*ethapi.Receipt, error)
			}{{"raw", DecodeReceipt}, {"admitted", DecodeReceiptAdmitted}} {
				b.Run(entry.name, func(b *testing.B) {
					b.ReportAllocs()
					b.SetBytes(int64(len(canonical)))
					b.ResetTimer()
					for range b.N {
						if _, err := entry.run(canonical); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		})
	}
}

func BenchmarkReceiptsProjectionBoundary(b *testing.B) {
	canonical, err := receiptobject.Encode(projectionReceiptFixture(b, 1))
	if err != nil {
		b.Fatal(err)
	}
	for _, count := range []int{0, 1, 64} {
		b.Run(fmt.Sprintf("receipts-%d", count), func(b *testing.B) {
			rows := make([]json.RawMessage, count)
			for i := range rows {
				rows[i] = canonical
			}
			batch, err := json.Marshal(rows)
			if err != nil {
				b.Fatal(err)
			}
			for _, entry := range []struct {
				name string
				run  func(json.RawMessage) ([]ethapi.Receipt, error)
			}{{"raw", DecodeReceipts}, {"admitted", DecodeReceiptsAdmitted}} {
				b.Run(entry.name, func(b *testing.B) {
					b.ReportAllocs()
					b.SetBytes(int64(len(batch)))
					b.ResetTimer()
					for range b.N {
						if _, err := entry.run(batch); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		})
	}
}
