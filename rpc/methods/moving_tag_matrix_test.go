package methods

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// matrixColumns is the contract header of docs/design/moving-tag-matrix.md.
// The interlock below parses data rows positionally, so a column added to
// the document without updating this list fails loudly instead of shifting
// every support cell silently.
var matrixColumns = []string{
	"Method", "Reference position", "earliest", "number", "hash",
	"latest", "safe", "finalized", "pending", "requireCanonical",
	"EthGuard action", "Wire form", "Result status", "Evidence meaning",
	"Tests", "Live evidence",
}

// forbiddenClaims bans positive-claim phrases that would present
// provider-backed agreement as Ethereum finality, canonical proof, or
// light-client verification. "Not X" disclaimers do not contain these
// phrases and stay allowed.
var forbiddenClaims = []string{
	"finality proof",
	"canonical proof",
	"light-client proof",
	"proves finality",
	"consensus proof",
	"最终性证明",
	"典型性证明",
	"共识证明",
}

func loadMatrixRows(t *testing.T) [][]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "design", "moving-tag-matrix.md"))
	if err != nil {
		t.Fatalf("read moving-tag matrix: %v", err)
	}
	doc := string(raw)
	for _, claim := range forbiddenClaims {
		if strings.Contains(doc, claim) {
			t.Fatalf("matrix contains forbidden claim %q: provider agreement must never be written as proof", claim)
		}
	}
	if !strings.Contains(doc, "provider-backed cross-source evidence") {
		t.Fatal("matrix lost its provider-backed corroboration disclaimer")
	}
	var rows [][]string
	for _, line := range strings.Split(doc, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "| eth") && !strings.HasPrefix(line, "| net") {
			continue
		}
		cells := strings.Split(line, "|")
		if len(cells) != len(matrixColumns)+2 { // leading + trailing empty
			t.Fatalf("matrix row has %d cells, want %d: %s", len(cells)-2, len(matrixColumns), line)
		}
		row := make([]string, 0, len(matrixColumns))
		for _, cell := range cells[1 : len(cells)-1] {
			row = append(row, strings.TrimSpace(cell))
		}
		rows = append(rows, row)
	}
	return rows
}

// TestCatalogMatrixInterlock keeps the method registry and the moving-tag
// matrix in lockstep in both directions: a RegisteredMethod without a row
// fails, a row naming an unknown method fails, and a row with a blank
// support cell fails so an undecided contract cannot hide as an empty
// column. Support detail (pending/hash/requireCanonical coverage) is
// enforced by making blanks illegal: every cell must state the contract
// explicitly, including "n/a" where a shape does not apply.
func TestCatalogMatrixInterlock(t *testing.T) {
	rows := loadMatrixRows(t)
	byMethod := make(map[string][]string)
	for _, row := range rows {
		name := row[0]
		if _, dup := byMethod[name]; dup {
			t.Fatalf("matrix has duplicate row for %q", name)
		}
		byMethod[name] = row
		for i, cell := range row {
			if cell == "" {
				t.Fatalf("matrix row %q leaves %q blank; state the contract explicitly", name, matrixColumns[i])
			}
		}
	}
	registered := make(map[string]bool)
	for _, name := range RegisteredNames() {
		registered[name] = true
		if _, ok := byMethod[name]; !ok {
			t.Fatalf("registered method %q has no moving-tag matrix row", name)
		}
	}
	for name := range byMethod {
		if !registered[name] {
			t.Fatalf("matrix row %q names no registered method", name)
		}
	}
	// Pending, hash, and requireCanonical support must name their tests:
	// a row claiming coverage in a support cell without a Tests entry is
	// an unpinned claim.
	hashCol, pendingCol, canonCol, testsCol := 4, 8, 9, 14
	known, err := repoTestFuncNames(moduleRoot(t))
	if err != nil {
		t.Fatalf("scan test funcs: %v", err)
	}
	for name, row := range byMethod {
		if strings.Contains(row[hashCol], "yes") && row[testsCol] == "" {
			t.Fatalf("matrix row %q claims hash support with no test", name)
		}
		if strings.Contains(row[pendingCol], "yes") && row[testsCol] == "" {
			t.Fatalf("matrix row %q claims pending support with no test", name)
		}
		if strings.Contains(row[canonCol], "preserved") && row[testsCol] == "" {
			t.Fatalf("matrix row %q claims requireCanonical with no assertion", name)
		}
		if err := matrixTestColumn(row[testsCol], known); err != nil {
			t.Fatalf("matrix row %q Tests: %v", name, err)
		}
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root %s: %v", root, err)
	}
	return root
}

