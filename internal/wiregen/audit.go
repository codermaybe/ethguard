package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/codermaybe/ethguard/internal/executionapi"
)

type auditRoot struct {
	Path   string `json:"path"`
	SHA256 string `json:"effectiveSchemaSHA256"`
}
type typeAudit struct {
	Basis   string       `json:"basis"`
	Name    string       `json:"name"`
	Kind    string       `json:"kind"`
	Target  string       `json:"aliasTarget,omitempty"`
	Sources []string     `json:"sources"`
	Fields  []fieldAudit `json:"fields,omitempty"`
}
type fieldAudit struct {
	JSON     string `json:"json"`
	Go       string `json:"go"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
}
type rootAudit struct {
	Method      string `json:"method"`
	Source      string `json:"source"`
	Constructor string `json:"constructor"`
	ReturnType  string `json:"returnType"`
}
type constraintAudit struct {
	Path        string         `json:"path"`
	Role        string         `json:"role"`
	Constraints map[string]any `json:"constraints"`
	Guarantee   string         `json:"guarantee"`
	Model       string         `json:"publicType,omitempty"`
}
type coverageAudit struct {
	Method           string   `json:"method"`
	Upstream         bool     `json:"upstreamExists"`
	ResultCollected  bool     `json:"resultCollected"`
	RequestCollected bool     `json:"requestCollected"`
	ResultGenerated  bool     `json:"resultGenerated"`
	RequestGenerated []string `json:"requestGenerated"`
	ExclusionReason  string   `json:"exclusionReason,omitempty"`
}
type generationAudit struct {
	DataExceptions []executionapi.DataException   `json:"dataExceptions"`
	Source         executionapi.SourceMetadata    `json:"source"`
	Inventory      []executionapi.InventoryMethod `json:"upstreamInventory"`
	Coverage       []coverageAudit                `json:"coverage"`

	FormatVersion      int                            `json:"formatVersion"`
	Version            string                         `json:"version"`
	ManifestSHA256     string                         `json:"manifestSHA256"`
	Errata             []executionapi.AppliedErratum  `json:"errata"`
	RequestRoots       []executionapi.SchemaSelection `json:"requestRoots"`
	Roots              []auditRoot                    `json:"roots"`
	Scalars            []scalarPolicy                 `json:"scalarPolicies"`
	Usages             []schemaPolicy                 `json:"schemaPolicies"`
	Types              []typeAudit                    `json:"publicTypes"`
	Results            []rootAudit                    `json:"resultConstructors"`
	Constraints        []constraintAudit              `json:"schemaConstraints"`
	Exclusions         map[string]string              `json:"excludedResultMethods"`
	RuntimeConsumption string                         `json:"runtimeConsumption"`
	GeneratedSHA256    string                         `json:"generatedSHA256"`
}

func writeGenerationAudit(bundle executionapi.Bundle, reg *registry, roots map[string]*ref, generated, target string) error {
	audit, err := buildGenerationAudit(bundle, reg, roots)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(generated)
	if err != nil {
		return err
	}
	audit.GeneratedSHA256 = fmt.Sprintf("%x", sha256.Sum256(raw))
	raw, err = json.MarshalIndent(audit, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(target, append(raw, '\n'), 0644)
}

func buildGenerationAudit(bundle executionapi.Bundle, reg *registry, roots map[string]*ref) (generationAudit, error) {
	scalars, err := reviewedScalarPolicies(reg.manifest)
	if err != nil {
		return generationAudit{}, err
	}
	audit := generationAudit{DataExceptions: bundle.DataExceptions, Source: bundle.Source, Inventory: bundle.Inventory, FormatVersion: 2, Version: bundle.Version, ManifestSHA256: reg.manifest.SHA256, Errata: bundle.Errata, RequestRoots: reg.manifest.RequestRoots, Scalars: scalars, RuntimeConsumption: "Not inferred by generation; verified by method consumer contract tests", Exclusions: map[string]string{}}
	byName := map[string]*typeAudit{}
	for _, n := range natives {
		byName[n.name] = &typeAudit{Basis: "compiler scalar library; exact matching schema usages listed separately", Name: n.name, Kind: "scalar", Sources: []string{}}
	}
	for _, m := range reg.objects {
		entry := &typeAudit{Basis: "effective schema compilation", Name: m.name, Kind: "object", Sources: []string{}}
		for _, f := range m.fields {
			entry.Fields = append(entry.Fields, fieldAudit{f.json, f.goName, fmt.Sprintf("%#v", fieldTypeCode(f)), f.req})
		}
		if m.retainAdditional {
			entry.Fields = append(entry.Fields, fieldAudit{"<additional members>", "AdditionalProperties", "map[string]json.RawMessage", false})
		}
		byName[m.name] = entry
	}
	for _, m := range reg.unionList {
		entry := &typeAudit{Basis: "effective schema compilation", Name: m.name, Kind: m.kind, Sources: []string{}}
		for _, b := range m.branches {
			entry.Fields = append(entry.Fields, fieldAudit{"<branch " + itoa(b.index) + ">", b.goName, "*" + fmt.Sprintf("%#v", typeCode(b.ref)), false})
		}
		byName[m.name] = entry
	}
	for _, m := range reg.maps {
		byName[m.name] = &typeAudit{Basis: "effective schema compilation", Name: m.name, Kind: "dictionary", Sources: []string{}}
	}
	for _, selection := range reg.manifest.Selections() {
		raw, err := executionapi.SelectSchema(bundle, selection)
		if err != nil {
			return generationAudit{}, err
		}
		n, err := parseSchema(raw)
		if err != nil {
			return generationAudit{}, err
		}
		path := selection.Source.Path()
		role := selection.Source.Part
		audit.Roots = append(audit.Roots, auditRoot{path, fmt.Sprintf("%x", sha256.Sum256(raw))})
		collectSchemaPolicies(n, path, role, false, false, &audit.Usages)
		reg.collectConstraints(n, nil, path, role, false, &audit.Constraints, byName)
	}
	for alias, target := range reg.manifest.Aliases {
		for reg.manifest.Aliases[target] != "" {
			target = reg.manifest.Aliases[target]
		}
		if byName[target] == nil {
			return generationAudit{}, fmt.Errorf("audit: missing alias target %s", target)
		}
		byName[alias] = &typeAudit{Basis: "manifest alias to compiled type", Name: alias, Kind: "alias", Target: reg.manifest.Aliases[alias], Sources: append([]string{}, byName[target].Sources...)}
	}
	for _, entry := range byName {
		sort.Strings(entry.Sources)
		audit.Types = append(audit.Types, *entry)
	}
	for method, r := range roots {
		audit.Results = append(audit.Results, rootAudit{method, method + ".result", methodShimName(method), fmt.Sprintf("%#v", typeCode(r))})
	}
	for _, method := range bundle.Inventory {
		_, resultCollected := bundle.Results[method.Name]
		_, requestCollected := bundle.Requests[method.Name]
		entry := coverageAudit{Method: method.Name, Upstream: true, ResultCollected: resultCollected, RequestCollected: requestCollected, ResultGenerated: roots[method.Name] != nil, RequestGenerated: []string{}}
		for _, root := range reg.manifest.RequestRoots {
			if root.Source.Method == method.Name {
				entry.RequestGenerated = append(entry.RequestGenerated, root.Source.Path())
			}
		}
		sort.Strings(entry.RequestGenerated)
		if !entry.ResultGenerated {
			entry.ExclusionReason = reg.manifest.DefaultExclusionReason
			audit.Exclusions[method.Name] = entry.ExclusionReason
		}
		audit.Coverage = append(audit.Coverage, entry)
	}
	sort.Slice(audit.Coverage, func(i, j int) bool { return audit.Coverage[i].Method < audit.Coverage[j].Method })
	sort.Slice(audit.Types, func(i, j int) bool { return audit.Types[i].Name < audit.Types[j].Name })
	sort.Slice(audit.Results, func(i, j int) bool { return audit.Results[i].Method < audit.Results[j].Method })
	sort.Slice(audit.Roots, func(i, j int) bool { return audit.Roots[i].Path < audit.Roots[j].Path })
	sort.Slice(audit.Usages, func(i, j int) bool { return audit.Usages[i].Path < audit.Usages[j].Path })
	sort.Slice(audit.Constraints, func(i, j int) bool { return audit.Constraints[i].Path < audit.Constraints[j].Path })
	return audit, nil
}

// Audit visits the effective source tree independently of deduplication. A
// shared type retains every contributing path, including merged branches.
func (reg *registry) collectConstraints(n, envelope *schema, path, role string, preserve bool, out *[]constraintAudit, types map[string]*typeAudit) {
	previousRequest, previousPreserve := reg.strictRequests, reg.preserveStrings
	reg.strictRequests, reg.preserveStrings = role == "request", preserve
	defer func() { reg.strictRequests, reg.preserveStrings = previousRequest, previousPreserve }()
	name := ""
	modelNode := n
	if envelope != nil {
		modelNode = mergeBranch(envelope, n)
	}
	key := reg.modelFingerprint(modelNode)
	if m := reg.byFP[key]; m != nil {
		name = m.name
	}
	if m := reg.unions[key]; m != nil {
		name = m.name
	}
	if m := reg.mapByFP[key]; m != nil {
		name = m.name
	}
	if n.typeVal == "string" {
		name = stringNative(n, preserve)
	}
	if n.typeVal == "number" {
		name = "Number"
	}
	if entry := types[name]; entry != nil {
		entry.Sources = appendMethod(entry.Sources, path)
	}
	constraints := canonNode(n)
	for _, key := range []string{"properties", "patternProperties", "items", "branches"} {
		delete(constraints, key)
	}
	if len(n.properties) > 0 {
		names := []string{}
		for _, p := range n.properties {
			names = append(names, p.name)
		}
		constraints["properties"] = names
	}
	if len(n.patternProperties) > 0 {
		names := []string{}
		for _, p := range n.patternProperties {
			names = append(names, p.name)
		}
		constraints["patternProperties"] = names
	}
	if n.items != nil {
		constraints["items"] = schemaChild(path, "items")
	}
	if len(n.branches) > 0 {
		paths := []string{}
		for i := range n.branches {
			paths = append(paths, schemaChild(path, n.compKind, itoa(i)))
		}
		constraints[n.compKind] = paths
	}
	*out = append(*out, constraintAudit{path, role, constraints, "generated constructor; direct Go assignment and MarshalJSON do not revalidate schema", name})
	for _, p := range n.properties {
		reg.collectConstraints(p.schema, nil, schemaChild(path, "properties", p.name), role, preserve, out, types)
	}
	for _, p := range n.patternProperties {
		reg.collectConstraints(p.schema, nil, schemaChild(path, "patternProperties", p.name), role, preserve, out, types)
	}
	if n.items != nil {
		reg.collectConstraints(n.items, nil, schemaChild(path, "items"), role, preserve || n.uniqueItems, out, types)
	}
	for i, b := range n.branches {
		var envelope *schema
		if len(n.properties) > 0 {
			envelope = n
		}
		reg.collectConstraints(b, envelope, schemaChild(path, n.compKind, itoa(i)), role, preserve, out, types)
	}
}
