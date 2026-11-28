package executionapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/codermaybe/ethguard/internal/jsonsafe"
	jsonpatch "github.com/evanphx/json-patch/v5"
)

const DataOriginalPattern = "^0x[0-9a-f]*$"
const DataWholeBytePattern = "^0x([0-9a-f]{2})*$"

// AppliedErratum contains measured input/output identities, never handwritten
// output checksums. Targets retain every application and retirement candidate.
type AppliedErratum struct {
	ID, File, SHA256                      string
	Kind                                  string
	InputSchemaSHA256, OutputSchemaSHA256 string
	Targets                               []AppliedTarget
}
type AppliedTarget struct {
	Source                                                SchemaRef
	Status                                                string
	BeforeSHA256, AfterSHA256                             string
	OriginalRootSHA256, InputRootSHA256, OutputRootSHA256 string
	ChangedPointers                                       []string
	Detail                                                string `json:",omitempty"`
}
type OverlayStatus struct {
	ID, File, Kind string
	Targets        []AppliedTarget
}
type schemaTarget struct {
	Method    string `json:"method"`
	Part      string `json:"part"`
	Parameter *int   `json:"parameter,omitempty"`
}

func (t schemaTarget) ref() SchemaRef {
	return SchemaRef{Method: t.Method, Part: t.Part, Parameter: t.Parameter}
}

type schemaGuard struct {
	Pointer string          `json:"pointer"`
	Value   json.RawMessage `json:"value,omitempty"`
	Absent  bool            `json:"absent,omitempty"`
}
type dataTarget struct {
	Source   SchemaRef       `json:"source"`
	Identity json.RawMessage `json:"identity"`
}
type DataException struct {
	Source     SchemaRef       `json:"source"`
	Identity   json.RawMessage `json:"identity"`
	Reason     string          `json:"reason"`
	RetireWhen string          `json:"retireWhen"`
}
type erratum struct {
	ID          string          `json:"id"`
	Kind        string          `json:"kind"`
	DependsOn   []string        `json:"dependsOn,omitempty"`
	Target      schemaTarget    `json:"target,omitempty"`
	Reason      string          `json:"reason"`
	References  []string        `json:"references"`
	RetireWhen  string          `json:"retireWhen"`
	Before      []schemaGuard   `json:"before,omitempty"`
	After       []schemaGuard   `json:"after,omitempty"`
	Patch       json.RawMessage `json:"patch,omitempty"`
	FromPattern string          `json:"fromPattern,omitempty"`
	ToPattern   string          `json:"toPattern,omitempty"`
	Targets     []dataTarget    `json:"targets,omitempty"`
	Exceptions  []DataException `json:"exceptions,omitempty"`
}
type overlayFile struct {
	definition erratum
	name       string
	raw        []byte
}

func decodeStrict(raw []byte, out any) error {
	if err := jsonsafe.Validate(raw); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}

