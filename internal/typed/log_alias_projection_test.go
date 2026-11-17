package typed

import (
	"encoding/json"
	"strings"
	"testing"
)

// The typed subscription projection decodes push events through
// typed.DecodeLog, which routes the raw event through the shared log kernel's
// encode stage. The alias semantics the kernel owns survive to this public
// entry: a case-variant alias overrides the projected value, and a malformed
// alias-carried member rejects the event.

func subLogBase() string {
	return `{"address":"0x` + strings.Repeat("11", 20) + `","topics":["0x` + strings.Repeat("22", 32) + `"],"data":"0xabcd","blockNumber":"0x1",` +
		`"transactionHash":"0x` + strings.Repeat("33", 32) + `","transactionIndex":"0x0",` +
		`"blockHash":"0x` + strings.Repeat("44", 32) + `","logIndex":"0x0","removed":false`
}

func TestSubscriptionLogProjectionResolvesTheLastAliasOccurrence(t *testing.T) {
	event, err := DecodeLog(json.RawMessage(subLogBase() + `,"Address":"0x` + strings.Repeat("55", 20) + `"}`))
	if err != nil {
		t.Fatalf("subscription projection must resolve the alias, got %v", err)
	}
	if event.Address == nil || string(*event.Address) != "0x"+strings.Repeat("55", 20) {
		t.Fatalf("the projected log must carry the last occurrence's address, got %v", event.Address)
	}
}

func TestSubscriptionLogProjectionRejectsAliasCarriedMalformedData(t *testing.T) {
	if _, err := DecodeLog(json.RawMessage(subLogBase() + `,"Data":"oops"}`)); err == nil {
		t.Fatal("an alias-carried malformed data must reject the subscription event")
	}
}

func TestSubscriptionLogProjectionKeepsAliasCarriedTimestamp(t *testing.T) {
	event, err := DecodeLog(json.RawMessage(subLogBase() + `,"BlockTimestamp":"0x2"}`))
	if err != nil || event.BlockTimestamp == nil || string(*event.BlockTimestamp) != "0x2" {
		t.Fatalf("the alias-carried timestamp must survive the projection: %+v, %v", event, err)
	}
	if _, err := DecodeLog(json.RawMessage(subLogBase() + `,"BlockTimestamp":"oops"}`)); err == nil {
		t.Fatal("an alias-carried malformed timestamp must reject the subscription event")
	}
}
