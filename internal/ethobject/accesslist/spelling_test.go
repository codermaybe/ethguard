package accesslist

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestHexSpellingCompatibilityDoesNotChangeTextOrRepairValues(t *testing.T) {
	for _, message := range []string{"0xAB", "0XAB"} {
		raw := json.RawMessage(`{"accessList":[{"address":"0X` + strings.Repeat("AB", 20) + `","storageKeys":["0X` + strings.Repeat("CD", 32) + `"],"vendor":"0XAB"}],"gasUsed":"0XFF","error":"` + message + `","vendor":{"error":"0xCD"}}`)
		before := bytes.Clone(raw)
		out, err := Encode(raw)
		if err != nil {
			t.Fatal(err)
		}
		want := `{"accessList":[{"address":"0x` + strings.Repeat("ab", 20) + `","storageKeys":["0x` + strings.Repeat("cd", 32) + `"]}],"error":"` + message + `","gasUsed":"0xff"}`
		if string(out) != want || !bytes.Equal(raw, before) {
			t.Fatalf("canonical %s, original %s; want %s", out, raw, want)
		}
	}
	for _, raw := range []string{
		`{"accessList":[],"gasUsed":"0X00"}`,
		`{"accessList":[],"gasUsed":12}`,
		`{"accessList":[],"gasUsed":"0XAG"}`,
		`{"accessList":[{"address":"0XAB","storageKeys":[]}]}`,
		`{"accessList":[],"error":{"text":"0XAB"}}`,
	} {
		if _, err := Encode(json.RawMessage(raw)); err == nil {
			t.Errorf("invalid source survived preprocessing: %s", raw)
		}
	}
}
