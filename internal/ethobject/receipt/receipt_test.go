package receipt

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

func TestReceiptEnvelopeRejectsMissingMalformedAndNullFields(t *testing.T) {
	base := receiptFixture(t)
	for _, field := range []string{"from", "effectiveGasPrice"} {
		for _, replacement := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`"bad"`)} {
			t.Run(field+string(replacement), func(t *testing.T) {
				var obj map[string]json.RawMessage
				if err := json.Unmarshal(base, &obj); err != nil {
					t.Fatal(err)
				}
				if replacement == nil {
					delete(obj, field)
				} else {
					obj[field] = replacement
				}
				raw, err := json.Marshal(obj)
				if err != nil {
					t.Fatal(err)
				}
				assertReceiptRejected(t, raw)
			})
		}
	}
}

func assertReceiptRejected(t *testing.T, raw json.RawMessage) {
	t.Helper()
	if _, err := Encode(raw); err == nil {
		t.Fatalf("Encode accepted %s", raw)
	}
	if _, err := PrepareTransaction(rpc.Request{Params: []any{receiptTxHash}}, raw); err == nil {
		t.Fatalf("PrepareTransaction accepted %s", raw)
	}
	block := append(append([]byte{'['}, raw...), ']')
	if _, err := EncodeBlock(block); err == nil {
		t.Fatalf("EncodeBlock accepted %s", raw)
	}
}

func TestReceiptOptionalNonnullableAndByteBoundaries(t *testing.T) {
	base := receiptFixture(t)
	for _, field := range []string{"blobGasUsed", "blobGasPrice", "type", "status", "root"} {
		t.Run(field+" null", func(t *testing.T) { assertReceiptRejected(t, replaceField(t, base, field, nil)) })
	}
	for _, kind := range []string{"0x100", "0xffff", "bad"} {
		t.Run(kind, func(t *testing.T) { assertReceiptRejected(t, replaceField(t, base, "type", kind)) })
	}
	for _, kind := range []string{"0x0", "0xff", "0XFF", "0x00"} {
		raw := replaceField(t, base, "type", kind)
		encoded, err := Encode(raw)
		if err != nil {
			t.Fatalf("type %s: %v", kind, err)
		}
		if _, err := PrepareTransaction(rpc.Request{Params: []any{receiptTxHash}}, raw); err != nil {
			t.Fatalf("admit type %s: %v", kind, err)
		}
		again, err := Encode(encoded)
		if err != nil || !bytes.Equal(encoded, again) {
			t.Fatalf("type %s not canonical: %s, %v", kind, again, err)
		}
	}
	assertReceiptRejected(t, replaceField(t, base, "root", "bad"))
	assertReceiptRejected(t, replaceField(t, base, "status", "0x2"))
}

func TestReceiptNullableAddressesPreserveThreeStates(t *testing.T) {
	for _, field := range []string{"to", "contractAddress"} {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(receiptFixture(t), &obj); err != nil {
			t.Fatal(err)
		}
		delete(obj, field)
		missing, err := json.Marshal(obj)
		if err != nil {
			t.Fatal(err)
		}
		var outputs []json.RawMessage
		for _, raw := range []json.RawMessage{missing, replaceField(t, missing, field, nil), replaceField(t, missing, field, "0x0000000000000000000000000000000000000000")} {
			encoded, err := Encode(raw)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := PrepareTransaction(rpc.Request{Params: []any{receiptTxHash}}, raw); err != nil {
				t.Fatal(err)
			}
			outputs = append(outputs, encoded)
		}
		if bytes.Equal(outputs[0], outputs[1]) || bytes.Equal(outputs[1], outputs[2]) || bytes.Equal(outputs[0], outputs[2]) {
			t.Fatalf("%s states collapsed: %s", field, outputs)
		}
		if bytes.Contains(outputs[0], []byte(`"`+field+`"`)) || !bytes.Contains(outputs[1], []byte(`"`+field+`":null`)) {
			t.Fatalf("%s presence changed: %s", field, outputs)
		}
	}
}
