package receipt

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
)

func TestReceiptCandidateSeparatesCompatibilityFromSchema(t *testing.T) {
	baseline, err := Encode(decodeReceiptFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(decodeShoutedSpelling(t, baseline), &fields); err != nil {
		t.Fatal(err)
	}
	fields["provider"] = json.RawMessage(`{"text":"0xAB","type":9007199254740993}`)
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	before := bytes.Clone(raw)
	if _, err := ethapi.NewReceipt(raw); err == nil {
		t.Fatal("strict constructor accepted an unprojected source")
	}
	got, err := Encode(raw)
	if err != nil || !bytes.Equal(got, baseline) || !bytes.Equal(raw, before) {
		t.Fatalf("source projection = %s, %v; source changed=%v", got, err, !bytes.Equal(raw, before))
	}
	for _, malformed := range []json.RawMessage{json.RawMessage(`"0X00"`), json.RawMessage(`"0XAG"`), json.RawMessage(`1`), json.RawMessage(`null`)} {
		fields["gasUsed"] = malformed
		raw, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Encode(raw); err == nil {
			t.Errorf("invalid gasUsed survived preprocessing: %s", malformed)
		}
	}
}
