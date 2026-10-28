package log

import (
	"encoding/json"
	"testing"
)

// Encoding is a fixpoint: the canonical form re-encodes to itself, and the
// shared receipt/getLogs encoder must hold that for any fuzzed payload.
func FuzzEncodeIdempotent(f *testing.F) {
	f.Add([]byte(`[{"address":"0x0000000aa232009084bd71a5797d089aa4edfad4","topics":["0x00000000000000000000000000000000000000000000000000000000000000ff"],"data":"0xdeadbeef","blockNumber":"0x1","transactionHash":"0xaa1c6c878d686b6c14b77c6b78ff23ae4b069cf941736678e9c1f8102b219b15","transactionIndex":"0x0","blockHash":"0x9bc1666a9b54a70b8fc7f4caf052c25d2bef94897c2815dc168163d6bc188b1e","logIndex":"0x1","removed":false,"blockTimestamp":"0x5","extra":"dropped"}]`))
	f.Add([]byte(`[]`))
	f.Add([]byte(`[{"address":"0x01"}]`))
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
