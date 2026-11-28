package conformance

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"reflect"
	"sort"
	"strconv"

	"github.com/codermaybe/ethguard/internal/jsonsafe"
)

// FullDiff includes both response schemas and ordered request descriptors.
// Byte identities preserve formatting and field-order evidence independently
// of the semantic changes, whose paths are literal RFC 6901 pointers.
type FullDiff struct {
	BeforeVersion string         `json:"beforeVersion"`
	AfterVersion  string         `json:"afterVersion"`
	Results       []MethodDiff   `json:"results"`
	Requests      []MethodDiff   `json:"requests"`
	Artifacts     []ArtifactDiff `json:"artifacts"`
}
type ArtifactDiff struct {
	Name         string `json:"name"`
	BeforeSHA256 string `json:"beforeSHA256"`
	AfterSHA256  string `json:"afterSHA256"`
	Changed      bool   `json:"changed"`
}

func DiffDirectories(before, after fs.FS) (FullDiff, error) {
	var inputs [4][]byte
	for i, side := range []fs.FS{before, after} {
		for j, name := range []string{"methods.json", "requests.json"} {
			raw, err := fs.ReadFile(side, name)
			if err != nil {
				return FullDiff{}, err
			}
			inputs[i*2+j] = raw
		}
	}
	return DiffComplete(inputs[0], inputs[1], inputs[2], inputs[3])
}
func DiffComplete(beforeResults, beforeRequests, afterResults, afterRequests []byte) (FullDiff, error) {
	raws := [][]byte{beforeResults, beforeRequests, afterResults, afterRequests}
	versions := make([]string, 4)
	sets := make([]map[string]json.RawMessage, 4)
	for i, raw := range raws {
		if err := jsonsafe.Validate(raw); err != nil {
			return FullDiff{}, err
		}
		var envelope struct {
			Version  string
			Results  map[string]json.RawMessage
			Requests map[string]json.RawMessage
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return FullDiff{}, err
		}
		versions[i] = envelope.Version
		sets[i] = envelope.Results
		if i%2 == 1 {
			sets[i] = envelope.Requests
		}
		if versions[i] == "" || sets[i] == nil {
			return FullDiff{}, fmt.Errorf("missing version or definitions in input %d", i)
		}
	}
	if versions[0] != versions[1] || versions[2] != versions[3] {
		return FullDiff{}, fmt.Errorf("result/request version skew")
	}
	result := FullDiff{BeforeVersion: versions[0], AfterVersion: versions[2], Results: []MethodDiff{}, Requests: []MethodDiff{}, Artifacts: []ArtifactDiff{}}
	for i, name := range []string{"methods.json", "requests.json"} {
		result.Artifacts = append(result.Artifacts, ArtifactDiff{name, fmt.Sprintf("%x", sha256.Sum256(raws[i])), fmt.Sprintf("%x", sha256.Sum256(raws[i+2])), !bytes.Equal(raws[i], raws[i+2])})
	}
	result.Results = diffRawMethods(sets[0], sets[2])
	result.Requests = diffRawMethods(sets[1], sets[3])
	return result, nil
}
func diffRawMethods(before, after map[string]json.RawMessage) []MethodDiff {
	names := map[string]bool{}
	for n := range before {
		names[n] = true
	}
	for n := range after {
		names[n] = true
	}
	keys := make([]string, 0, len(names))
	for n := range names {
		keys = append(keys, n)
	}
	sort.Strings(keys)
	out := []MethodDiff{}
	for _, name := range keys {
		a, hasA := before[name]
		b, hasB := after[name]
		entry := MethodDiff{Method: name, Added: !hasA, Removed: !hasB}
		if hasA && hasB {
			var av, bv any
			ad := json.NewDecoder(bytes.NewReader(a))
			ad.UseNumber()
			bd := json.NewDecoder(bytes.NewReader(b))
			bd.UseNumber()
			_ = ad.Decode(&av) // Both documents passed jsonsafe.Validate before indexing.
			_ = bd.Decode(&bv) // Both documents passed jsonsafe.Validate before indexing.
			entry.Changed = diffValues(av, bv, "")
			ao, bo := objectOrders(a), objectOrders(b)
			all := map[string]bool{}
			for p := range ao {
				all[p] = true
			}
			for p := range bo {
				all[p] = true
			}
			paths := make([]string, 0, len(all))
			for p := range all {
				paths = append(paths, p)
			}
			sort.Strings(paths)
			for _, p := range paths {
				if ao[p] != "" && bo[p] != "" && ao[p] != bo[p] {
					entry.Changed = append(entry.Changed, SchemaChange{Path: p, Kind: "object-order-changed", Before: ao[p], After: bo[p]})
				}
			}
		}
		if entry.Added || entry.Removed || len(entry.Changed) > 0 {
			out = append(out, entry)
		}
	}
	return out
}
func diffValues(a, b any, path string) []SchemaChange {
	if reflect.DeepEqual(a, b) {
		return nil
	}
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)
	if aok && bok {
		all := map[string]bool{}
		for k := range am {
			all[k] = true
		}
		for k := range bm {
			all[k] = true
		}
		keys := make([]string, 0, len(all))
		for k := range all {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := []SchemaChange{}
		for _, k := range keys {
			av, hasA := am[k]
			bv, hasB := bm[k]
			p := path + "/" + escape(k)
			if !hasA || !hasB {
				kind := "added"
				if !hasB {
					kind = "removed"
				}
				out = append(out, valueChange(p, kind, av, bv))
			} else {
				out = append(out, diffValues(av, bv, p)...)
			}
		}
		return out
	}
	aa, aok := a.([]any)
	ba, bok := b.([]any)
	if aok && bok {
		out := []SchemaChange{}
		n := len(aa)
		if len(ba) > n {
			n = len(ba)
		}
		for i := 0; i < n; i++ {
			p := path + "/" + strconv.Itoa(i)
			switch {
			case i >= len(aa):
				out = append(out, valueChange(p, "added", nil, ba[i]))
			case i >= len(ba):
				out = append(out, valueChange(p, "removed", aa[i], nil))
			default:
				out = append(out, diffValues(aa[i], ba[i], p)...)
			}
		}
		return out
	}
	return []SchemaChange{valueChange(path, "changed", a, b)}
}
func valueChange(path, kind string, a, b any) SchemaChange {
	ar, _ := json.Marshal(a)
	br, _ := json.Marshal(b)
	return SchemaChange{Path: path, Kind: kind, Before: string(ar), After: string(br)}
}
func objectOrders(raw []byte) map[string]string {
	out := map[string]string{}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var visit func(string)
	visit = func(path string) {
		token, err := dec.Token()
		if err != nil {
			return
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return
		}
		switch delim {
		case '{':
			keys := []string{}
			for dec.More() {
				key, _ := dec.Token()
				s := key.(string)
				keys = append(keys, s)
				visit(path + "/" + escape(s))
			}
			_, _ = dec.Token() // Closing delimiter belongs to validated JSON.
			encoded, _ := json.Marshal(keys)
			out[path] = string(encoded)
		case '[':
			for i := 0; dec.More(); i++ {
				visit(path + "/" + strconv.Itoa(i))
			}
			_, _ = dec.Token() // Closing delimiter belongs to validated JSON.
		}
	}
	visit("")
	return out
}