func loadOverlays(files fs.FS, applied ...AppliedErratum) ([]overlayFile, error) {
	entries, err := fs.ReadDir(files, "errata")
	if err != nil {
		return nil, fmt.Errorf("errata inventory: %w", err)
	}
	byID := map[string]overlayFile{}
	previous := map[string]bool{}
	for _, e := range applied {
		previous[e.ID] = true
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		name := "errata/" + entry.Name()
		raw, err := fs.ReadFile(files, name)
		if err != nil {
			return nil, err
		}
		var e erratum
		if err := decodeStrict(raw, &e); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if strings.TrimSpace(e.ID) == "" || strings.TrimSpace(e.Reason) == "" || strings.TrimSpace(e.RetireWhen) == "" || len(e.References) == 0 {
			return nil, fmt.Errorf("%s: incomplete audit metadata", name)
		}
		for _, ref := range e.References {
			u, err := url.Parse(ref)
			if err != nil || u.Scheme != "https" || u.Host == "" {
				return nil, fmt.Errorf("%s: reference must be an absolute HTTPS URL", name)
			}
		}
		if _, ok := byID[e.ID]; ok {
			return nil, fmt.Errorf("duplicate erratum %q", e.ID)
		}
		switch e.Kind {
		case "patch":
			if err := e.Target.ref().Validate(); err != nil {
				return nil, fmt.Errorf("%s target: %w", name, err)
			}
			if len(e.Before) == 0 || len(e.After) == 0 {
				return nil, fmt.Errorf("%s: patch requires before and after local guards", name)
			}
			if e.FromPattern != "" || e.ToPattern != "" || len(e.Targets) > 0 || len(e.Exceptions) > 0 {
				return nil, fmt.Errorf("%s: mixed rule kinds", name)
			}
			var ops []map[string]json.RawMessage
			if err := decodeStrict(e.Patch, &ops); err != nil || len(ops) == 0 {
				return nil, fmt.Errorf("%s: patch must contain operations", name)
			}
			writes := 0
			for _, op := range ops {
				var kind string
				if err := json.Unmarshal(op["op"], &kind); err != nil {
					return nil, fmt.Errorf("%s: invalid patch operation: %w", name, err)
				}
				if kind != "test" {
					writes++
				}
			}
			if writes == 0 {
				return nil, fmt.Errorf("%s: patch does not change the schema document", name)
			}
			for _, guards := range [][]schemaGuard{e.Before, e.After} {
				for _, g := range guards {
					if _, err := pointerTokens(g.Pointer); err != nil || g.Absent == (len(g.Value) > 0) {
						return nil, fmt.Errorf("%s: invalid local guard", name)
					}
				}
			}
		case "data-pattern":
			if e.FromPattern != DataOriginalPattern || e.ToPattern != DataWholeBytePattern || len(e.Targets) == 0 || len(e.Patch) > 0 || len(e.Before) > 0 || len(e.After) > 0 || e.Target.Method != "" {
				return nil, fmt.Errorf("%s: invalid DATA rule", name)
			}
			seen := map[string]bool{}
			for _, target := range e.Targets {
				if err := target.Source.Validate(); err != nil {
					return nil, err
				}
				if seen[target.Source.Path()] || len(target.Identity) == 0 {
					return nil, fmt.Errorf("%s: duplicate or unguarded DATA target %s", name, target.Source.Path())
				}
				seen[target.Source.Path()] = true
			}
			for _, e := range e.Exceptions {
				if err := e.Source.Validate(); err != nil {
					return nil, err
				}
				if seen[e.Source.Path()] || e.Reason == "" || e.RetireWhen == "" || len(e.Identity) == 0 {
					return nil, fmt.Errorf("%s: invalid DATA exception %s", name, e.Source.Path())
				}
				seen[e.Source.Path()] = true
			}
		default:
			return nil, fmt.Errorf("%s: unsupported overlay kind %q", name, e.Kind)
		}
		byID[e.ID] = overlayFile{e, name, raw}
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	state := map[string]int{}
	ordered := []overlayFile{}
	var visit func(string) error
	visit = func(id string) error {
		if state[id] == 1 {
			return fmt.Errorf("overlay dependency cycle at %s", id)
		}
		if state[id] == 2 {
			return nil
		}
		f, ok := byID[id]
		if !ok {
			if previous[id] {
				return nil
			}
			return fmt.Errorf("missing overlay dependency %s", id)
		}
		state[id] = 1
		deps := append([]string(nil), f.definition.DependsOn...)
		sort.Strings(deps)
		seen := map[string]bool{}
		for _, dep := range deps {
			if seen[dep] {
				return fmt.Errorf("duplicate dependency %s", dep)
			}
			seen[dep] = true
			if err := visit(dep); err != nil {
				return err
			}
		}
		state[id] = 2
		ordered = append(ordered, f)
		return nil
	}
	for _, id := range ids {
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	// Overlapping writes are only legal along a declared dependency path.
	var depends func(string, string) bool
	depends = func(id, want string) bool {
		for _, dep := range byID[id].definition.DependsOn {
			if dep == want || depends(dep, want) {
				return true
			}
		}
		return false
	}
	owners := map[string]string{}
	for _, f := range ordered {
		writes, err := overlayWrites(f.definition)
		if err != nil {
			return nil, err
		}
		for _, path := range writes {
			for prior, owner := range owners {
				if path == prior || strings.HasPrefix(path, prior+"/") || strings.HasPrefix(prior, path+"/") {
					if owner != f.definition.ID && !depends(f.definition.ID, owner) {
						return nil, fmt.Errorf("conflicting overlay writes %s and %s at %s", owner, f.definition.ID, path)
					}
				}
			}
			owners[path] = f.definition.ID
		}
	}
	return ordered, nil
}
func overlayWrites(e erratum) ([]string, error) {
	if e.Kind == "data-pattern" {
		out := []string{}
		for _, target := range e.Targets {
			out = append(out, target.Source.Path()+"/pattern")
		}
		return out, nil
	}
	var ops []struct{ Op, Path, From string }
	if err := json.Unmarshal(e.Patch, &ops); err != nil {
		return nil, err
	}
	out := []string{}
	seen := map[string]bool{}
	for _, op := range ops {
		if op.Op == "test" {
			continue
		}
		if op.Path == "" {
			return nil, fmt.Errorf("root replacement patches require an explicit reviewed subpath")
		}
		for _, pointer := range []string{op.Path, op.From} {
			if pointer == "" && pointer == op.From {
				continue
			}
			if _, err := pointerTokens(pointer); err != nil {
				return nil, err
			}
			path := e.Target.ref().Path() + pointer
			if !seen[path] {
				out = append(out, path)
				seen[path] = true
			}
		}
	}
	return out, nil
}
func cloneBundle(bundle Bundle) Bundle {
	out := bundle
	out.Results = map[string]json.RawMessage{}
	for k, v := range bundle.Results {
		out.Results[k] = v
	}
	out.Requests = map[string][]Parameter{}
	for k, v := range bundle.Requests {
		out.Requests[k] = append([]Parameter{}, v...)
	}
	out.Errata = append([]AppliedErratum(nil), bundle.Errata...)
	out.DataExceptions = append([]DataException(nil), bundle.DataExceptions...)
	return out
}

// EvaluateOverlays is the upgrade diagnostic boundary. It returns every target
// status and only commits the candidate Bundle when the whole inventory passes.
func EvaluateOverlays(bundle Bundle, sourceJSON []byte, files fs.FS) (Bundle, []OverlayStatus, error) {
	var source struct{ Version, Commit string }
	if err := json.Unmarshal(sourceJSON, &source); err != nil {
		return Bundle{}, nil, err
	}
	if source.Version != bundle.Version || !isDigest(source.Commit, 20) {
		return Bundle{}, nil, fmt.Errorf("errata source: invalid version or commit identity")
	}
	overlays, err := loadOverlays(files, bundle.Errata...)
	if err != nil {
		return Bundle{}, nil, err
	}
	seen := map[string]bool{}
	for _, applied := range bundle.Errata {
		seen[applied.ID] = true
	}
	candidate := cloneBundle(bundle)
	statuses := []OverlayStatus{}
	var failures []string
	for _, f := range overlays {
		e := f.definition
		if seen[e.ID] {
			return Bundle{}, statuses, fmt.Errorf("duplicate or already applied erratum %q", e.ID)
		}
		seen[e.ID] = true
		applied := AppliedErratum{ID: e.ID, File: f.name, SHA256: digest(f.raw), Kind: e.Kind}
		if e.Kind == "patch" {
			target := applyStructural(&candidate, e)
			applied.Targets = []AppliedTarget{target}
			applied.InputSchemaSHA256 = target.BeforeSHA256
			applied.OutputSchemaSHA256 = target.AfterSHA256
		} else {
			for _, target := range e.Targets {
				applied.Targets = append(applied.Targets, applyDataTarget(&candidate, e, target))
			}
			for _, exception := range e.Exceptions {
				raw, err := SelectSchema(candidate, SchemaSelection{Source: exception.Source})
				if err != nil || !sameJSON(raw, exception.Identity) {
					failures = append(failures, fmt.Sprintf("stale DATA exception %s", exception.Source.Path()))
				}
				candidate.DataExceptions = append(candidate.DataExceptions, exception)
			}
		}
		for i := range applied.Targets {
			target := &applied.Targets[i]
			originalRoot, originalErr := rootSchema(bundle, target.Source)
			if originalErr == nil {
				target.OriginalRootSHA256 = digest(originalRoot)
			}
			if target.InputRootSHA256 == "" && e.Kind == "patch" {
				target.InputRootSHA256 = target.BeforeSHA256
				target.OutputRootSHA256 = target.AfterSHA256
			}
			if target.Status == "review-required" {
				failures = append(failures, e.ID+" "+target.Source.Path()+": "+target.Detail)
			}
		}
		statuses = append(statuses, OverlayStatus{e.ID, f.name, e.Kind, applied.Targets})
		candidate.Errata = append(candidate.Errata, applied)
	}
	if len(failures) > 0 {
		return Bundle{}, statuses, fmt.Errorf("overlay review required: %s", strings.Join(failures, "; "))
	}
	return candidate, statuses, nil
}
func applyErrata(bundle *Bundle, sourceJSON []byte, files fs.FS) error {
	candidate, statuses, err := EvaluateOverlays(*bundle, sourceJSON, files)
	if err != nil {
		return err
	}
	for _, status := range statuses {
		for _, target := range status.Targets {
			if target.Status == "already-satisfied" {
				return fmt.Errorf("overlay %s target %s already satisfied; review retirement before normal consumption", status.ID, target.Source.Path())
			}
		}
	}
	*bundle = candidate
	return nil
}
func rootSchema(bundle Bundle, s SchemaRef) (json.RawMessage, error) {
	s.Pointer = ""
	return SelectSchema(bundle, SchemaSelection{Source: s})
}
func writeRoot(bundle *Bundle, s SchemaRef, raw json.RawMessage) {
	if s.Part == "result" {
		bundle.Results[s.Method] = raw
	} else {
		bundle.Requests[s.Method][*s.Parameter].Schema = raw
	}
}
func applyPatch(raw, patch json.RawMessage) (json.RawMessage, error) {
	p, err := jsonpatch.DecodePatch(patch)
	if err != nil {
		return nil, err
	}
	options := jsonpatch.NewApplyOptions()
	options.SupportNegativeIndices = false
	options.AllowMissingPathOnRemove = false
	options.EnsurePathExistsOnAdd = false
	options.EscapeHTML = false
	return p.ApplyWithOptions(raw, options)
}
func applyStructural(bundle *Bundle, e erratum) AppliedTarget {
	s := e.Target.ref()
	result := AppliedTarget{Source: s, Status: "review-required"}
	raw, err := rootSchema(*bundle, s)
	if err != nil {
		result.Detail = err.Error()
		return result
	}
	result.BeforeSHA256 = digest(raw)
	if guardsMatch(raw, e.After) {
		result.Status = "already-satisfied"
		result.AfterSHA256 = result.BeforeSHA256
		return result
	}
	if !guardsMatch(raw, e.Before) {
		result.Detail = "local before/after guards do not match"
		return result
	}
	output, err := applyPatch(raw, e.Patch)
	if err != nil {
		result.Detail = "apply RFC 6902 patch: " + err.Error()
		return result
	}
	if sameJSON(raw, output) {
		result.Detail = "patch does not change the schema document"
		return result
	}
	if !guardsMatch(output, e.After) {
		result.Detail = "patch violates after guards"
		return result
	}
	result.Status = "applicable"
	result.AfterSHA256 = digest(output)
	result.ChangedPointers, _ = overlayWrites(e)
	writeRoot(bundle, s, output)
	return result
}
func applyDataTarget(bundle *Bundle, e erratum, target dataTarget) AppliedTarget {
	result := AppliedTarget{Source: target.Source, Status: "review-required"}
	root, rootErr := rootSchema(*bundle, target.Source)
	if rootErr == nil {
		result.InputRootSHA256 = digest(root)
	}
	raw, err := SelectSchema(*bundle, SchemaSelection{Source: target.Source})
	if err != nil {
		result.Detail = err.Error()
		return result
	}
	result.BeforeSHA256 = digest(raw)
	var node map[string]json.RawMessage
	if err := json.Unmarshal(raw, &node); err != nil {
		result.Detail = err.Error()
		return result
	}
	var pattern string
	if err := json.Unmarshal(node["pattern"], &pattern); err != nil {
		result.Detail = "invalid DATA pattern: " + err.Error()
		return result
	}
	delete(node, "pattern")
	identity, _ := json.Marshal(node)
	if !sameJSON(identity, target.Identity) {
		result.Detail = "local DATA node identity changed"
		return result
	}
	if pattern == e.ToPattern {
		result.Status = "already-satisfied"
		result.AfterSHA256 = result.BeforeSHA256
		result.OutputRootSHA256 = result.InputRootSHA256
		return result
	}
	if pattern != e.FromPattern {
		result.Detail = "DATA fromPattern/toPattern does not match"
		return result
	}
	patch, _ := json.Marshal([]map[string]any{{"op": "test", "path": target.Source.Pointer + "/pattern", "value": e.FromPattern}, {"op": "replace", "path": target.Source.Pointer + "/pattern", "value": e.ToPattern}})
	if rootErr != nil {
		result.Detail = rootErr.Error()
		return result
	}
	result.InputRootSHA256 = digest(root)
	output, err := applyPatch(root, patch)
	if err != nil {
		result.Detail = err.Error()
		return result
	}
	result.OutputRootSHA256 = digest(output)
	writeRoot(bundle, target.Source, output)
	next, _ := SelectSchema(*bundle, SchemaSelection{Source: target.Source})
	result.Status = "applicable"
	result.AfterSHA256 = digest(next)
	result.ChangedPointers = []string{target.Source.Path() + "/pattern"}
	return result
}
func guardsMatch(raw json.RawMessage, guards []schemaGuard) bool {
	for _, g := range guards {
		value, err := valueAtPointer(raw, g.Pointer)
		if g.Absent {
			if err == nil {
				return false
			}
		} else if err != nil || !sameJSON(value, g.Value) {
			return false
		}
	}
	return true
}
func valueAtPointer(raw json.RawMessage, pointer string) (json.RawMessage, error) {
	tokens, err := pointerTokens(pointer)
	if err != nil {
		return nil, err
	}
	for _, token := range tokens {
		trim := bytes.TrimSpace(raw)
		if len(trim) == 0 {
			return nil, fmt.Errorf("missing pointer value")
		}
		switch trim[0] {
		case '{':
			var o map[string]json.RawMessage
			if err := json.Unmarshal(raw, &o); err != nil {
				return nil, err
			}
			var ok bool
			raw, ok = o[token]
			if !ok {
				return nil, fmt.Errorf("missing member %s", token)
			}
		case '[':
			var a []json.RawMessage
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, err
			}
			i, err := strconv.Atoi(token)
			if err != nil || i < 0 || i >= len(a) || token != strconv.Itoa(i) {
				return nil, fmt.Errorf("invalid array index %s", token)
			}
			raw = a[i]
		default:
			return nil, fmt.Errorf("non-container pointer")
		}
	}
	return raw, nil
}
func sameJSON(a, b []byte) bool {
	var x, y any
	da := json.NewDecoder(bytes.NewReader(a))
	da.UseNumber()
	db := json.NewDecoder(bytes.NewReader(b))
	db.UseNumber()
	return da.Decode(&x) == nil && db.Decode(&y) == nil && reflect.DeepEqual(x, y)
}
func digest(raw []byte) string { return fmt.Sprintf("%x", sha256.Sum256(raw)) }
func isDigest(value string, size int) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == size && value == strings.ToLower(value)
}
