package methods

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ethguard/rpc"
)

// TestMethodRulesMatchIndex locks docs/design/method-rules.md to runtime
// introspection of every RegisteredMethod. A layer that does not implement
// MethodRuleSource fails the test with the method name and the concrete
// type. There is no allow-undeclared exception.
//
// ETHGUARD_UPDATE_METHOD_RULES=1 rewrites the file. The default does not.
func TestMethodRulesMatchIndex(t *testing.T) {
	rows, err := methodRuleRows()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(RegisteredNames()) {
		t.Fatalf("rule rows = %d, registered methods = %d", len(rows), len(RegisteredNames()))
	}
	got := renderMethodRules(rows)
	path := filepath.Join("..", "..", "docs", "design", "method-rules.md")
	if os.Getenv("ETHGUARD_UPDATE_METHOD_RULES") == "1" {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read method rules: %v\n%s", err, got)
	}
	if string(want) != got {
		t.Fatalf("method rules index drifted:\n%s", methodRuleDiff(string(want), got))
	}
}

func methodRuleRows() ([]methodRuleRow, error) {
	var rows []methodRuleRow
	for _, method := range Catalog() {
		if method.Kind != RegisteredMethod {
			continue
		}
		policy, err := policyForRuleIndex(method.Name)
		if err != nil {
			return nil, err
		}
		rule, err := inspectMethodRule(method.Name, policy)
		if err != nil {
			return nil, err
		}
		rows = append(rows, methodRuleRow{Method: method.Name, Rule: rule})
	}
	if err := assertPendingSemantics(rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// assertPendingSemantics locks the three pending mechanisms to methods whose
// constructions differ. The values have to come from those layers; a single
// fixed label cannot satisfy all three.
func assertPendingSemantics(rows []methodRuleRow) error {
	got := map[string]string{}
	for _, row := range rows {
		got[row.Method] = ruleOrNone(row.Rule.Pending)
		if row.Method != MethodCall {
			continue
		}
		want := rpc.MethodRule{
			Anchor:      "hash@1",
			BlockRef:    rpc.RuleBlockRefEIP1898,
			Pending:     rpc.RulePendingLocalObservation,
			Resolver:    rpc.RuleResolverCallRevert,
			CompareKind: rpc.RuleCompareMethodSpecific,
		}
		if row.Rule != want {
			return fmt.Errorf("%s: column shape = %+v, want %+v", row.Method, row.Rule, want)
		}
	}
	checks := []struct {
		method  string
		pending string
	}{
		{MethodGetLogs, rpc.RulePendingLocalReject},
		{MethodCall, rpc.RulePendingLocalObservation},
		{MethodChainID, "none"},
		{MethodEstimateGas, rpc.RulePendingLocalObservation},
	}
	for _, check := range checks {
		pending, ok := got[check.method]
		if !ok {
			return fmt.Errorf("%s: missing from the rule index", check.method)
		}
		if pending != check.pending {
			return fmt.Errorf("%s: pending = %s, want %s", check.method, pending, check.pending)
		}
	}
	return nil
}

// policyForRuleIndex constructs the registered policy. A name without a case
// fails the walk; it is not skipped.
func policyForRuleIndex(name string) (rpc.Policy, error) {
	var anchor rpc.AnchorFunc
	var height rpc.AnchorHeightFunc
	switch name {
	case MethodChainID:
		return ChainIDPolicy(), nil
	case MethodGetBlockByHash:
		return BlockByHashPolicy(), nil
	case MethodGetBlockByNumber:
		return BlockByNumberPolicy(), nil
	case MethodGetBalance:
		return GetBalancePolicy(anchor), nil
	case MethodGetTransactionReceipt:
		return TransactionReceiptPolicy(), nil
	case MethodGetTransactionByHash:
		return TransactionByHashPolicy(), nil
	case MethodGetBlockReceipts:
		return BlockReceiptsPolicy(anchor), nil
	case MethodGetTransactionCount:
		return GetTransactionCountPolicy(anchor), nil
	case MethodGetStorageAt:
		return GetStorageAtPolicy(anchor), nil
	case MethodGetCode:
		return GetCodePolicy(anchor), nil
	case MethodCall:
		return CallPolicy(anchor), nil
	case MethodEstimateGas:
		return EstimateGasPolicy(anchor), nil
	case MethodGetLogs:
		return LogsPolicy(height), nil
	case MethodFeeHistory:
		return FeeHistoryPolicy(height), nil
	case MethodGasPrice:
		return GasPricePolicy(), nil
	case MethodMaxPriorityFeePerGas:
		return MaxPriorityFeePerGasPolicy(), nil
	case MethodBlobBaseFee:
		return BlobBaseFeePolicy(), nil
	case MethodBlockNumber:
		return BlockNumberPolicy(), nil
	case MethodCreateAccessList:
		return CreateAccessListPolicy(anchor), nil
	case MethodPeerCount:
		return PeerCountPolicy(), nil
	case MethodSyncing:
		return SyncingPolicy(), nil
	case MethodNetVersion:
		return NetVersionPolicy(), nil
	case MethodListening:
		return ListeningPolicy(), nil
	default:
		return nil, fmt.Errorf("%s: no policy constructor for rule introspection", name)
	}
}

// inspectMethodRule visits each concrete decorator. The outer MethodRule
// already includes the inner composition; the loop still type-asserts every
// layer so a missing implementation names that layer.
func inspectMethodRule(method string, policy rpc.Policy) (rpc.MethodRule, error) {
	if policy == nil {
		return rpc.MethodRule{}, fmt.Errorf("%s: nil policy", method)
	}
	var composed rpc.MethodRule
	for depth := 0; policy != nil; depth++ {
		if depth > 16 {
			return rpc.MethodRule{}, fmt.Errorf("%s: rule chain did not terminate at %T", method, policy)
		}
		source, ok := policy.(rpc.MethodRuleSource)
		if !ok {
			return rpc.MethodRule{}, fmt.Errorf("%s: %T does not implement rule introspection", method, policy)
		}
		layer, inner := source.MethodRule()
		if depth == 0 {
			composed = layer
		}
		policy = inner
	}
	if composed.Resolver == "" || composed.CompareKind == "" {
		return rpc.MethodRule{}, fmt.Errorf("%s: resolver or comparison-key class is empty", method)
	}
	return composed, nil
}

type methodRuleRow struct {
	Method string
	Rule   rpc.MethodRule
}

func renderMethodRules(rows []methodRuleRow) string {
	var b strings.Builder
	b.WriteString("由 TestMethodRulesMatchIndex 生成，勿手动编辑。\n")
	b.WriteString("\n")
	b.WriteString("| 方法 | 锚定 | 区块引用形态 | pending 处理 | resolver | 比较键种类 |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	for _, row := range rows {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n",
			row.Method,
			ruleOrNone(row.Rule.Anchor),
			ruleOrNone(row.Rule.BlockRef),
			ruleOrNone(row.Rule.Pending),
			row.Rule.Resolver,
			row.Rule.CompareKind,
		)
	}
	return b.String()
}

func ruleOrNone(value string) string {
	if value == "" {
		return "none"
	}
	return value
}

func methodRuleDiff(before, after string) string {
	a := strings.Split(strings.TrimSuffix(before, "\n"), "\n")
	b := strings.Split(strings.TrimSuffix(after, "\n"), "\n")
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
				continue
			}
			if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
				continue
			}
			dp[i][j] = dp[i][j+1]
		}
	}
	var buf strings.Builder
	buf.WriteString("--- docs/design/method-rules.md\n+++ introspected\n")
	i, j := 0, 0
	for i < n && j < m {
		if a[i] == b[j] {
			i++
			j++
			continue
		}
		if dp[i+1][j] >= dp[i][j+1] {
			buf.WriteString("- " + a[i] + "\n")
			i++
			continue
		}
		buf.WriteString("+ " + b[j] + "\n")
		j++
	}
	for ; i < n; i++ {
		buf.WriteString("- " + a[i] + "\n")
	}
	for ; j < m; j++ {
		buf.WriteString("+ " + b[j] + "\n")
	}
	return buf.String()
}
