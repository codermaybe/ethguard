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
	"strings"

	"github.com/codermaybe/ethguard/internal/jsonsafe"
	jsonpatch "github.com/evanphx/json-patch/v5"
)

// AppliedErratum identifies the exact correction material used by consumers.
// The raw upstream version and source files retain their original identities.
type AppliedErratum struct {
	ID, File, SHA256                      string
	InputSchemaSHA256, OutputSchemaSHA256 string
}

type erratum struct {
	ID                 string          `json:"id"`
	SourceVersion      string          `json:"sourceVersion"`
	SourceCommit       string          `json:"sourceCommit"`
	Target             schemaTarget    `json:"target"`
	Reason             string          `json:"reason"`
	References         []string        `json:"references"`
	RetireWhen         string          `json:"retireWhen"`
	InputSchemaSHA256  string          `json:"inputSchemaSHA256"`
	OutputSchemaSHA256 string          `json:"outputSchemaSHA256"`
	Patch              json.RawMessage `json:"patch"`
}

type schemaTarget struct {
	Method    string `json:"method"`
	Part      string `json:"part"`
	Parameter *int   `json:"parameter,omitempty"`
}

// Only reviewed repository data reaches this function. There is no public
// option to load patches from a provider, request, or arbitrary filesystem.
func applyErrata(bundle *Bundle, sourceJSON []byte, files fs.FS) error {
	var source struct{ Version, Commit string }
	if err := json.Unmarshal(sourceJSON, &source); err != nil {
		return fmt.Errorf("errata source: %w", err)
	}
	if source.Version == "" || source.Version != bundle.Version || !isDigest(source.Commit, 20) {
		return fmt.Errorf("errata source: invalid version or commit identity")
	}
	entries, err := fs.ReadDir(files, "errata")
	if err != nil {
		return fmt.Errorf("errata inventory: %w", err)
	}
	// Commit the complete view only after every correction passes. Even a
	// failure in the last file leaves the caller's original view untouched.
	candidate := *bundle
	candidate.Results = make(map[string]json.RawMessage, len(bundle.Results))
	for key, raw := range bundle.Results {
		candidate.Results[key] = raw
	}
	candidate.Requests = make(map[string][]Parameter, len(bundle.Requests))
	for key, params := range bundle.Requests {
		candidate.Requests[key] = append([]Parameter{}, params...)
	}
	candidate.Errata = append([]AppliedErratum(nil), bundle.Errata...)
	seen := map[string]bool{}
	for _, applied := range bundle.Errata {
		seen[applied.ID] = true
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		name := "errata/" + entry.Name()
		raw, err := fs.ReadFile(files, name)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if err := jsonsafe.Validate(raw); err != nil {
			return fmt.Errorf("%s: invalid correction JSON: %w", name, err)
		}
		var e erratum
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&e); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return fmt.Errorf("%s: trailing JSON", name)
		}
		if strings.TrimSpace(e.ID) == "" || strings.TrimSpace(e.Reason) == "" || strings.TrimSpace(e.RetireWhen) == "" || len(e.References) == 0 || !isDigest(e.InputSchemaSHA256, 32) || !isDigest(e.OutputSchemaSHA256, 32) {
			return fmt.Errorf("%s: incomplete audit metadata", name)
		}
		for _, ref := range e.References {
			u, err := url.Parse(ref)
			if err != nil || u.Scheme != "https" || u.Host == "" {
				return fmt.Errorf("%s: reference must be an absolute HTTPS URL", name)
			}
		}
		if seen[e.ID] {
			return fmt.Errorf("%s: duplicate or already applied erratum %q", name, e.ID)
		}
		if e.SourceVersion != source.Version || e.SourceCommit != source.Commit {
			return fmt.Errorf("%s: source pin changed; review or retire erratum %q", name, e.ID)
		}
		if err := applySchemaPatch(&candidate, e); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		candidate.Errata = append(candidate.Errata, AppliedErratum{ID: e.ID, File: name, SHA256: digest(raw), InputSchemaSHA256: e.InputSchemaSHA256, OutputSchemaSHA256: e.OutputSchemaSHA256})
		seen[e.ID] = true
	}
	*bundle = candidate
	return nil
}

func applySchemaPatch(bundle *Bundle, e erratum) error {
	var raw json.RawMessage
	switch e.Target.Part {
	case "result":
		if e.Target.Parameter != nil {
			return fmt.Errorf("result target cannot select a parameter")
		}
		raw = bundle.Results[e.Target.Method]
	case "request":
		params := bundle.Requests[e.Target.Method]
		if e.Target.Parameter == nil || *e.Target.Parameter < 0 || *e.Target.Parameter >= len(params) {
			return fmt.Errorf("request target parameter does not exist")
		}
		raw = params[*e.Target.Parameter].Schema
	default:
		return fmt.Errorf("target part must be result or request")
	}
	if len(raw) == 0 {
		return fmt.Errorf("target schema %s does not exist", e.Target.Method)
	}
	if got := digest(raw); got != e.InputSchemaSHA256 {
		return fmt.Errorf("input schema digest %s != %s; upstream changed or correction is redundant: review or retire %q", got, e.InputSchemaSHA256, e.ID)
	}
	patch, err := jsonpatch.DecodePatch(e.Patch)
	if err != nil {
		return fmt.Errorf("decode RFC 6902 patch: %w", err)
	}
	if len(patch) == 0 {
		return fmt.Errorf("patch must contain operations")
	}
	// Keep RFC 6902 behavior: no negative indices, implicit ancestor creation,
	// or successful removal of a missing path. No package globals are changed.
	options := jsonpatch.NewApplyOptions()
	options.SupportNegativeIndices = false
	options.AllowMissingPathOnRemove = false
	options.EnsurePathExistsOnAdd = false
	options.EscapeHTML = false
	patched, err := patch.ApplyWithOptions(raw, options)
	if err != nil {
		return fmt.Errorf("apply RFC 6902 patch: %w", err)
	}
	if jsonpatch.Equal(raw, patched) {
		return fmt.Errorf("patch does not change the schema document; retire %q", e.ID)
	}
	if !bytes.HasPrefix(bytes.TrimSpace(patched), []byte("{")) {
		return fmt.Errorf("patch result must be a schema object")
	}
	if got := digest(patched); got != e.OutputSchemaSHA256 {
		return fmt.Errorf("output schema digest %s != %s", got, e.OutputSchemaSHA256)
	}
	if e.Target.Part == "result" {
		bundle.Results[e.Target.Method] = patched
	} else {
		bundle.Requests[e.Target.Method][*e.Target.Parameter].Schema = patched
	}
	return nil
}

func digest(raw []byte) string { return fmt.Sprintf("%x", sha256.Sum256(raw)) }

func isDigest(value string, size int) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == size && value == strings.ToLower(value)
}
