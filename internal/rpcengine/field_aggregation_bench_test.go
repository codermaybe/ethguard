package rpcengine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

type aggregateBenchmarkCase struct {
	name         string
	observations []rpc.Observation
}

func BenchmarkAggregateFields(b *testing.B) {
	// Fixture construction, formatting, and source setup are outside each timer.
	for _, test := range aggregateBenchmarkCases() {
		b.Run(test.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := AggregateFields(test.observations, 2); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func aggregateBenchmarkCases() []aggregateBenchmarkCase {
	// These are Answer.Result values, without a JSON-RPC envelope. The small
	// receipt and log array model actual method payload shapes; the benchmark
	// measures only generic field aggregation, not method admission or transport.
	receipt := fmt.Sprintf(`{"transactionHash":"0x%s","transactionIndex":"0x1","blockHash":"0x%s","blockNumber":"0x10","from":"0x%s","to":"0x%s","cumulativeGasUsed":"0x5208","gasUsed":"0x5208","effectiveGasPrice":"0x1","contractAddress":null,"status":"0x1","type":"0x0","logsBloom":"0x%s","logs":[]}`,
		strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("1", 40), strings.Repeat("2", 40), strings.Repeat("0", 512))
	logs := fmt.Sprintf(`[{"address":"0x%s","topics":["0x%s"],"data":"0x01","blockNumber":"0x10","transactionHash":"0x%s","transactionIndex":"0x1","blockHash":"0x%s","logIndex":"0x0","removed":false},{"address":"0x%s","topics":[],"data":"0x02","blockNumber":"0x10","transactionHash":"0x%s","transactionIndex":"0x1","blockHash":"0x%s","logIndex":"0x1","removed":false}]`,
		strings.Repeat("1", 40), strings.Repeat("a", 64), strings.Repeat("c", 64), strings.Repeat("b", 64),
		strings.Repeat("2", 40), strings.Repeat("c", 64), strings.Repeat("b", 64))
	payloads := []struct {
		name     string
		majority string
		dissent  string
		third    string
	}{
		{
			name:     "scalar",
			majority: `"0x1234567890abcdef"`,
			dissent:  `"0xdeadbeef"`,
			third:    `"0xcafef00d"`,
		},
		{
			name:     "receipt",
			majority: receipt,
			dissent:  strings.Replace(receipt, `"gasUsed":"0x5208"`, `"gasUsed":"0x5209"`, 1),
			third:    strings.Replace(receipt, `"gasUsed":"0x5208"`, `"gasUsed":"0x520a"`, 1),
		},
		{
			name:     "logs-array",
			majority: logs,
			dissent:  strings.Replace(logs, `"data":"0x01"`, `"data":"0xff"`, 1),
			third:    strings.Replace(logs, `"data":"0x01"`, `"data":"0xfe"`, 1),
		},
	}

	var cases []aggregateBenchmarkCase
	for _, payload := range payloads {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, []byte(payload.majority), "", "  "); err != nil {
			panic(err)
		}
		for _, scenario := range []struct {
			name string
			raw  [3]string
		}{
			{name: "3-identical", raw: [3]string{payload.majority, payload.majority, payload.majority}},
			{name: "3-equivalent", raw: [3]string{payload.majority, "\n" + pretty.String(), "\t " + payload.majority + "\r\n"}},
			{name: "2-vs-1-aab", raw: [3]string{payload.majority, payload.majority, payload.dissent}},
			{name: "2-vs-1-aba", raw: [3]string{payload.majority, payload.dissent, payload.majority}},
			{name: "2-vs-1-baa", raw: [3]string{payload.dissent, payload.majority, payload.majority}},
			{name: "3-all-different", raw: [3]string{payload.majority, payload.dissent, payload.third}},
		} {
			cases = append(cases, aggregateBenchmarkCase{
				name:         payload.name + "/" + scenario.name,
				observations: aggregateBenchmarkObservations(scenario.raw[0], scenario.raw[1], scenario.raw[2]),
			})
		}
	}
	return cases
}

func aggregateBenchmarkObservations(first, second, third string) []rpc.Observation {
	return []rpc.Observation{
		aggregateObservation("benchmark-a", first),
		aggregateObservation("benchmark-b", second),
		aggregateObservation("benchmark-c", third),
	}
}
