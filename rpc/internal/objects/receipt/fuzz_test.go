package receipt

import (
	"testing"

	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

// The canonical form re-encodes to itself and keeps the same pruned
// comparison key: whatever the encoder accepts must vote on the same key it
// was accepted under, or sources could shift clusters by encoding alone.
func FuzzEncodeKeyStableAndIdempotent(f *testing.F) {
	f.Add([]byte(`{"transactionHash":"0xaa1c6c878d686b6c14b77c6b78ff23ae4b069cf941736678e9c1f8102b219b15","transactionIndex":"0x0","blockHash":"0x9bc1666a9b54a70b8fc7f4caf052c25d2bef94897c2815dc168163d6bc188b1e","blockNumber":"0x18d592f","from":"0x0000000aa232009084bd71a5797d089aa4edfad4","cumulativeGasUsed":"0x1","gasUsed":"0x1","effectiveGasPrice":"0x1","logsBloom":"0x00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000","logs":[],"status":"0x1","type":"0x0","vendor":"dropped"}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		out, err := Encode(data)
		if err != nil {
			return
		}
		want, err := Normalize(data, wire.PruneMethod, "eth_getTransactionReceipt")
		if err != nil {
			t.Fatalf("Encode accepted what Normalize rejects: %v", err)
		}
		got, err := Normalize(out, wire.PruneMethod, "eth_getTransactionReceipt")
		if err != nil {
			t.Fatalf("canonical output rejected by Normalize: %v", err)
		}
		if got != want {
			t.Fatalf("comparison key changed under encoding:\ninput     %s\ncanonical %s\nkeys %q vs %q", data, out, want, got)
		}
		again, err := Encode(out)
		if err != nil || string(again) != string(out) {
			t.Fatalf("encoding is not a fixpoint (%v)", err)
		}
	})
}
