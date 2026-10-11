package shapeprobe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"ethguard/internal/rpcsample"
	"ethguard/rpc"
	"ethguard/rpc/methods"
)

// ReproductionNote is stored in the batch metadata. The anchor makes the
// block-parameter requests repeatable. It does not freeze later client or
// protocol revisions, and a mismatch at the same height is only a difference
// pending review.
const ReproductionNote = "The anchor height makes block-parameter requests repeatable: a rerun at the same height sends the same eth_feeHistory and eth_getLogs bodies. It does not promise the same response shape after a protocol or client change. A reproduction mismatch is a difference pending review, not an invalidation of this sample. web3_clientVersion is context only and is not a client attribution."

// Meta is the probe batch metadata. It is not an expect.json and not a golden.
type Meta struct {
	Schema             string             `json:"schema"`
	BatchID            string             `json:"batch_id"`
	CapturedAt         time.Time          `json:"captured_at"`
	Network            rpcsample.Network  `json:"network"`
	BlockHeight        string             `json:"block_height"`
	BlockHeightDecimal uint64             `json:"block_height_decimal"`
	AnchorRule         string             `json:"anchor_rule"`
	LogsWindow         LogsWindow         `json:"logs_window"`
	PreflightHeight    map[string]string  `json:"preflight_block_height"`
	Web3ClientVersion  map[string]*string `json:"web3_client_version"`
	Note               string             `json:"note"`
}

// ProbeMethods is the fixed report order. web3_clientVersion is not included.
func ProbeMethods() []string {
	return []string{
		methods.MethodChainID,
		methods.MethodNetVersion,
		methods.MethodGasPrice,
		methods.MethodMaxPriorityFeePerGas,
		methods.MethodBlobBaseFee,
		methods.MethodBlockNumber,
		methods.MethodSyncing,
		methods.MethodPeerCount,
		methods.MethodListening,
		methods.MethodFeeHistory,
		methods.MethodGetLogs,
	}
}

type observation struct {
	sourceID   string
	sha256     string
	observedAt time.Time
	endpoint   string
	request    string
	errKinds   []string
	result     json.RawMessage
	key        string
	normErr    string
	rejected   bool
	noResult   bool
}

// RenderReport writes one section per probe method. Differences name the
// record sha256 that carries the raw body. The text does not decide whether
// a difference should be normalized or treated as a conflict.
func RenderReport(meta Meta, index rpcsample.Index, records []rpcsample.Record) (string, error) {
	if len(index.Records) != len(records) {
		return "", fmt.Errorf("shapeprobe: index has %d records, loaded %d", len(index.Records), len(records))
	}
	byMethod := map[string][]observation{}
	for i, record := range records {
		entry := index.Records[i]
		if entry.Method != record.Method || entry.SourceID != record.Source.ID || entry.SHA256 == "" {
			return "", fmt.Errorf("shapeprobe: index entry %d does not match record %s/%s", i, record.Method, record.Source.ID)
		}
		obs, err := observe(record, entry.SHA256)
		if err != nil {
			return "", err
		}
		byMethod[record.Method] = append(byMethod[record.Method], obs)
	}
	var b strings.Builder
	b.WriteString("# 读方法响应形状探查\n\n")
	b.WriteString("本报告只陈述观察。它不判断这些差异应当如何处理。\n\n")
	fmt.Fprintf(&b, "- 批次: %s\n", meta.BatchID)
	fmt.Fprintf(&b, "- 锚定高度: %s (%d)\n", meta.BlockHeight, meta.BlockHeightDecimal)
	fmt.Fprintf(&b, "- eth_getLogs 窗口: fromBlock=%s toBlock=%s\n", meta.LogsWindow.FromBlock, meta.LogsWindow.ToBlock)
	fmt.Fprintf(&b, "- 锚的取法: %s\n", meta.AnchorRule)
	fmt.Fprintf(&b, "- 复现边界: %s\n\n", meta.Note)
	for _, method := range ProbeMethods() {
		rows := byMethod[method]
		if len(rows) < 3 {
			return "", fmt.Errorf("shapeprobe: %s has %d endpoint results, want at least 3", method, len(rows))
		}
		if err := writeMethod(&b, meta, method, rows); err != nil {
			return "", err
		}
	}
	b.WriteString("## web3_clientVersion\n\n")
	b.WriteString("只作上下文，不作客户端归因。采集失败记为 null。\n\n")
	ids := make([]string, 0, len(meta.Web3ClientVersion))
	for id := range meta.Web3ClientVersion {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		b.WriteString("- 没有记录到端点。\n")
	}
	for _, id := range ids {
		version := meta.Web3ClientVersion[id]
		if version == nil {
			fmt.Fprintf(&b, "- %s: null\n", id)
			continue
		}
		fmt.Fprintf(&b, "- %s: %s\n", id, *version)
	}
	b.WriteString("\n")
	return b.String(), nil
}