// repoTestFuncNames scans top-level func Test*(t *testing.T) declarations
// with go/parser. TestMain, methods, and helpers are not test functions.
func repoTestFuncNames(root string) (map[string]bool, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "testdata":
				if path != root {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return testFuncNames(paths, func(path string) (*ast.File, error) {
		return parser.ParseFile(token.NewFileSet(), path, nil, 0)
	})
}

func testFuncNames(paths []string, parse func(string) (*ast.File, error)) (map[string]bool, error) {
	known := make(map[string]bool)
	for _, path := range paths {
		file, err := parse(path)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		for _, name := range testFuncNamesIn(file) {
			known[name] = true
		}
	}
	return known, nil
}

func testFuncNamesIn(file *ast.File) []string {
	var names []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Name == nil || !isGoTestFunc(fn) {
			continue
		}
		names = append(names, fn.Name.Name)
	}
	return names
}

func isGoTestFunc(fn *ast.FuncDecl) bool {
	if !isCitableTestName(fn.Name.Name) {
		return false
	}
	if fn.Type.Params == nil || len(fn.Type.Params.List) != 1 {
		return false
	}
	star, ok := fn.Type.Params.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "testing" && sel.Sel.Name == "T"
}

// matrixTestColumn checks one Tests cell against functions the scanner saw.
// known is the fixture or the repository scan; this never rewrites the matrix.
func matrixTestColumn(cell string, known map[string]bool) error {
	if strings.TrimSpace(cell) == "" {
		return errors.New("empty Tests cell")
	}
	for _, part := range strings.Split(cell, ",") {
		name := strings.TrimSpace(part)
		if !isCitableTestName(name) {
			return fmt.Errorf("%q is not a complete Test function name", name)
		}
		if !known[name] {
			return fmt.Errorf("test func %q is not declared", name)
		}
	}
	return nil
}

// isCitableTestName is Go's test discovery rule for the Test prefix
// (cmd/go isTest): the name is "Test", or the first character after "Test"
// is not a lowercase letter. Test_Example and Test123 qualify; Testhelper
// does not. The token must also be one Go identifier, so wildcards and
// notes are not names. TestMain is the test harness hook, not a case.
func isCitableTestName(name string) bool {
	if name == "TestMain" || !token.IsIdentifier(name) || !strings.HasPrefix(name, "Test") {
		return false
	}
	rest := name[len("Test"):]
	if rest == "" {
		return true
	}
	r, _ := utf8.DecodeRuneInString(rest)
	return !unicode.IsLower(r)
}

