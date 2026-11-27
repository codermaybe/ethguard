package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/codermaybe/ethguard/internal/executionapi"
)

func main() {
	out := flag.String("out", "types.gen.go", "output Go file")
	auditOut := flag.String("audit-out", "", "deterministic generation audit output")
	manifestPath := flag.String("manifest", "internal/executionapi/manifest.json", "build-time generation contract")
	sourceDir := flag.String("source-dir", "", "candidate spec directory; empty uses embedded source")
	flag.Parse()
	manifest, err := executionapi.LoadManifest(*manifestPath)
	if err != nil {
		fail("%v", err)
	}
	var bundle executionapi.Bundle
	if *sourceDir == "" {
		bundle, err = executionapi.Read()
	} else {
		bundle, err = executionapi.ReadFrom(os.DirFS(*sourceDir))
	}
	if err != nil {
		fail("%v", err)
	}
	reg, roots, err := compileBundle(bundle, manifest)
	if err != nil {
		fail("%v", err)
	}
	staging, err := os.MkdirTemp("", "ethguard-wiregen-")
	if err != nil {
		fail("%v", err)
	}
	defer func() { _ = os.RemoveAll(staging) }()
	generated := filepath.Join(staging, "types.gen.go")
	if err = emit(reg, bundle.Version, roots, generated); err != nil {
		fail("%v", err)
	}
	audit := filepath.Join(staging, "types.audit.json")
	// Audit validation always runs, even when no audit output was requested.
	if err = writeGenerationAudit(bundle, reg, roots, generated, audit); err != nil {
		fail("%v", err)
	}
	files := map[string]string{*out: generated}
	if *auditOut != "" {
		if same, err := sameOutputFile(*auditOut, *out); err != nil {
			fail("%v", err)
		} else if same {
			fail("Go and audit output must differ")
		}
		files[*auditOut] = audit
	}
	if err = publishGenerated(files); err != nil {
		fail("%v", err)
	}
	fmt.Fprintf(os.Stderr, "gen: wrote %s (%d objects, %d fields, %d unions, %d shims)\n", *out, reg.report.objects, reg.report.countFields, len(reg.unionList), reg.report.countShims)
}

func compileBundle(bundle executionapi.Bundle, manifest executionapi.Manifest) (*registry, map[string]*ref, error) {
	if err := manifest.Validate(); err != nil {
		return nil, nil, err
	}
	if err := validateSourceSelection(bundle, manifest); err != nil {
		return nil, nil, err
	}
	if _, err := reviewedScalarPolicies(manifest); err != nil {
		return nil, nil, err
	}
	if err := executionapi.ValidateSelectedData(bundle, manifest.Selections()); err != nil {
		return nil, nil, err
	}
	reg := newRegistry(manifest, &genReport{visited: map[string]bool{}})
	for _, applied := range bundle.Errata {
		reg.report.errata = append(reg.report.errata, applied.ID+" SHA-256 "+applied.SHA256)
	}
	names := append([]string{}, manifest.ResultMethods...)
	sort.Strings(names)
	if len(names) != len(bundle.Results) {
		return nil, nil, fmt.Errorf("spec: Manifest result selection and source inventory differ")
	}
	for _, name := range names {
		raw, err := executionapi.SelectSchema(bundle, executionapi.SchemaSelection{Source: executionapi.SchemaRef{Method: name, Part: "result"}})
		if err != nil {
			return nil, nil, err
		}
		node, err := parseSchema(raw)
		if err != nil {
			return nil, nil, err
		}
		if err = reg.registerModelNames(node, name+".result"); err != nil {
			return nil, nil, err
		}
	}
	roots := make(map[string]*ref, len(names))
	for _, name := range names {
		node, err := parseSchema(bundle.Results[name])
		if err != nil {
			return nil, nil, err
		}
		root, err := reg.walk(node, name+".result")
		if err != nil {
			return nil, nil, err
		}
		roots[name] = root
		reg.report.methods = append(reg.report.methods, name)
	}
	if err := generateRequestRoots(reg, bundle.Requests); err != nil {
		return nil, nil, err
	}
	if err := reg.validatePolicyUsage(); err != nil {
		return nil, nil, err
	}
	if err := reg.validatePublicNames(roots); err != nil {
		return nil, nil, err
	}
	return reg, roots, nil
}

// Prepare both outputs before replacing either. On publication failure restore
// prior bytes so failed generation does not leave a successful-looking pair.
func publishGenerated(files map[string]string) error {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	staged := map[string]string{}
	old := map[string][]byte{}
	existed := map[string]bool{}
	defer func() {
		for _, path := range staged {
			_ = os.Remove(path)
		}
	}()
	for _, path := range paths {
		before, err := os.ReadFile(path)
		if err == nil {
			old[path] = before
			existed[path] = true
		} else if !os.IsNotExist(err) {
			return err
		}
		raw, err := os.ReadFile(files[path])
		if err != nil {
			return err
		}
		f, err := os.CreateTemp(filepath.Dir(path), ".wiregen-*")
		if err != nil {
			return err
		}
		staged[path] = f.Name()
		if _, err = f.Write(raw); err != nil {
			_ = f.Close()
			return err
		}
		if err = f.Chmod(0644); err != nil {
			_ = f.Close()
			return err
		}
		if err = f.Close(); err != nil {
			return err
		}
	}
	for i, path := range paths {
		if err := os.Rename(staged[path], path); err != nil {
			for _, done := range paths[:i] {
				if existed[done] {
					if rollbackErr := os.WriteFile(done, old[done], 0644); rollbackErr != nil {
						return fmt.Errorf("publish: %v; restore %s: %w", err, done, rollbackErr)
					}
				} else {
					if rollbackErr := os.Remove(done); rollbackErr != nil {
						return fmt.Errorf("publish: %v; remove %s: %w", err, done, rollbackErr)
					}
				}
			}
			return err
		}
	}
	return nil
}
func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "gen: "+format+"\n", args...)
	os.Exit(1)
}

func sameOutputFile(a, b string) (bool, error) {
	left, err := filepath.Abs(a)
	if err != nil {
		return false, err
	}
	right, err := filepath.Abs(b)
	if err != nil {
		return false, err
	}
	if left == right {
		return true, nil
	}
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	if leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo) {
		return true, nil
	}
	// Resolve directory symlinks even when the final file does not exist yet.
	leftDir, err := filepath.EvalSymlinks(filepath.Dir(left))
	if err != nil {
		return false, err
	}
	rightDir, err := filepath.EvalSymlinks(filepath.Dir(right))
	if err != nil {
		return false, err
	}
	return filepath.Join(leftDir, filepath.Base(left)) == filepath.Join(rightDir, filepath.Base(right)), nil
}
