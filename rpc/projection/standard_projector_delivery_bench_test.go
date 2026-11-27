package projection

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/ethapi"
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
	for _, fixture := range largeProjectionFixtures() {
		b.Run(fixture.name, func(b *testing.B) {
			core := fixture.build(b)
			projector, err := NewSchemaProjector(fixture.method)
			if err != nil {
				b.Fatal(err)
			}
			for _, extensions := range []int{0, 2} {
				b.Run(fmt.Sprintf("Extensions%d", extensions), func(b *testing.B) {
					evaluation := largeProjectionEvaluation(b, core, extensions)
					b.ReportAllocs()
					b.ResetTimer()
					var set rpc.ExtensionSet
					for i := 0; i < b.N; i++ {
						var err error
						set, err = projector.ProjectObservation(core, evaluation)
						if err != nil {
							b.Fatal(err)
						}
					}
					b.StopTimer()
					if len(set.Fields) != extensions {
						b.Fatalf("extensions = %d, want %d", len(set.Fields), extensions)
					}
					deliveredProjectionExtensions = set
					b.ReportMetric(float64(len(core)), "core-bytes/op")
					b.ReportMetric(float64(len(evaluation.Accepted)), "accepted-fields/op")
					b.ReportMetric(float64(extensions), "extensions/op")
					b.ReportMetric(3, "sources/op")
				})
			}
		})
	}
}

type largeProjectionFixture struct {
	name   string
	method string
	build  func(testing.TB) json.RawMessage
}

func largeProjectionFixtures() []largeProjectionFixture {
	return []largeProjectionFixture{
		{name: "Receipt16Logs", method: "eth_getTransactionReceipt", build: projectionReceipt16Logs},
		{name: "Block100Tx", method: "eth_getBlockByHash", build: projectionBlock100Tx},
	}
}

// These are synthetic schema-conforming payloads for the projection boundary,
// not provider samples or proof of transaction/header validity. Fixture
// construction, schema checks and three-source aggregation stay outside timing.
func projectionReceipt16Logs(tb testing.TB) json.RawMessage {
	tb.Helper()
	hash := "0x" + strings.Repeat("11", 32)
	address := "0x" + strings.Repeat("22", 20)
	logs := make([]map[string]any, 16)
	for i := range logs {
		logs[i] = map[string]any{
			"address": address, "topics": []string{hash, hash, hash},
			"data": "0x" + strings.Repeat("ab", 128), "blockNumber": "0x7",
			"transactionHash": hash, "transactionIndex": "0x0", "blockHash": hash,
			"logIndex": fmt.Sprintf("0x%x", i), "removed": false, "blockTimestamp": "0x64",
		}
	}
	raw := marshalProjectionFixture(tb, map[string]any{
		"transactionHash": hash, "transactionIndex": "0x0", "blockHash": hash,
		"blockNumber": "0x7", "from": address, "to": address, "contractAddress": nil,
		"cumulativeGasUsed": "0x5208", "gasUsed": "0x5208", "effectiveGasPrice": "0x1",
		"logsBloom": "0x" + strings.Repeat("00", 256), "logs": logs, "status": "0x1", "type": "0x00",
	})
	if _, err := ethapi.NewGetTransactionReceiptResult(raw); err != nil {
		tb.Fatalf("receipt fixture schema: %v", err)
	}
	return raw
}

func projectionBlock100Tx(tb testing.TB) json.RawMessage {
	tb.Helper()
	hash := "0x" + strings.Repeat("11", 32)
	address := "0x" + strings.Repeat("22", 20)
	transactions := make([]map[string]any, 100)
	for i := range transactions {
		transactions[i] = map[string]any{
			"type": "0x0", "chainId": "0x1", "nonce": fmt.Sprintf("0x%x", i),
			"gas": "0x5208", "gasPrice": "0x1", "to": address, "value": "0x1",
			"input": "0x" + strings.Repeat("ab", 32), "v": "0x25", "r": "0x1", "s": "0x2",
			"blockHash": hash, "blockNumber": "0x7", "blockTimestamp": "0x64",
			"from": address, "hash": fmt.Sprintf("0x%064x", i+1), "transactionIndex": fmt.Sprintf("0x%x", i),
		}
	}
	raw := marshalProjectionFixture(tb, map[string]any{
		"number": "0x7", "hash": hash, "parentHash": hash, "nonce": "0x0000000000000000",
		"sha3Uncles": hash, "logsBloom": "0x" + strings.Repeat("00", 256), "transactionsRoot": hash,
		"stateRoot": hash, "receiptsRoot": hash, "miner": address, "difficulty": "0x0",
		"extraData": "0x", "size": "0x10000", "gasLimit": "0x1c9c380", "gasUsed": "0x200b20",
		"timestamp": "0x64", "mixHash": hash, "baseFeePerGas": "0x1",
		"transactions": transactions, "uncles": []string{},
	})
	if _, err := ethapi.NewBlock(raw); err != nil {
		tb.Fatalf("block fixture schema: %v", err)
	}
	return raw
}

func marshalProjectionFixture(tb testing.TB, value any) json.RawMessage {
	tb.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		tb.Fatal(err)
	}
	return raw
}

func largeProjectionEvaluation(tb testing.TB, core json.RawMessage, extensions int) rpc.Evaluation {
	tb.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(core, &object); err != nil {
		tb.Fatal(err)
	}
	for i := 0; i < extensions; i++ {
		object[fmt.Sprintf("vendor%d", i)] = json.RawMessage(`"0x1"`)
	}
	raw := marshalProjectionFixture(tb, object)
	observations := make([]rpc.Observation, 3)
	for i := range observations {
		observations[i] = rpc.Observation{
			Attempted: true, Source: rpc.SourceRef{ID: fmt.Sprintf("source-%d", i)},
			Response: rpc.Response{Exchange: rpc.Exchange{Progress: rpc.ExchangeCompleted}, Answer: rpc.Answer{Kind: rpc.AnswerResult, Result: raw}},
		}
	}
	evaluation, err := rpc.AggregateFields(observations, 2)
	if err != nil {
		tb.Fatal(err)
	}
	return evaluation
}

func TestStandardProjectorLargeDeliveryFixtures(t *testing.T) {
	for _, fixture := range largeProjectionFixtures() {
		t.Run(fixture.name, func(t *testing.T) {
			core := fixture.build(t)
			projector, err := NewSchemaProjector(fixture.method)
			if err != nil {
				t.Fatal(err)
			}
			for _, extensions := range []int{0, 2} {
				evaluation := largeProjectionEvaluation(t, core, extensions)
				set, err := projector.ProjectObservation(core, evaluation)
				if err != nil || len(set.Fields) != extensions {
					t.Fatalf("delivery = %#v, %v; want %d extensions", set, err, extensions)
				}
				if extensions == 0 && set.Fields != nil {
					t.Fatal("zero-extension delivery must keep nil fields")
				}
				for i, field := range set.Fields {
					if field.Path != fmt.Sprintf("/vendor%d", i) || len(field.Sources) != 3 {
						t.Fatalf("extension lost its path or three-source evidence: %#v", field)
					}
				}
			}
		})
	}
}