// TestMatrixTestsColumnRejectsMissingAndRenamed proves the interlock fails
// closed on an in-memory name set. It does not edit moving-tag-matrix.md.
func TestMatrixTestsColumnRejectsMissingAndRenamed(t *testing.T) {
	const src = `package fixture
import "testing"
type S struct{}
func Test(t *testing.T) {}
func TestKept(t *testing.T) {}
func TestRenamed(t *testing.T) {}
func Test_Example(t *testing.T) {}
func Test123(t *testing.T) {}
func TestMain(m *testing.M) {}
func Testhelper(t *testing.T) {}
func helper(t *testing.T) {}
func (s *S) TestMethod(t *testing.T) {}
func TestnotATest(t *testing.T) {}
`
	known, err := testFuncNames([]string{"mem_test.go"}, func(string) (*ast.File, error) {
		return parser.ParseFile(token.NewFileSet(), "mem_test.go", src, 0)
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Test", "TestKept", "TestRenamed", "Test_Example", "Test123"}
	if len(known) != len(want) {
		t.Fatalf("scanner saw %v, want %v", known, want)
	}
	for _, name := range want {
		if !known[name] {
			t.Fatalf("scanner rejected %s", name)
		}
	}
	for _, name := range []string{"TestMain", "Testhelper", "TestnotATest", "helper", "TestMethod"} {
		if known[name] {
			t.Fatalf("scanner accepted %s", name)
		}
	}
	if err := matrixTestColumn("Test, TestKept, Test_Example, Test123", known); err != nil {
		t.Fatal(err)
	}
	if err := matrixTestColumn("Testhelper", known); err == nil {
		t.Fatal("Testhelper was accepted")
	}
	if err := matrixTestColumn("TestMain", known); err == nil {
		t.Fatal("TestMain was accepted")
	}
	if err := matrixTestColumn("TestMissing", known); err == nil {
		t.Fatal("missing test func was accepted")
	}
	if err := matrixTestColumn("TestOldName", known); err == nil {
		t.Fatal("renamed test func was accepted under its old name")
	}
	for _, alias := range []string{
		"as above",
		"TestAdapterBlockByHash*",
		"methods tests",
		"TestFeeHistoryRejectsHashInNewestBlock (T02)",
		"eth_feahistory_test.go",
	} {
		if err := matrixTestColumn(alias, known); err == nil {
			t.Fatalf("alias %q was accepted", alias)
		}
	}
}

func TestMethodContractsForbiddenClaims(t *testing.T) {
	doc := readRepoDoc(t, filepath.Join("docs", "design", "method-contracts.md"))
	assertNoForbiddenClaims(t, "method-contracts", doc)
	if !strings.Contains(doc, "provider-backed") {
		t.Fatal("method-contracts lost its provider-backed evidence wording")
	}
	for _, name := range []string{
		"TestNormalizeCallOutcomeSeparatesRevertDataStates",
		"TestEmptyBlockUnclesAreLegalAndForeignBlockIsRejected",
		"TestReferenceAnchorFailureKeepsPrepareErrorAndSkipsBusinessFanout",
		"TestReferenceEstablishedFreshPerCall",
		"TestReferenceFailureEvidenceIsUnknownWithObservedAt",
	} {
		if !strings.Contains(doc, name) {
			t.Fatalf("method-contracts does not link %s", name)
		}
	}
}

// TestRuntimeEvidenceDocsForbiddenClaims covers the T05 documents that sit
// outside the method matrix: the decision record, the roadmap section, and
// the metrics migration comment.
func TestRuntimeEvidenceDocsForbiddenClaims(t *testing.T) {
	for _, rel := range []string{
		filepath.Join("docs", "decisions.md"),
		filepath.Join("observe", "prometheus", "metrics.go"),
	} {
		assertNoForbiddenClaims(t, rel, readRepoDoc(t, rel))
	}
	roadmap := readRepoDoc(t, filepath.Join("docs", "design", "project-roadmap.md"))
	start := strings.Index(roadmap, "## T05：")
	end := strings.Index(roadmap, "## 暂不推进")
	if start < 0 || end < start {
		t.Fatal("roadmap lost the T05 section")
	}
	section := roadmap[start:end]
	assertNoForbiddenClaims(t, "roadmap T05", section)
	for _, name := range []string{
		"TestDecisionEventStatusMatchesSuccessClass",
		"TestDecisionEventFailedStatusForClassifiedErrors",
		"TestStatusStringSetExcludesFailed",
		"TestReferenceAnchorFailureKeepsPrepareErrorAndSkipsBusinessFanout",
		"TestReferenceEstablishedFreshPerCall",
		"TestReferenceFailureEvidenceIsUnknownWithObservedAt",
		"TestDocumentedDecisionStatusLabelsMatchEmissions",
		"0038",
	} {
		if !strings.Contains(section, name) {
			t.Fatalf("roadmap T05 does not link %s", name)
		}
	}
	decisions := readRepoDoc(t, filepath.Join("docs", "decisions.md"))
	if !strings.Contains(decisions, "## 0038.") || !strings.Contains(decisions, "Rejected:") {
		t.Fatal("decision 0038 is missing or does not record the rejected helper")
	}
	metrics := readRepoDoc(t, filepath.Join("observe", "prometheus", "metrics.go"))
	if !strings.Contains(metrics, "Documented emitted status labels:") {
		t.Fatal("metrics migration comment lost its status list")
	}
}

func readRepoDoc(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func assertNoForbiddenClaims(t *testing.T, name, doc string) {
	t.Helper()
	for _, claim := range forbiddenClaims {
		if strings.Contains(doc, claim) {
			t.Fatalf("%s contains forbidden claim %q", name, claim)
		}
	}
}
