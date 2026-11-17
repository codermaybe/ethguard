package log

import (
	"encoding/json"
	"strings"
	"testing"
)

// The encode stage extracts values through the item struct's own matching:
// keys match their member case-insensitively and the last occurrence wins.
// A case-variant alias therefore overrides the canonical member's value for
// the protected bytes, and a malformed alias is rejected even when the
// canonical member carried a legal value. The admission kernel keeps its
// exact-key contract; the two stages do not share one matching rule.

func logBase() string {
	return `{"address":"0x` + strings.Repeat("11", 20) + `","topics":[],"data":"0x","blockNumber":"0x1",` +
		`"transactionHash":"0x` + strings.Repeat("22", 32) + `","transactionIndex":"0x0",` +
		`"blockHash":"0x` + strings.Repeat("33", 32) + `","logIndex":"0x0","removed":false`
}

func TestAddressAliasOverridesTheEncodedValue(t *testing.T) {
	overridden := logBase() + `,"Address":"0x` + strings.Repeat("22", 20) + `"}`
	out, err := EncodeOne(json.RawMessage(overridden))
	if err != nil {
		t.Fatalf("alias override must encode, got %v", err)
	}
	if out.Address != "0x"+strings.Repeat("22", 20) {
		t.Fatalf("the last occurrence of the member must win, got %s", out.Address)
	}
	if err := Validate(json.RawMessage(overridden)); err != nil {
		t.Fatalf("the admission kernel must keep accepting the alias document, got %v", err)
	}
}

func TestTimestampAliasIsCheckedAtTheEncodeStage(t *testing.T) {
	illegal := logBase() + `,"BlockTimestamp":"oops"}`
	if err := Validate(json.RawMessage(illegal)); err != nil {
		t.Fatalf("the admission kernel ignores the optional member, got %v", err)
	}
	_, err := EncodeOne(json.RawMessage(illegal))
	if err == nil || !strings.Contains(err.Error(), "blockTimestamp") {
		t.Fatalf("an alias-carried malformed timestamp must reject at the encode stage, got %v", err)
	}
	kept, err := EncodeOne(json.RawMessage(logBase()+`,"BlockTimestamp":"0x2"}`))
	if err != nil || kept.BlockTimestamp == nil || *kept.BlockTimestamp != "0x2" {
		t.Fatalf("an alias-carried legal timestamp must survive: %+v, %v", kept, err)
	}
}

func TestDataAliasRejectsMalformedBytesAtTheEncodeStage(t *testing.T) {
	_, err := EncodeOne(json.RawMessage(logBase() + `,"Data":"oops"}`))
	if err == nil || !strings.Contains(err.Error(), "data") {
		t.Fatalf("an alias-carried malformed data must reject at the encode stage, got %v", err)
	}
	if err := Validate(json.RawMessage(logBase() + `,"Data":"oops"}`)); err != nil {
		t.Fatalf("the admission kernel must keep accepting the alias document, got %v", err)
	}
}

func TestTopicsAliasKeepsTheBaselineShape(t *testing.T) {
	out, err := EncodeOne(json.RawMessage(logBase() + `,"Topics":null}`))
	if err != nil || out.Topics == nil || len(out.Topics) != 0 {
		t.Fatalf("a null alias over an empty array stays an empty projection: %+v, %v", out, err)
	}
}

func TestAliasOnlyDocumentsStillFailTheAdmissionKernel(t *testing.T) {
	// A member present only under its alias is missing for the exact-key
	// admission contract, at every stage.
	aliasOnly := `{"ADDRESS":"0x` + strings.Repeat("11", 20) + `","topics":[],"data":"0x","blockNumber":"0x1",` +
		`"transactionHash":"0x` + strings.Repeat("22", 32) + `","transactionIndex":"0x0",` +
		`"blockHash":"0x` + strings.Repeat("33", 32) + `","logIndex":"0x0","removed":false}`
	if err := Validate(json.RawMessage(aliasOnly)); err == nil || !strings.Contains(err.Error(), "missing address") {
		t.Fatalf("alias-only member must stay missing for the admission kernel, got %v", err)
	}
	if _, err := EncodeOne(json.RawMessage(aliasOnly)); err == nil || !strings.Contains(err.Error(), "missing address") {
		t.Fatalf("alias-only member must fail the encode stage's kernel pass too, got %v", err)
	}
}
