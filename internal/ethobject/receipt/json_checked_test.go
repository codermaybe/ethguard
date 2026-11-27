package receipt

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/core/types"

	"github.com/codermaybe/ethguard/internal/jsonsafe"
	"github.com/codermaybe/ethguard/rpc"
)

func TestJSONCheckedReceiptPreservesBytesAndErrors(t *testing.T) {
	base := receiptFixture(t, logFixture())
	for _, raw := range []json.RawMessage{
		base, receiptFixture(t), []byte(`null`), []byte(`{}`), []byte(`[]`),
		deleteField(t, base, "logs"), replaceField(t, base, "status", "0x2"),
		replaceField(t, base, "logsBloom", "0x"+strings.Repeat("00", 256)),
		aliasReceipt(t, `,"BlockTimestamp":"oops"`, nil),
		aliasReceipt(t, `,"Address":"0x`+strings.Repeat("77", 20)+`"`, func(row map[string]any) {
			row["address"] = "0x" + strings.Repeat("77", 20)
		}),
	} {
		original := bytes.Clone(raw)
		if err := jsonsafe.Validate(raw); err != nil {
			t.Fatal(err)
		}
		want, wantErr := Encode(raw)
		got, gotErr := encodeJSONChecked(raw)
		if !bytes.Equal(got, want) || (got == nil) != (want == nil) || !reflect.DeepEqual(gotErr, wantErr) {
			t.Fatalf("encode checked = %s, %v; raw = %s, %v", got, gotErr, want, wantErr)
		}
		want, wantReceipt, wantErr := DecodeWithCanonical(raw)
		got, gotReceipt, gotErr := decodeWithCanonicalJSONChecked(raw)
		if !bytes.Equal(got, want) || !reflect.DeepEqual(gotReceipt, wantReceipt) || !reflect.DeepEqual(gotErr, wantErr) {
			t.Fatalf("decode checked = %s, %v; raw = %s, %v", got, gotErr, want, wantErr)
		}
		if !bytes.Equal(raw, original) {
			t.Fatal("checked receipt processing modified input")
		}
	}
}

func TestReceiptJSONGateAndDirectPreparationKeepErrorOrder(t *testing.T) {
	base := receiptFixture(t)
	duplicate := append([]byte(`{"status":"0x1",`), base[1:]...)
	foreign := replaceField(t, base, "blockHash", "0x"+strings.Repeat("22", 32))
	raw := append(append(append([]byte{'['}, foreign...), ','), duplicate...)
	raw = append(raw, ']')
	if _, err := EncodeBlock(raw); !errors.Is(err, jsonsafe.ErrDuplicateKey) {
		t.Fatalf("whole-array encode gate = %v", err)
	}
	// PrepareBlock has a different, frozen direct-entry order: the first
	// receipt binds before the second receipt is validated.
	if _, err := PrepareBlock(rpc.Request{Params: []any{receiptBlkHash}}, raw); err == nil || !strings.Contains(err.Error(), "receipt 0 block hash") {
		t.Fatalf("direct preparation error order = %v", err)
	}
	if _, err := PrepareTransaction(rpc.Request{}, duplicate); !errors.Is(err, rpc.ErrInvalidRequest) {
		t.Fatalf("transaction request must precede payload safety: %v", err)
	}
	header, txs, _ := commitmentFixture(t, false, types.LegacyTxType)
	if _, err := VerifyBlockRows(header, txs, raw); !errors.Is(err, jsonsafe.ErrDuplicateKey) {
		t.Fatalf("commitment safety must precede receipt count: %v", err)
	}
	if _, err := VerifyBlockRows(nil, txs, raw); err == nil || err.Error() != "receipt commitment: missing or invalid header" {
		t.Fatalf("commitment header must precede safety: %v", err)
	}
}
