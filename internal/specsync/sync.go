package specsync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/codermaybe/ethguard/internal/executionapi"
	"github.com/codermaybe/ethguard/internal/spec/builder"
	"github.com/codermaybe/ethguard/internal/spec/conformance"
)

type Options struct {
	Inputs                   Inputs
	Workspace, Manifest, Out string
	GoTool                   string
	PrepareOnly              bool
}
type Evidence struct {
	Command   []string `json:"command"`
	Status    string   `json:"status"`
	Log       string   `json:"log,omitempty"`
	LogSHA256 string   `json:"logSHA256,omitempty"`
	Reason    string   `json:"reason,omitempty"`
}
type GeneratedDiff struct {
	Artifacts         []conformance.ArtifactDiff `json:"artifacts"`
	AddedSignatures   []string                   `json:"addedSignatures"`
	RemovedSignatures []string                   `json:"removedSignatures"`
}
type Report struct {
	Mode                 string                       `json:"mode"`
	Ready                bool                         `json:"ready"`
	Publishable          bool                         `json:"publishable"`
	Source               executionapi.SourceMetadata  `json:"source"`
	BaselineSourceSHA256 string                       `json:"baselineSourceSHA256,omitempty"`
	ManifestSHA256       string                       `json:"manifestSHA256,omitempty"`
	SnapshotSHA256       string                       `json:"snapshotSHA256,omitempty"`
	RawDiff              *conformance.FullDiff        `json:"rawDiff,omitempty"`
	EffectiveDiff        *conformance.FullDiff        `json:"effectiveDiff,omitempty"`
	Overlays             []executionapi.OverlayStatus `json:"overlays,omitempty"`
	Generated            *GeneratedDiff               `json:"generated,omitempty"`
	Validation           []Evidence                   `json:"validation"`
	Conformance          *conformance.Report          `json:"originalConformance,omitempty"`
	Problems             []string                     `json:"problems"`
	Toolchain            *GoToolchain                 `json:"toolchain,omitempty"`
}

