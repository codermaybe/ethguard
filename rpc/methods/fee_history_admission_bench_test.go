package methods

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

// Both rows run complete request admission, including count and exact-key
// reward-width binding. The prior normalized-key chain remains a cost control.
func BenchmarkFeeHistoryRequestAdmission(b *testing.B) {
	for _, blocks := range []int{1, 64} {
		value := feeHistoryAdmissionPayload(b, blocks)
		req := rpc.Request{Method: MethodFeeHistory, Params: []any{uint64(blocks), "latest", []float64{10, 50}}}
		for _, path := range []struct {
			name  string
			admit AdmissionFunc
		}{
			{"admit", admitFeeHistory},
			{"normalized-key-chain", admitFeeHistoryViaNormalizedKey},
		} {
			b.Run(fmt.Sprintf("blocks-%d/%s", blocks, path.name), func(b *testing.B) {
				ctx := context.Background()
				b.ReportAllocs()
				b.SetBytes(int64(len(value)))
				b.ResetTimer()
				for range b.N {
					if _, err := path.admit(ctx, req, value, nil); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func feeHistoryAdmissionPayload(b *testing.B, blocks int) json.RawMessage {
	b.Helper()
	base, blob := make([]string, blocks+1), make([]string, blocks+1)
	gas, blobRatios := make([]json.Number, blocks), make([]json.Number, blocks)
	reward := make([][]string, blocks)
	for i := 0; i < blocks; i++ {
		base[i] = fmt.Sprintf("0x%x", 30_000_000_000+i)
		blob[i] = fmt.Sprintf("0x%x", i+1)
		gas[i], blobRatios[i] = json.Number("0.5"), json.Number("0.25")
		reward[i] = []string{fmt.Sprintf("0x%x", 1_000_000_000+i), fmt.Sprintf("0x%x", 2_000_000_000+i)}
	}
	base[blocks], blob[blocks] = "0x1d1a94a200", "0x2"
	value, err := json.Marshal(struct {
		Oldest    string        `json:"oldestBlock"`
		Base      []string      `json:"baseFeePerGas"`
		Gas       []json.Number `json:"gasUsedRatio"`
		Reward    [][]string    `json:"reward"`
		Blob      []string      `json:"baseFeePerBlobGas"`
		BlobRatio []json.Number `json:"blobGasUsedRatio"`
	}{"0x1200000", base, gas, reward, blob, blobRatios})
	if err != nil {
		b.Fatal(err)
	}
	return value
}
