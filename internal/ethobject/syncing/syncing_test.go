package syncing

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
)

func TestSourceProjectionPrecedesTheClosedOfficialSchema(t *testing.T) {
	raw := json.RawMessage(`{"currentBlock":"0XAB","highestBlock":"0xCD","provider":{"text":"0xAB","counter":9007199254740993}}`)
	before := bytes.Clone(raw)
	if _, err := ethapi.NewSyncingResult(raw); err == nil {
		t.Fatal("strict constructor accepted unprojected provider response")
	}
	got, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := got.MarshalJSON()
	if err != nil || string(encoded) != `{"currentBlock":"0xab","highestBlock":"0xcd"}` {
		t.Fatalf("projected status = %s, %v", encoded, err)
	}
	if !bytes.Equal(raw, before) {
		t.Fatalf("source evidence was rewritten: %s", raw)
	}
	for _, raw := range []string{
		`true`, `null`, `[]`, `"0XAB"`,
		`{"currentBlock":null}`, `{"currentBlock":7}`,
		`{"currentBlock":"0X00"}`, `{"currentBlock":"0XAG"}`,
		`{"currentBlock":"0x1","currentBlock":"0X2"}`,
	} {
		if _, err := Decode(json.RawMessage(raw)); err == nil {
			t.Errorf("invalid source survived preprocessing: %s", raw)
		}
	}
}
