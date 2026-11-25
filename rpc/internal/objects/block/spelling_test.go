package block

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/internal/typed"
)

func TestBlockBodySpellingCompatibilityReachesAdmissionAndPublicDecode(t *testing.T) {
	raw, req := completeBlock(t)
	want, err := Encode(raw)
	if err != nil {
		t.Fatal(err)
	}
	changed := mutateBlock(t, raw, func(block, tx map[string]json.RawMessage) {
		block["size"] = json.RawMessage(`"0X321"`)
		for _, field := range []string{"type", "hash", "from", "to", "input", "gas", "blockHash"} {
			tx[field] = json.RawMessage(strings.ToUpper(string(tx[field])))
		}
		tx["v"], tx["yParity"] = json.RawMessage(`"0X00"`), json.RawMessage(`"0x00"`)
	})
	before := bytes.Clone(changed)
	got, err := Encode(changed)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("equivalent body spelling changed canonical block: %v", err)
	}
	if _, err := AdmitBlock(context.Background(), req, changed, true, nil); err != nil {
		t.Fatalf("source-local body compatibility rejected during admission: %v", err)
	}
	if _, err := typed.DecodeBlock(changed); err != nil {
		t.Fatalf("public raw block decoder rejected compatible body: %v", err)
	}
	if !bytes.Equal(before, changed) {
		t.Fatal("source evidence mutated")
	}
	for _, field := range []string{"v", "yParity"} {
		invalid := mutateBlock(t, raw, func(_ map[string]json.RawMessage, tx map[string]json.RawMessage) {
			tx[field] = json.RawMessage(`"0x000"`)
		})
		if _, err := Encode(invalid); err == nil {
			t.Fatalf("block repaired invalid %s byte width", field)
		}
		if _, err := typed.DecodeBlock(invalid); err == nil {
			t.Fatalf("public block decoder repaired invalid %s byte width", field)
		}
	}
}
