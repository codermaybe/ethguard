package accesslist

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// benchAccessList builds one createAccessList-shaped result over n items, the
// shape a corroborated construction read admits per source. The byte size is
// reported alongside the item count so the cost per admitted member is
// visible.
func benchAccessList(b *testing.B, n int) json.RawMessage {
	b.Helper()
	items := make([]string, n)
	for i := range items {
		items[i] = fmt.Sprintf(`{"address":"0x%040x","storageKeys":["0x%064x","0x%064x"]}`, i+1, i+1, i+2)
	}
	value := json.RawMessage(fmt.Sprintf(
		`{"accessList":[%s],"gasUsed":"0x5208","error":"execution reverted"}`,
		strings.Join(items, ",")))
	b.ReportAllocs()
	b.SetBytes(int64(len(value)))
	return value
}

func BenchmarkAccessListEncode(b *testing.B) {
	for _, n := range []int{0, 3, 64} {
		b.Run(fmt.Sprintf("items-%d", n), func(b *testing.B) {
			value := benchAccessList(b, n)
			b.ResetTimer()
			for range b.N {
				if _, err := Encode(value); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