// Run writes only a new candidate directory. Failure still preserves a report;
// neither this function nor any child command modifies active source inputs.
func Run(ctx context.Context, options Options) (Report, error) {
	report := Report{Validation: []Evidence{}, Problems: []string{}}
	mode, err := options.Inputs.Mode()
	if err != nil {
		return report, err
	}
	report.Mode = mode
	workspace, err := resolvedPath(options.Workspace)
	if err != nil {
		return report, err
	}
	if options.Out == "" {
		return report, fmt.Errorf("candidate output directory is required")
	}
	out, err := resolvedPath(options.Out)
	if err != nil {
		return report, err
	}
	if out == workspace || strings.HasPrefix(out, workspace+string(filepath.Separator)) {
		return report, fmt.Errorf("candidate directory must be outside the active workspace")
	}
	if _, err := os.Stat(out); err == nil {
		return report, fmt.Errorf("candidate output already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return report, err
	}
	if err := os.MkdirAll(filepath.Dir(out), 0755); err != nil {
		return report, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(out), ".specsync-")
	if err != nil {
		return report, err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	manifestPath := options.Manifest
	if manifestPath == "" {
		manifestPath = filepath.Join(workspace, "internal/executionapi/manifest.json")
	}
	runErr := prepare(ctx, options, workspace, manifestPath, stage, &report)
	if runErr != nil {
		report.Problems = append(report.Problems, runErr.Error())
		report.Ready = false
		report.Publishable = false
	}
	if err := writeJSON(filepath.Join(stage, "report.json"), report); err != nil {
		return report, err
	}
	if err := os.Rename(stage, out); err != nil {
		return report, err
	}
	return report, runErr
}
func prepare(ctx context.Context, options Options, workspace, manifestPath, stage string, report *Report) error {
	manifest, err := executionapi.LoadManifest(manifestPath)
	if err != nil {
		return err
	}
	report.ManifestSHA256 = manifest.SHA256
	baselineSource, err := os.ReadFile(filepath.Join(workspace, "internal/spec/SOURCE.json"))
	if err != nil {
		return err
	}
	report.BaselineSourceSHA256 = hash(baselineSource)
	acquisition, err := os.MkdirTemp("", "ethguard-spec-source-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(acquisition) }()
	var toolchain GoToolchain
	if report.Mode != "offline-materials" || !options.PrepareOnly {
		toolchain, err = selectGoToolchain(ctx, workspace, options.GoTool)
		if err != nil {
			return err
		}
		report.Toolchain = &toolchain
	}
	material, err := acquire(ctx, options.Inputs, workspace, acquisition, manifest, toolchain)
	if err != nil {
		return err
	}
	artifacts, err := builder.BuildSelected(material.OpenRPC, material.Source.Version, manifest)
	if err != nil {
		return err
	}
	specDir := filepath.Join(stage, "spec")
	if _, err := copyTree(filepath.Join(workspace, "internal/spec"), specDir); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(specDir, "vectors")); err != nil {
		return err
	}
	vectorSource := VectorSource{Repository: material.Source.Repository, Version: material.Source.Version, Commit: material.Source.Commit, UpstreamVectorDirectory: "tests", Vectors: map[string]string{}}
	selected := map[string]bool{}
	for _, method := range manifest.ResultMethods {
		selected[method] = true
	}
	for path, raw := range material.Vectors {
		method, _, _ := strings.Cut(path, "/")
		if !selected[method] {
			continue
		}
		if err := writeFile(filepath.Join(specDir, "vectors", filepath.FromSlash(path)), raw); err != nil {
			return err
		}
		vectorSource.Vectors[path] = hash(raw)
	}
	vectorBytes, err := encodeJSON(vectorSource)
	if err != nil {
		return err
	}
	source := material.Source
	source.Bundles = map[string]string{"methods.json": hash(artifacts.Methods), "requests.json": hash(artifacts.Requests), "inventory.json": hash(artifacts.Inventory)}
	source.VectorManifestSHA256 = hash(vectorBytes)
	outputs := map[string][]byte{"methods.json": artifacts.Methods, "requests.json": artifacts.Requests, "inventory.json": artifacts.Inventory, "vectors/SOURCE.json": vectorBytes, "VERSION": []byte(source.Version + "\n")}
	sourceRaw, err := encodeJSON(source)
	if err != nil {
		return err
	}
	outputs["SOURCE.json"] = sourceRaw
	for name, raw := range outputs {
		if err := writeFile(filepath.Join(specDir, name), raw); err != nil {
			return err
		}
	}
	if err := writeJSON(filepath.Join(stage, "material-source.json"), MaterialSource{Source: source, Vectors: vectorSource}); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(stage, "openrpc.json"), material.OpenRPC); err != nil {
		return err
	}
	report.Source = source
	rawDiff, err := conformance.DiffDirectories(os.DirFS(filepath.Join(workspace, "internal/spec")), os.DirFS(specDir))
	if err != nil {
		return err
	}
	report.RawDiff = &rawDiff
	original, err := executionapi.ReadOriginalFrom(os.DirFS(specDir))
	if err != nil {
		return err
	}
	effective, statuses, err := executionapi.EvaluateOverlays(original, sourceRaw, os.DirFS(specDir))
	report.Overlays = statuses
	if err != nil {
		return err
	}
	for _, status := range statuses {
		for _, target := range status.Targets {
			if target.Status == "already-satisfied" {
				return fmt.Errorf("overlay %s at %s is a retirement candidate; review/remove it before declaring the candidate ready", status.ID, target.Source.Path())
			}
		}
	}
	if err := executionapi.ValidateSelectedData(effective, manifest.Selections()); err != nil {
		return err
	}
	baseline, err := executionapi.ReadFrom(os.DirFS(filepath.Join(workspace, "internal/spec")))
	if err != nil {
		return err
	}
	beforeResults, beforeRequests, err := effectiveEnvelopes(baseline)
	if err != nil {
		return err
	}
	afterResults, afterRequests, err := effectiveEnvelopes(effective)
	if err != nil {
		return err
	}
	effectiveDiff, err := conformance.DiffComplete(beforeResults, beforeRequests, afterResults, afterRequests)
	if err != nil {
		return err
	}
	report.EffectiveDiff = &effectiveDiff
	originalReport := conformance.Run(os.DirFS(specDir), artifacts.Methods, source.Version)
	report.Conformance = &originalReport
	// Original specification defects are reported faithfully. They are not
	// converted into evidence of effective-schema or runtime equivalence.
	if options.PrepareOnly {
		report.Validation = append(report.Validation, Evidence{Command: []string{"candidate-generation-and-tests"}, Status: "not-run", Reason: "prepare-only requested"})
		return nil
	}
	snapshot := filepath.Join(stage, "workspace")
	snapshotFiles, err := copyTree(workspace, snapshot)
	if err != nil {
		return err
	}
	currentFiles, err := treeDigests(workspace)
	if err != nil {
		return err
	}
	if !equalJSON(snapshotFiles, currentFiles) {
		return fmt.Errorf("workspace changed while creating snapshot; retry from a stable workspace")
	}
	if err := os.RemoveAll(filepath.Join(snapshot, "internal/spec")); err != nil {
		return err
	}
	if _, err := copyTree(specDir, filepath.Join(snapshot, "internal/spec")); err != nil {
		return err
	}
	manifestRaw, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	if hash(manifestRaw) != manifest.SHA256 {
		return fmt.Errorf("manifest changed during source extraction")
	}
	if err := writeFile(filepath.Join(snapshot, "internal/executionapi/manifest.json"), manifestRaw); err != nil {
		return err
	}
	if err := runEvidence(ctx, snapshot, stage, report, []string{"go", "run", "./internal/wiregen", "-source-dir", "internal/spec", "-manifest", "internal/executionapi/manifest.json", "-out", "ethapi/types.gen.go", "-audit-out", "ethapi/types.audit.json"}); err != nil {
		return err
	}
	generated, err := compareGenerated(workspace, snapshot)
	if err != nil {
		return err
	}
	report.Generated = generated
	for _, name := range []string{"types.gen.go", "types.audit.json"} {
		raw, err := os.ReadFile(filepath.Join(snapshot, "ethapi", name))
		if err != nil {
			return err
		}
		if err := writeFile(filepath.Join(stage, "generated", name), raw); err != nil {
			return err
		}
	}
	snapshotFiles, err = treeDigests(snapshot)
	if err != nil {
		return err
	}
	snapshotRaw, err := encodeJSON(snapshotFiles)
	if err != nil {
		return err
	}
	report.SnapshotSHA256 = hash(snapshotRaw)
	if err := writeFile(filepath.Join(stage, "snapshot.json"), snapshotRaw); err != nil {
		return err
	}
	if source.Category == "fixture" {
		if err := runEvidence(ctx, snapshot, stage, report, []string{"go", "build", "./ethapi"}); err != nil {
			return err
		}
		report.Validation = append(report.Validation, Evidence{Command: []string{"go", "test", "./..."}, Status: "not-applicable", Reason: "synthetic fixture cannot be consumed as embedded official runtime source"})
		report.Ready = true
		report.Publishable = false
		return nil
	}
	if err := runEvidence(ctx, snapshot, stage, report, []string{"go", "test", "-p", "2", "./..."}); err != nil {
		return err
	}
	if err := runEvidence(ctx, snapshot, stage, report, []string{"go", "vet", "./..."}); err != nil {
		return err
	}
	if err := runEvidence(ctx, snapshot, stage, report, []string{"go", "build", "./..."}); err != nil {
		return err
	}
	report.Validation = append(report.Validation, Evidence{Command: []string{"external-provider-replay"}, Status: "not-run", Reason: "requires separately reviewed provider replay evidence; specsync never invents it"})
	report.Ready = true
	report.Publishable = false // publication requires ordinary code and replay review.
	return nil
}
func runEvidence(ctx context.Context, snapshot, stage string, report *Report, args []string) error {
	output, err := command(ctx, snapshot, true, report.Toolchain.Path, args[1:]...)
	log := fmt.Sprintf("logs/%02d.log", len(report.Validation)+1)
	if writeErr := writeFile(filepath.Join(stage, log), output); writeErr != nil {
		return writeErr
	}
	evidence := Evidence{Command: args, Status: "passed", Log: log, LogSHA256: hash(output)}
	if err != nil {
		evidence.Status = "failed"
	}
	report.Validation = append(report.Validation, evidence)
	return err
}
func writeFile(path string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0644)
}
func encodeJSON(value any) ([]byte, error) {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}
func writeJSON(path string, value any) error {
	raw, err := encodeJSON(value)
	if err != nil {
		return err
	}
	return writeFile(path, raw)
}
func effectiveEnvelopes(bundle executionapi.Bundle) ([]byte, []byte, error) {
	a, err := json.Marshal(struct {
		Version string                     `json:"version"`
		Results map[string]json.RawMessage `json:"results"`
	}{bundle.Version, bundle.Results})
	if err != nil {
		return nil, nil, err
	}
	b, err := json.Marshal(struct {
		Version  string                              `json:"version"`
		Requests map[string][]executionapi.Parameter `json:"requests"`
	}{bundle.Version, bundle.Requests})
	return a, b, err
}
func copyTree(source, dest string) (map[string]string, error) {
	sums := map[string]string{}
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == ".git" {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("snapshot symlinks need explicit review: %s", relative)
		}
		if entry.IsDir() {
			return os.MkdirAll(filepath.Join(dest, relative), 0755)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := writeFile(filepath.Join(dest, relative), raw); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if err := os.Chmod(filepath.Join(dest, relative), info.Mode().Perm()); err != nil {
			return err
		}
		sums[filepath.ToSlash(relative)] = hash(raw)
		return nil
	})
	return sums, err
}
func treeDigests(root string) (map[string]string, error) {
	sums := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == ".git" {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("snapshot symlink: %s", relative)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sums[filepath.ToSlash(relative)] = hash(raw)
		return nil
	})
	return sums, err
}
func compareGenerated(before, after string) (*GeneratedDiff, error) {
	out := &GeneratedDiff{Artifacts: []conformance.ArtifactDiff{}, AddedSignatures: []string{}, RemovedSignatures: []string{}}
	for _, name := range []string{"types.gen.go", "types.audit.json"} {
		a, err := os.ReadFile(filepath.Join(before, "ethapi", name))
		if err != nil {
			return nil, err
		}
		b, err := os.ReadFile(filepath.Join(after, "ethapi", name))
		if err != nil {
			return nil, err
		}
		out.Artifacts = append(out.Artifacts, conformance.ArtifactDiff{Name: name, BeforeSHA256: hash(a), AfterSHA256: hash(b), Changed: !bytes.Equal(a, b)})
		if name == "types.gen.go" {
			left, err := publicSignatures(a)
			if err != nil {
				return nil, err
			}
			right, err := publicSignatures(b)
			if err != nil {
				return nil, err
			}
			for signature := range left {
				if !right[signature] {
					out.RemovedSignatures = append(out.RemovedSignatures, signature)
				}
			}
			for signature := range right {
				if !left[signature] {
					out.AddedSignatures = append(out.AddedSignatures, signature)
				}
			}
		}
	}
	sort.Strings(out.AddedSignatures)
	sort.Strings(out.RemovedSignatures)
	return out, nil
}
func publicSignatures(raw []byte) (map[string]bool, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "types.gen.go", raw, 0)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	emit := func(node any) error {
		var b bytes.Buffer
		if err := printer.Fprint(&b, fset, node); err != nil {
			return err
		}
		out[b.String()] = true
		return nil
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if ast.IsExported(d.Name.Name) {
				copy := *d
				copy.Body = nil
				if err := emit(&copy); err != nil {
					return nil, err
				}
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if ast.IsExported(s.Name.Name) {
						if err := emit(s); err != nil {
							return nil, err
						}
					}
				case *ast.ValueSpec:
					for _, name := range s.Names {
						if ast.IsExported(name.Name) {
							if err := emit(s); err != nil {
								return nil, err
							}
							break
						}
					}
				}
			}
		}
	}
	return out, nil
}

// Resolve the existing ancestor as well as a nonexistent destination suffix.
// Lexical containment alone permits an outside symlink to write into the
// active workspace before snapshot copying starts.
func resolvedPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	ancestor := absolute
	var suffix []string
	for {
		_, err := os.Lstat(ancestor)
		if err == nil {
			resolved, err := filepath.EvalSymlinks(ancestor)
			if err != nil {
				return "", err
			}
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return resolved, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", fmt.Errorf("no existing ancestor for %s", path)
		}
		suffix = append(suffix, filepath.Base(ancestor))
		ancestor = parent
	}
}
