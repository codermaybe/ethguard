package block

import (
	"encoding/json"
	"testing"
)

// Encoding is a fixpoint: the canonical form re-encodes to itself, for both
// the hash-addressed and number-addressed block shapes.
func FuzzEncodeIdempotent(f *testing.F) {
	f.Add([]byte(`{"number":"0x18d592f","hash":"0x9bc1666a9b54a70b8fc7f4caf052c25d2bef94897c2815dc168163d6bc188b1e","parentHash":"0x9bc1666a9b54a70b8fc7f4caf052c25d2bef94897c2815dc168163d6bc188b1e","nonce":"0x0000000000000000","transactions":[],"extra":"dropped"}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`{}`))
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
