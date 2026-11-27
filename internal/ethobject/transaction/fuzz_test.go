package transaction

import (
	"encoding/json"
	"testing"
)

// Preparation is a fixpoint: preparing a canonical transaction preserves
// its bytes, and the output stays valid JSON.
func FuzzPrepareIdempotent(f *testing.F) {
	f.Add([]byte(`{"hash":"0xaa1c6c878d686b6c14b77c6b78ff23ae4b069cf941736678e9c1f8102b219b15","nonce":"0x1","blockNumber":"0x18d592f","vendor":"dropped"}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		candidate, err := Prepare(data)
		if err != nil {
			return
		}
		out := candidate.Canonical()
		if !json.Valid(out) {
			t.Fatalf("accepted input produced invalid JSON: %s", out)
		}
		again, err := Prepare(out)
		if err != nil {
			t.Fatalf("canonical output rejected on re-encoding: %v", err)
		}
		if string(again.Canonical()) != string(out) {
			t.Fatalf("preparation is not a fixpoint:\nfirst  %s\nsecond %s", out, again.Canonical())
		}
	})
}
