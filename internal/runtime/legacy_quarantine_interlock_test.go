package runtime

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The legacy constructor quarantine. The default runtime assembles the
// method registry from explicit execution plans; the hand-written
// rpc/methods constructors are a compatibility and plan-equivalence surface,
// not a runtime fact source. This interlock keeps three facts executable:
//
//   - internal/runtime calls no legacy constructor, except the reference
//     establishment seam files declared here with a reason — the strict-head
//     round and the head contract codec are the two production consumers
//     the seam contract already records, pinned so any new consumer is a
//     reviewable diff;
//   - the plan registry is assembled exactly once: the method registry file
//     is the only production file of this package that compiles plans, so
//     no second registry can appear beside it;
//   - adding a new constructor to rpc/methods cannot smuggle a new runtime
//     dependency: the audited constructor set is re-derived from the
//     production sources on every run.

// legacyConstructorSeamAllowlist names the production files of this package
// that may call a rpc/methods legacy constructor, each with the reason it
// stays. Establishing a reference is the establishment algorithms' own
// business, and the compatibility constructor is what currently carries the
// decision-0006 default those algorithms depend on (RS4 owns the migration).
var legacyConstructorSeamAllowlist = map[string]string{
	"strict_head.go": "strict-head establishment round resolves through the block constructor's anchored policy",
	"head_codec.go":  "the head contract's identity spelling comes from the block constructor's normalizer",
}

// formatterPolicyConstructors re-derives the audited constructor set from
// the rpc/methods production sources: every exported function declaration
// whose name ends in Policy.
func methodPolicyConstructors(t *testing.T) []string {
	t.Helper()
	dir := modulePackageDir(t, "github.com/codermaybe/ethguard/rpc/methods")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read rpc/methods sources: %v", err)
	}
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, "_test.go") || !strings.HasSuffix(name, ".go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse rpc/methods %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil {
				continue
			}
			if strings.HasSuffix(fn.Name.Name, "Policy") && ast.IsExported(fn.Name.Name) {
				names = append(names, fn.Name.Name)
			}
		}
	}
	if len(names) == 0 {
		t.Fatal("the audited constructor inventory is empty; audit the rpc/methods source set")
	}
	sort.Strings(names)
	return names
}

// modulePackageDir resolves one module package's source directory so the
// audit works from any working directory.
func modulePackageDir(t *testing.T, importPath string) string {
	t.Helper()
	out, err := exec.Command("go", "list", "-f", "{{.Dir}}", importPath).Output()
	if err != nil {
		t.Fatalf("go list %s: %v", importPath, err)
	}
	return strings.TrimSpace(string(out))
}

// productionFiles returns the named package's non-test source files with
// their parsed ASTs.
func productionFiles(t *testing.T, importPath string) map[string]*ast.File {
	t.Helper()
	dir := modulePackageDir(t, importPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s sources: %v", importPath, err)
	}
	files := make(map[string]*ast.File)
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, "_test.go") || !strings.HasSuffix(name, ".go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s %s: %v", importPath, name, err)
		}
		files[name] = file
	}
	return files
}

// TestRuntimeCallsNoLegacyConstructorOutsideTheEstablishmentSeam walks the
// package's production ASTs: a call to a rpc/methods constructor may only
// appear in the declared establishment seam files, each carrying its audited
// reason.
func TestRuntimeCallsNoLegacyConstructorOutsideTheEstablishmentSeam(t *testing.T) {
	constructors := methodPolicyConstructors(t)
	files := productionFiles(t, "github.com/codermaybe/ethguard/internal/runtime")
	var violations []string
	for name, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			receiver, ok := selector.X.(*ast.Ident)
			if !ok || receiver.Name != "methods" {
				return true
			}
			for _, constructor := range constructors {
				if selector.Sel.Name != constructor {
					continue
				}
				reason, allowed := legacyConstructorSeamAllowlist[name]
				if !allowed {
					violations = append(violations, fmt.Sprintf(
						"internal/runtime/%s builds %s from the %s constructor; the default runtime assembles methods from execution plans",
						name, selector.Sel.Name, "methods"))
					return true
				}
				if reason == "" {
					violations = append(violations, fmt.Sprintf(
						"allowlist entry without reason: internal/runtime/%s", name))
				}
			}
			return true
		})
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("legacy constructor quarantine violations:\n  %s", strings.Join(violations, "\n  "))
	}
}

// TestRuntimeRegistryIsAssembledOnce keeps the plan registry single: the
// method registry file is the only production source of this package that
// compiles execution plans, so a second registry cannot appear beside it
// without a surfaced diff.
func TestRuntimeRegistryIsAssembledOnce(t *testing.T) {
	files := productionFiles(t, "github.com/codermaybe/ethguard/internal/runtime")
	var violations []string
	for name, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "CompilePlan" {
				return true
			}
			if receiver, ok := selector.X.(*ast.Ident); ok && receiver.Name == "methods" && name != "method_registry.go" {
				violations = append(violations, fmt.Sprintf(
					"internal/runtime/%s compiles execution plans; the registry is assembled only in method_registry.go", name))
			}
			return true
		})
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("second-registry violations:\n  %s", strings.Join(violations, "\n  "))
	}
}
