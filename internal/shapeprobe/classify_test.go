package shapeprobe

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

func TestClassifyFieldsSeparatesCoreAcceptedExtensionAndResidual(t *testing.T) {
	samples := []FieldSample{
		{SourceID: "s1", Result: json.RawMessage(`{"oldestBlock":"0x10","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[0.1,0.2],"vendor":{"mode":"fast"}}`)},
		{SourceID: "s2", Result: json.RawMessage(`{"oldestBlock":"0x10","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[0.1,0.2],"vendor":{"mode":"fast"}}`)},
		{SourceID: "s3", Result: json.RawMessage(`{"oldestBlock":"0x10","baseFeePerGas":["0x1","0x2"],"gasUsedRatio":[0.1,0.2],"vendor":{"mode":"safe"}}`)},
	}
	classification, err := ClassifyFields("eth_feeHistory", samples)
	if err != nil {
		t.Fatal(err)
	}
	if !hasField(classification.Core, "/oldestBlock") || !hasField(classification.Core, "/baseFeePerGas/0") {
		t.Fatalf("standard fields were not classified as Core: %#v", classification.Core)
	}
	if !hasField(classification.Extensions, "/vendor/mode") {
		t.Fatalf("accepted shared extension missing: %#v", classification.Extensions)
	}
	if !hasField(classification.Diagnostics.Residuals, "/vendor/mode") {
		t.Fatalf("unsupported extension value missing from diagnostics: %#v", classification.Diagnostics)
	}
}

func TestClassifyFieldsRetainsDivergentExtensionValuesAsResidual(t *testing.T) {
	samples := []FieldSample{
		{SourceID: "s1", Result: json.RawMessage(`{"oldestBlock":"0x10","baseFeePerGas":["0x1"],"gasUsedRatio":[0.1],"vendor":"a"}`)},
		{SourceID: "s2", Result: json.RawMessage(`{"oldestBlock":"0x10","baseFeePerGas":["0x1"],"gasUsedRatio":[0.1],"vendor":"b"}`)},
		{SourceID: "s3", Result: json.RawMessage(`{"oldestBlock":"0x10","baseFeePerGas":["0x1"],"gasUsedRatio":[0.1],"vendor":"c"}`)},
	}
	classification, err := ClassifyFields("eth_feeHistory", samples)
	if err != nil {
		t.Fatal(err)
	}
	if hasField(classification.Extensions, "/vendor") {
		t.Fatalf("divergent extension was accepted: %#v", classification.Extensions)
	}
	if len(classification.Diagnostics.Residuals) != 3 {
		t.Fatalf("unexpected residuals: %#v", classification.Diagnostics.Residuals)
	}
}

func TestClassifyFieldsUsesScalarRootAsCore(t *testing.T) {
	classification, err := ClassifyFields("eth_chainId", []FieldSample{
		{SourceID: "s1", Result: json.RawMessage(`"0x1"`)},
		{SourceID: "s2", Result: json.RawMessage(`"0x1"`)},
		{SourceID: "s3", Result: json.RawMessage(`"0x1"`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasField(classification.Core, "") {
		t.Fatalf("scalar root was not classified as Core: %#v", classification.Core)
	}
}

func TestClassifyFieldsKeepsUnavailableSourcesInVotingBasis(t *testing.T) {
	classification, err := ClassifyFields("eth_chainId", []FieldSample{
		{SourceID: "s1", Result: json.RawMessage(`"0x1"`)},
		{SourceID: "s2", Result: json.RawMessage(`"0x1"`)},
		{SourceID: "s3"},
		{SourceID: "s4"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if hasField(classification.Core, "") {
		t.Fatalf("two of four sources should not pass the majority basis: %#v", classification.Core)
	}
	if len(classification.Diagnostics.Residuals) != 1 {
		t.Fatalf("expected one residual scalar value: %#v", classification.Diagnostics.Residuals)
	}
}

func hasField(fields []rpc.FieldObservation, path string) bool {
	for _, field := range fields {
		if field.Path == path {
			return true
		}
	}
	return false
}

func TestReportIncludesFieldClassification(t *testing.T) {
	meta := Meta{BatchID: "test", BlockHeight: "0x10", BlockHeightDecimal: 16, LogsWindow: LogsWindowAt(16), Note: ReproductionNote}
	index, records := feeObservations(t)
	text, err := RenderReport(meta, index, records)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "字段分类（按原始 JSON 叶值、固定调用门槛）") {
		t.Fatalf("classification section missing:\n%s", text)
	}
}
