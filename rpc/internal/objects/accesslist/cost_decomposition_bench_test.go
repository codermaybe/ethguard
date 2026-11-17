package accesslist

import (
	"encoding/json"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
)

// BenchmarkAccessListCostDecomposition is a diagnostic-only split of the
// encode path for cost attribution; none of its variants is a production
// behavior. shape+model runs the per-item shape/presence pass, generated
// scalar constructors and item assembly; constructor runs the generated result
// constructor alone over the raw document; marshal measures only the final
// serialization of a pre-built result.
func BenchmarkAccessListCostDecomposition(b *testing.B) {
	value := benchAccessList(b, 64)
	var fields struct {
		AccessList json.RawMessage `json:"accessList"`
	}
	if err := json.Unmarshal(value, &fields); err != nil {
		b.Fatal(err)
	}
	if _, err := Encode(value); err != nil {
		b.Fatal(err)
	}
	result, err := ethapi.NewAccessListResult(value)
	if err != nil {
		b.Fatal(err)
	}
	b.Run("shape-plus-model", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			if _, err := checkItems(fields.AccessList); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("constructor", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			if _, err := ethapi.NewAccessListResult(value); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("marshal", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			if _, err := result.MarshalJSON(); err != nil {
				b.Fatal(err)
			}
		}
	})
}
