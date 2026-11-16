package accesslist

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// benchAccessList builds one createAccessList-shaped result over n items,
// the shape a corroborated construction read admits per source.
func benchAccessList(n int) json.RawMessage {
	items := make([]string, n)
	for i := range items {
		items[i] = fmt.Sprintf(`{"address":"0x%040x","storageKeys":["0x%064x","0x%064x"]}`, i+1, i+1, i+2)
	}
	return json.RawMessage(fmt.Sprintf(
		`{"accessList":[%s],"gasUsed":"0x5208","error":"execution reverted"}`,
		strings.Join(items, ",")))
}

func BenchmarkAccessListEncode(b *testing.B) {
	value := benchAccessList(64)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Encode(value); err != nil {
			b.Fatal(err)
		}
	}
}
