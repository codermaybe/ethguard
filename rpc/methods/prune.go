package methods

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"ethguard/internal/spec"
	"ethguard/rpc"
)

// MaskNode is a whitelist tree compiled from the official execution-apis
// result contracts (internal/spec/methods.json). It describes JSON topology
// only: no business types, no requiredness, no value validation. Requiredness
// stays fork-aware and hand-written in the object slices; this tree decides
// only which fields participate in comparison.
type MaskNode struct {
	// Properties whitelists the retained subfields of an object (key ->
	// subtree). A nil map on a non-wildcard node drops the whole object.
	Properties map[string]*MaskNode

	// Items constrains every element of an array (for example the logs of a
	// receipt). A nil Items on a non-wildcard node keeps scalar arrays such
	// as topics untouched.
	Items *MaskNode

	// Wildcard passes everything below untouched: scalar results, open
	// objects, and branches the spec leaves unconstrained.
	Wildcard bool
}

// Prune returns data with every non-whitelisted field removed, following the
// whitelist (mask.Properties) rather than the data keys: even thousands of
// injected vendor fields cost only the whitelisted lookups. Unknown shapes
// degrade to passthrough, never to over-pruning.
func Prune(data any, mask *MaskNode) any {
	if data == nil || mask == nil {
		return nil
	}
	if mask.Wildcard {
		return data
	}

	switch v := data.(type) {
	case map[string]any:
		if mask.Properties == nil {
			return nil
		}
		cleaned := make(map[string]any, len(mask.Properties))
		for allowedKey, childMask := range mask.Properties {
			if val, exists := v[allowedKey]; exists {
				if prunedVal := Prune(val, childMask); prunedVal != nil {
					cleaned[allowedKey] = prunedVal
				}
			}
		}
		return cleaned

	case []any:
		if mask.Items == nil {
			return v
		}
		cleanedArr := make([]any, 0, len(v))
		for _, item := range v {
			if prunedItem := Prune(item, mask.Items); prunedItem != nil {
				cleanedArr = append(cleanedArr, prunedItem)
			}
		}
		return cleanedArr

	default:
		return v
	}
}

// --- Mask compiler: official result contracts -> whitelist trees ---
//
// Composition (oneOf/anyOf/allOf) merges by union over the object/array
// branches. Bare-primitive and null branches contribute nothing: scalars pass
// through pruning untouched at runtime, and absence is handled by the hand
// admission gates. A concrete mask always wins over a wildcard leaf for the
// same reason. The bundle is fully $ref-resolved, so a surviving $ref is a
// corrupt bundle and fails loudly instead of degrading silently.

func mergeMasks(a, b *MaskNode) *MaskNode {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.Wildcard && len(a.Properties) == 0 && a.Items == nil {
		return b
	}
	if b.Wildcard && len(b.Properties) == 0 && b.Items == nil {
		return a
	}
	out := &MaskNode{Properties: map[string]*MaskNode{}}
	for k, v := range a.Properties {
		out.Properties[k] = v
	}
	for k, v := range b.Properties {
		if prev, ok := out.Properties[k]; ok {
			out.Properties[k] = mergeMasks(prev, v)
		} else {
			out.Properties[k] = v
		}
	}
	out.Items = mergeMasks(a.Items, b.Items)
	if len(out.Properties) == 0 && out.Items == nil {
		return &MaskNode{Wildcard: true}
	}
	return out
}

func asSpecMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func asSpecList(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	return nil
}

// buildMask compiles one resolved schema node into a whitelist tree,
// recursing through composition at any depth.
func buildMask(node any) (*MaskNode, error) {
	m := asSpecMap(node)
	if m == nil {
		return &MaskNode{Wildcard: true}, nil
	}
	if ref, ok := m["$ref"].(string); ok {
		return nil, fmt.Errorf("spec bundle: unresolved $ref %q", ref)
	}
	var out *MaskNode
	t, _ := m["type"].(string)
	switch {
	case t == "null":
		// Absence: handled by admission gates, not pruning. Contributes nothing.
	case t == "object" || m["properties"] != nil:
		sub := &MaskNode{Properties: map[string]*MaskNode{}}
		for name, prop := range asSpecMap(m["properties"]) {
			built, err := buildMask(prop)
			if err != nil {
				return nil, err
			}
			sub.Properties[name] = built
		}
		if len(sub.Properties) == 0 {
			sub.Wildcard = true // open object: constrain nothing
		}
		out = mergeMasks(out, sub)
	case t == "array" || m["items"] != nil:
		sub := &MaskNode{}
		if items, ok := m["items"]; ok {
			built, err := buildMask(items)
			if err != nil {
				return nil, err
			}
			sub.Items = built
		} else {
			sub.Items = &MaskNode{Wildcard: true}
		}
		out = mergeMasks(out, sub)
	default:
		// Bare primitive (or typeless) node: scalars pass through pruning
		// untouched, so only composition children below may contribute.
	}
	for _, k := range []string{"oneOf", "anyOf", "allOf"} {
		for _, item := range asSpecList(m[k]) {
			built, err := buildMask(item)
			if err != nil {
				return nil, err
			}
			out = mergeMasks(out, built)
		}
	}
	if out == nil {
		return &MaskNode{Wildcard: true}, nil
	}
	return out, nil
}

// --- Bundle loading: embed -> compile once, fail loudly on skew ---

var (
	masksOnce sync.Once
	masksMap  map[string]*MaskNode
	masksErr  error
)

// methodMasks compiles the vendored bundle into per-method whitelist trees
// exactly once. It also interlocks the two version stamps: the methods.json
// self-description must equal the VERSION pin, or the bundle is skewed.
func methodMasks() (map[string]*MaskNode, error) {
	masksOnce.Do(func() {
		masksMap, masksErr = compileBundle()
	})
	return masksMap, masksErr
}

func compileBundle() (map[string]*MaskNode, error) {
	var bundle struct {
		Version string         `json:"version"`
		Results map[string]any `json:"results"`
	}
	if err := json.Unmarshal(spec.MethodsJSON(), &bundle); err != nil {
		return nil, fmt.Errorf("spec bundle: %w", err)
	}
	if bundle.Version != spec.Version() {
		return nil, fmt.Errorf("spec bundle: methods.json version %q != VERSION pin %q",
			bundle.Version, spec.Version())
	}
	out := make(map[string]*MaskNode, len(bundle.Results))
	names := make([]string, 0, len(bundle.Results))
	for name := range bundle.Results {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		built, err := buildMask(bundle.Results[name])
		if err != nil {
			return nil, fmt.Errorf("spec bundle: method %q: %w", name, err)
		}
		out[name] = built
	}
	return out, nil
}

// PruneMethod prunes one raw response through the named method's whitelist
// tree and returns the canonical comparison key: pruned, hex-canonicalized,
// and marshalled. The raw value keeps its full fidelity for admission gates
// and typed decoding; only the comparison key is pruned. Unknown methods are
// a configuration error, never a silent passthrough.
func PruneMethod(method string, raw json.RawMessage) (string, error) {
	masks, err := methodMasks()
	if err != nil {
		return "", err
	}
	mask, ok := masks[method]
	if !ok {
		return "", fmt.Errorf("%w: no spec mask for method %q", rpc.ErrInvalidRequest, method)
	}
	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		return "", err
	}
	pruned := Prune(data, mask)
	canonicalizeHex(&pruned)
	encoded, err := json.Marshal(pruned)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
