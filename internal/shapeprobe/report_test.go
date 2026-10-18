package shapeprobe

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/codermaybe/ethguard/internal/rpcsample"
	"github.com/codermaybe/ethguard/rpc/methods"
)

func TestReportDiffNamesRecordSHA256(t *testing.T) {
	meta := Meta{
		Schema:             MetaSchema,
		BatchID:            "2026-09-26",
		CapturedAt:         time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC),
		Network:            rpcsample.Network{ChainID: 1, Fork: "unspecified"},
		BlockHeight:        "0x10",
		BlockHeightDecimal: 16,
		AnchorRule:         "test",
		LogsWindow:         LogsWindowAt(16),
		Web3ClientVersion:  map[string]*string{"s1": nil, "s2": nil, "s3": nil},
		Note:               ReproductionNote,
	}
	index, records := feeObservations(t)
	text, err := RenderReport(meta, index, records)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "比较键: 不一致") {
		t.Fatalf("missing disagreement:\n%s", text)
	}
	if !strings.Contains(text, "sha256=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") ||
		!strings.Contains(text, "sha256=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb") {
		t.Fatalf("diff is not tied to record sha256:\n%s", text)
	}
	if strings.Contains(text, "应归一") || strings.Contains(text, "应判冲突") {
		t.Fatalf("report drew a conclusion:\n%s", text)
	}
}

func TestReportNamesNormalizeRejection(t *testing.T) {
	meta := Meta{
		BatchID:            "2026-09-26",
		Network:            rpcsample.Network{ChainID: 1, Fork: "unspecified"},
		BlockHeight:        "0x10",
		BlockHeightDecimal: 16,
		LogsWindow:         LogsWindowAt(16),
		Note:               ReproductionNote,
	}
	body := func(result string) string {
		return `{"jsonrpc":"2.0","id":1,"result":` + result + `}`
	}
	sums := []string{
		strings.Repeat("a", 64),
		strings.Repeat("b", 64),
		strings.Repeat("c", 64),
	}
	var index rpcsample.Index
	var records []rpcsample.Record
	add := func(method, source, sum, result string) {
		index.Records = append(index.Records, rpcsample.IndexEntry{Method: method, SourceID: source, SHA256: sum})
		records = append(records, rpcsample.Record{
			Method: method, RequestID: method, Source: rpcsampleSource(source).Source,
			ObservedAt: time.Unix(0, 0).UTC(),
			Response:   rpcsample.WireResponse{Body: body(result), HTTPStatus: 200},
		})
	}
	for _, method := range ProbeMethods() {
		result := `"0x1"`
		switch method {
		case methods.MethodNetVersion:
			result = `"1"`
		case methods.MethodSyncing, methods.MethodListening:
			result = `false`
		case methods.MethodFeeHistory:
			result = `{"oldestBlock":"0x1","baseFeePerGas":["0x1","0x1"],"gasUsedRatio":[0.1,0.2],"reward":[["0x1"],["0x1"]]}`
		case methods.MethodGetLogs:
			result = `[]`
		}
		for i, source := range []string{"s1", "s2", "s3"} {
			value := result
			if method == methods.MethodChainID && source == "s3" {
				value = `{"not":"a quantity"}`
			}
			add(method, source, sums[i], value)
		}
	}
	text, err := RenderReport(meta, index, records)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "现有归一化未接受:") || !strings.Contains(text, "sha256="+sums[2]) {
		t.Fatalf("rejection missing sha256:\n%s", text)
	}
}

func feeObservations(t *testing.T) (rpcsample.Index, []rpcsample.Record) {
	t.Helper()
	sums := map[string]string{
		"s1": strings.Repeat("a", 64),
		"s2": strings.Repeat("b", 64),
		"s3": strings.Repeat("c", 64),
	}
	reward := map[string]string{
		"s1": `[["0x1"],["0x1"]]`,
		"s2": `[["0x2"],["0x1"]]`,
		"s3": `[["0x1"],["0x1"]]`,
	}
	var index rpcsample.Index
	var records []rpcsample.Record
	for _, method := range ProbeMethods() {
		for _, source := range []string{"s1", "s2", "s3"} {
			result := `"0x1"`
			switch method {
			case methods.MethodNetVersion:
				result = `"1"`
			case methods.MethodSyncing, methods.MethodListening:
				result = `false`
			case methods.MethodFeeHistory:
				result = `{"oldestBlock":"0x10","baseFeePerGas":["0x1","0x1","0x1"],"gasUsedRatio":[0.1,0.2],"reward":` + reward[source] + `}`
			case methods.MethodGetLogs:
				result = `[]`
			}
			index.Records = append(index.Records, rpcsample.IndexEntry{Method: method, SourceID: source, SHA256: sums[source]})
			records = append(records, rpcsample.Record{
				Method: method, RequestID: method, Source: rpcsampleSource(source).Source,
				ObservedAt: time.Unix(0, 0).UTC(),
				Response:   rpcsample.WireResponse{HTTPStatus: 200, Body: `{"jsonrpc":"2.0","id":1,"result":` + result + `}`},
			})
		}
	}
	return index, records
}

func rpcsampleSource(id string) rpcsample.Record {
	var record rpcsample.Record
	raw := []byte(`{"source":{"id":"` + id + `","endpoint":"https://example.test"}}`)
	if err := json.Unmarshal(raw, &record); err != nil {
		panic(err)
	}
	return record
}
