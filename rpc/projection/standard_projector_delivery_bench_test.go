package projection

import (
	"encoding/json"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

var deliveredProjectionExtensions rpc.ExtensionSet

// The projector is assembled once, as in the runtime registry. Each measured
// call classifies accepted observations and builds the real fingerprint-bound
// ExtensionSet, including evidence copies for unknown fields.
func BenchmarkStandardProjectorDelivery(b *testing.B) {
	for _, tt := range []struct {
		name       string
		fields     []StandardField
		value      json.RawMessage
		accepted   []string
		extensions int
	}{
		{name: "ScalarBalance", fields: []StandardField{{Path: "", Required: true}}, value: json.RawMessage(`"0x64"`), accepted: []string{""}},
		{name: "ScalarWithExtension", fields: []StandardField{{Path: "", Required: true}}, value: json.RawMessage(`"0x64"`), accepted: []string{"/vendor", ""}, extensions: 1},
		{
			name: "WildcardObject",
			fields: []StandardField{
				{Path: "/blockHash", Required: true},
				{Path: "/logs", Required: true},
				{Path: "/logs/*/address", Required: true},
				{Path: "/logs/*/topics", Required: true},
				{Path: "/logs/*/topics/*", Required: true},
				{Path: "/status", Required: true},
			},
			value:      json.RawMessage(`{"blockHash":"0x01","logs":[{"address":"0x02","topics":["0x03"]}],"status":"0x1"}`),
			accepted:   []string{"/vendorZ", "/logs/0/topics/0", "/status", "/logs", "/logs/0/address", "/blockHash", "/logs/0/topics", "/vendorA"},
			extensions: 2,
		},
	} {
		b.Run(tt.name, func(b *testing.B) {
			projector := NewStandardProjector(tt.fields)
			evaluation := rpc.Evaluation{Accepted: projectionObservations(tt.accepted...)}
			b.ReportAllocs()
			b.ResetTimer()
			var set rpc.ExtensionSet
			for i := 0; i < b.N; i++ {
				var err error
				set, err = projector.ProjectObservation(tt.value, evaluation)
				if err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			if len(set.Fields) != tt.extensions {
				b.Fatalf("extensions = %d, want %d", len(set.Fields), tt.extensions)
			}
			deliveredProjectionExtensions = set
			b.ReportMetric(float64(len(tt.accepted)), "accepted-fields/op")
			b.ReportMetric(float64(tt.extensions), "extensions/op")
		})
	}
}
