package accesslist

import (
	"encoding/json"
	"testing"
)

// Encoding is a fixpoint: the canonical form re-encodes to itself, so no
// fuzzed input can make the encoder oscillate between spellings.
func FuzzEncodeIdempotent(f *testing.F) {
	f.Add([]byte(`{"accessList":[{"address":"0x0000000000000000000000000000000000000001","storageKeys":["0x00000000000000000000000000000000000000000000000000000000000000ff"]}],"gasUsed":"0x5208","error":"out of gas"}`))
	f.Add([]byte(`{"accessList":[],"gasUsed":"0x0"}`))
	f.Add([]byte(`{"accessList":[{"address":"0x01","storageKeys":[]}]}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`[]`))
	f.Fuzz(func(t *testing.T, data []byte) {
		out, err := Encode(data)
		if err != nil {
			return
		}
		if !json.Valid(out) {
			t.Fatalf("accepted input produced invalid JSON: %s", out)
		}
		again, err := Encode(out)
		if err != nil {
			t.Fatalf("canonical output rejected on re-encoding: %v", err)
		}
		if string(again) != string(out) {
			t.Fatalf("encoding is not a fixpoint:\nfirst  %s\nsecond %s", out, again)
		}
	})
}
