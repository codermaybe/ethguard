package log

import (
	"encoding/json"
	"strings"
	"testing"
)

// fullLog builds one complete log object; extra members ride before the
// closing brace (an empty string yields the base log, `,"member":value` adds
// one).
func fullLog(extra string) string {
	return `{"address":"0x` + strings.Repeat("11", 20) + `","topics":[],"data":"0x","blockNumber":"0x1",` +
		`"transactionHash":"0x` + strings.Repeat("22", 32) + `","transactionIndex":"0x0",` +
		`"blockHash":"0x` + strings.Repeat("33", 32) + `","logIndex":"0x0","removed":false` + extra + `}`
}

// The optional blockTimestamp is an encode-stage member: the shared object
// kernel that admission and the comparison key consult must keep accepting a
// log whose optional member fails its canonical encoding, and only the encode
// stage rejects it.

func TestTimestampEncodingStaysAnEncodeStageCheck(t *testing.T) {
	withBadTimestamp := fullLog(`,"blockTimestamp":"oops"`)
	if err := Validate(json.RawMessage(withBadTimestamp)); err != nil {
		t.Fatalf("admission kernel must accept a log whose optional timestamp is not a quantity, got %v", err)
	}
	_, err := EncodeOne(json.RawMessage(withBadTimestamp))
	if err == nil || !strings.Contains(err.Error(), "blockTimestamp") {
		t.Fatalf("encode stage must reject the malformed timestamp, got %v", err)
	}
}

func TestTimestampOptionalStatesKeepTheirShapes(t *testing.T) {
	absent, err := EncodeOne(json.RawMessage(fullLog("")))
	if err != nil || absent.BlockTimestamp != nil {
		t.Fatalf("absent timestamp must stay absent: %+v, %v", absent, err)
	}
	encoded, err := EncodeOne(json.RawMessage(fullLog(`,"blockTimestamp":null`)))
	if err != nil || encoded.BlockTimestamp != nil {
		t.Fatalf("null timestamp must fold to omission: %+v, %v", encoded, err)
	}
	kept, err := EncodeOne(json.RawMessage(fullLog(`,"blockTimestamp":"0x652f0c1e"`)))
	if err != nil || kept.BlockTimestamp == nil || *kept.BlockTimestamp != "0x652f0c1e" {
		t.Fatalf("a valid timestamp must survive: %+v, %v", kept, err)
	}
}

func TestBaseFieldErrorsStillPrecedeTheTimestampCheck(t *testing.T) {
	// A base-field violation and a timestamp violation together must report
	// the base field first: the timestamp check runs after the shared kernel.
	broken := strings.Replace(fullLog(`,"blockTimestamp":"oops"`), `"topics":[]`, `"topics":5`, 1)
	_, err := EncodeOne(json.RawMessage(broken))
	if err == nil || !strings.Contains(err.Error(), "topics") {
		t.Fatalf("base-field error must precede the timestamp check, got %v", err)
	}
	if err := Validate(json.RawMessage(broken)); err == nil || !strings.Contains(err.Error(), "topics") {
		t.Fatalf("admission kernel must see the same base-field error, got %v", err)
	}
}

func TestTimestampRoundTripThroughEncode(t *testing.T) {
	withTimestamp := `[` + fullLog(`,"blockTimestamp":"0x652f0c1e"`) + `]`
	out, err := Encode(json.RawMessage(withTimestamp))
	if err != nil {
		t.Fatalf("Encode = %v", err)
	}
	var probe []struct {
		BlockTimestamp *string `json:"blockTimestamp"`
	}
	if err := json.Unmarshal(out, &probe); err != nil {
		t.Fatal(err)
	}
	if len(probe) != 1 || probe[0].BlockTimestamp == nil || *probe[0].BlockTimestamp != "0x652f0c1e" {
		t.Fatalf("encoded array lost the timestamp: %s", out)
	}
}
