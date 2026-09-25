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
// introspection. This checkpoint renders the eth_call chain only, so the
// column shape can be confirmed before the other registered methods are
// added. The follow-up must walk every RegisteredMethod. It must not allow
// an undeclared layer to pass, and it must not skip a registered method.
//
// ETHGUARD_UPDATE_METHOD_RULES=1 rewrites the file. The default does not.
func TestMethodRulesMatchIndex(t *testing.T) {
	rows, err := methodRuleRows()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Method != MethodCall {
		t.Fatalf("column-shape checkpoint must render eth_call only, got %+v", rows)
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
	name, policy, err := catalogCallPolicy()
	if err != nil {
		return nil, err
	}
	rule, err := inspectMethodRule(name, policy)
	if err != nil {
		return nil, err
	}
	want := rpc.MethodRule{
		Anchor:      "hash@1",
		BlockRef:    "eip1898",
		Pending:     "local-observation",
		Resolver:    "call-revert",
		CompareKind: "method-specific",
	}
	if rule != want {
		return nil, fmt.Errorf("%s: column shape = %+v, want %+v", name, rule, want)
	}
	return []methodRuleRow{{Method: name, Rule: rule}}, nil
}

// catalogCallPolicy reads eth_call from Catalog rather than a free-floating
// name. Non-registered catalog entries are outside the index. Other
// registered methods are intentionally absent until the column shape is
// confirmed; they are not an allow-undeclared exception inside a full walk.
func catalogCallPolicy() (string, rpc.Policy, error) {
	for _, method := range Catalog() {
		if method.Kind != RegisteredMethod || method.Name != MethodCall {
			continue
		}
		return method.Name, CallPolicy(nil), nil
	}
	return "", nil, fmt.Errorf("catalog is missing registered method %s", MethodCall)
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