func writeMethod(b *strings.Builder, meta Meta, method string, rows []observation) error {
	fmt.Fprintf(b, "## %s\n\n", method)
	fmt.Fprintf(b, "- 端点数: %d\n", len(rows))
	if BlockParamRequest(method) {
		fmt.Fprintf(b, "- 锚定高度: %s（请求参数钉在该高度，不是 latest）\n", meta.BlockHeight)
	} else {
		fmt.Fprintf(b, "- 锚定高度: %s（上下文，不是该方法的参数）\n", meta.BlockHeight)
	}
	var accepted []observation
	var rejected []observation
	for _, row := range rows {
		if row.rejected || row.noResult {
			rejected = append(rejected, row)
			continue
		}
		accepted = append(accepted, row)
	}
	keysAgree := len(accepted) >= 2
	firstKey := ""
	for i, row := range accepted {
		if i == 0 {
			firstKey = row.key
			continue
		}
		if row.key != firstKey {
			keysAgree = false
		}
	}
	if len(accepted) < 2 {
		b.WriteString("- 比较键: 不足两条可归一响应，没有比较\n")
	} else if keysAgree {
		b.WriteString("- 比较键: 一致\n")
	} else {
		b.WriteString("- 比较键: 不一致\n")
	}
	var missing, refused []observation
	for _, row := range rejected {
		if row.noResult && row.normErr == "" || row.noResult && row.normErr == "响应没有 result" {
			missing = append(missing, row)
			continue
		}
		if row.normErr != "" && row.result == nil {
			missing = append(missing, row)
			continue
		}
		refused = append(refused, row)
	}
	writeAbsent := func(title string, rows []observation) {
		if len(rows) == 0 {
			fmt.Fprintf(b, "- %s: 无\n", title)
			return
		}
		fmt.Fprintf(b, "- %s:\n", title)
		for _, row := range rows {
			reason := row.normErr
			if reason == "" {
				reason = "没有 JSON-RPC result"
			}
			if len(row.errKinds) > 0 {
				reason += "（" + strings.Join(row.errKinds, ",") + "）"
			}
			fmt.Fprintf(b, "  - %s sha256=%s %s\n", row.sourceID, row.sha256, reason)
		}
	}
	writeAbsent("没有 JSON-RPC result", missing)
	writeAbsent("现有归一化未接受", refused)
	b.WriteString("- 端点观察:\n")
	for _, row := range rows {
		keyText := "`" + preview(row.key, 80) + "`"
		if row.rejected || row.noResult {
			keyText = "无"
		}
		fmt.Fprintf(b, "  - %s sha256=%s endpoint=%s observed_at=%s 比较键=%s\n",
			row.sourceID, row.sha256, row.endpoint, row.observedAt.UTC().Format(time.RFC3339Nano), keyText)
	}
	allSamples := make([]FieldSample, len(rows))
	for i, row := range rows {
		result := row.result
		if row.rejected || row.noResult {
			result = nil
		}
		allSamples[i] = FieldSample{SourceID: row.sourceID, SHA256: row.sha256, Result: result}
	}
	if err := writeClassification(b, method, allSamples); err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	b.WriteString("- 字段差异:\n")
	if len(accepted) < 2 || keysAgree {
		b.WriteString("  - 无\n\n")
		return nil
	}
	samples := make([]FieldSample, len(accepted))
	for i, row := range accepted {
		samples[i] = FieldSample{SourceID: row.sourceID, SHA256: row.sha256, Result: row.result}
	}
	diffs, err := DiffFields(samples)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	if len(diffs) == 0 {
		b.WriteString("  - 比较键不一致，但按 JSON 叶路径展开后没有可列出的值差异\n\n")
		return nil
	}
	for _, diff := range diffs {
		fmt.Fprintf(b, "  - `%s`:", diff.Path)
		for _, value := range diff.Values {
			if value.Missing {
				fmt.Fprintf(b, " %s sha256=%s 缺失", value.SourceID, value.SHA256)
				continue
			}
			fmt.Fprintf(b, " %s sha256=%s %s", value.SourceID, value.SHA256, preview(value.Raw, 160))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	return nil
}

func writeClassification(b *strings.Builder, method string, samples []FieldSample) error {
	classification, err := ClassifyFields(method, samples)
	if err != nil {
		return err
	}
	fmt.Fprintf(b, "- 字段分类（按原始 JSON 叶值、固定调用门槛）: Core=%d Extension=%d residual=%d conflict=%d\n",
		len(classification.Core), len(classification.Extensions),
		len(classification.Diagnostics.Residuals), len(classification.Diagnostics.Conflicts))
	writeFields := func(label string, fields []rpc.FieldObservation) {
		if len(fields) == 0 {
			return
		}
		fmt.Fprintf(b, "  - %s:\n", label)
		for _, field := range fields {
			fmt.Fprintf(b, "    - `%s` sources=%s value=%s\n",
				reportPath(field.Path), sourceIDs(field.Sources), preview(string(field.Value), 160))
		}
	}
	writeFields("Core", classification.Core)
	writeFields("Extension", classification.Extensions)
	writeFields("residual", classification.Diagnostics.Residuals)
	if len(classification.Diagnostics.Conflicts) > 0 {
		b.WriteString("  - conflict:\n")
		for _, conflict := range classification.Diagnostics.Conflicts {
			fmt.Fprintf(b, "    - `%s`:\n", reportPath(conflict.Path))
			for _, field := range conflict.Values {
				fmt.Fprintf(b, "      - sources=%s value=%s\n",
					sourceIDs(field.Sources), preview(string(field.Value), 160))
			}
		}
	}
	return nil
}

func reportPath(path string) string {
	if path == "" {
		return "/"
	}
	return path
}

func sourceIDs(sources []rpc.SourceRef) string {
	ids := make([]string, len(sources))
	for i, source := range sources {
		ids[i] = source.ID
	}
	return strings.Join(ids, ",")
}

func observe(record rpcsample.Record, sum string) (observation, error) {
	obs := observation{
		sourceID:   record.Source.ID,
		sha256:     sum,
		observedAt: record.ObservedAt,
		endpoint:   record.Source.Endpoint,
		request:    record.Request,
		noResult:   true,
	}
	for _, item := range record.Errors {
		obs.errKinds = append(obs.errKinds, item.Kind)
	}
	if record.Method == ClientVersionMethod {
		return obs, nil
	}
	if len(record.Errors) > 0 && record.Response.Body == "" {
		obs.rejected = true
		obs.normErr = "没有响应体"
		return obs, nil
	}
	result, ok, err := resultOf(record.Response.Body)
	if err != nil {
		obs.rejected = true
		obs.normErr = err.Error()
		return obs, nil
	}
	if !ok {
		obs.noResult = true
		obs.rejected = true
		if obs.normErr == "" {
			obs.normErr = "响应没有 result"
		}
		return obs, nil
	}
	obs.noResult = false
	obs.result = result
	key, err := NormalizeResult(record.Method, result)
	if err != nil {
		obs.rejected = true
		obs.normErr = err.Error()
		return obs, nil
	}
	obs.key = key
	return obs, nil
}

func resultOf(body string) (json.RawMessage, bool, error) {
	if strings.TrimSpace(body) == "" {
		return nil, false, nil
	}
	var fields map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader([]byte(body)))
	dec.UseNumber()
	if err := dec.Decode(&fields); err != nil {
		return nil, false, fmt.Errorf("响应不是 JSON 对象: %w", err)
	}
	if errBody, ok := fields["error"]; ok && len(bytes.TrimSpace(errBody)) > 0 && string(bytes.TrimSpace(errBody)) != "null" {
		return nil, false, nil
	}
	result, ok := fields["result"]
	if !ok || len(bytes.TrimSpace(result)) == 0 {
		return nil, false, nil
	}
	return result, true, nil
}

func preview(value string, limit int) string {
	if value == "" {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	return value[:limit] + fmt.Sprintf("…(len %d)", len(value))
}
