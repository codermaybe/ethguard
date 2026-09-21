package methods

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	}
}
