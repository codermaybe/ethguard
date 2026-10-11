package shapeprobe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// FieldDiff is one JSON path whose leaf values are not identical across the
// responses that the existing normalizer accepted. It is an observation, not
// a judgement about which spelling should compare equal.
type FieldDiff struct {
	Path   string
	Values []FieldValue
}

// FieldValue is one source's raw JSON at a path, tied to the record sha256.
type FieldValue struct {
	SourceID string
	SHA256   string
	Raw      string
	Missing  bool
}

// DiffFields walks result JSON and lists leaf paths that differ. Containers
// are not themselves entries unless they are empty. This is not a normalizer.
func DiffFields(samples []FieldSample) ([]FieldDiff, error) {
	flat := make([]map[string]string, len(samples))
	for i, sample := range samples {
		paths := map[string]string{}
		if err := flattenJSON(sample.Result, "", paths); err != nil {
			return nil, fmt.Errorf("%s: %w", sample.SourceID, err)
		}
		flat[i] = paths
	}
	seen := map[string]bool{}
	var paths []string
	for _, pathsOf := range flat {
		for path := range pathsOf {
			if !seen[path] {
				seen[path] = true
				paths = append(paths, path)
			}
		}
	}
	sort.Strings(paths)
	var diffs []FieldDiff
	for _, path := range paths {
		values := make([]FieldValue, len(samples))
		unique := map[string]bool{}
		for i, sample := range samples {
			raw, ok := flat[i][path]
			values[i] = FieldValue{SourceID: sample.SourceID, SHA256: sample.SHA256, Raw: raw, Missing: !ok}
			if ok {
				unique[raw] = true
			} else {
				unique["\x00missing"] = true
			}
		}
		if len(unique) <= 1 {
			continue
		}
		diffs = append(diffs, FieldDiff{Path: path, Values: values})
	}
	return diffs, nil
}

// FieldSample is one normalized response's raw result.
type FieldSample struct {
	SourceID string
	SHA256   string
	Result   json.RawMessage
}

func flattenJSON(raw json.RawMessage, path string, dst map[string]string) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		dst[displayPath(path)] = string(raw)
		return nil
	}
	switch raw[0] {
	case '{':
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			return err
		}
		if len(obj) == 0 {
			dst[displayPath(path)] = "{}"
			return nil
		}
		keys := make([]string, 0, len(obj))
		for key := range obj {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if err := flattenJSON(obj[key], path+"/"+escapePointer(key), dst); err != nil {
				return err
			}
		}
		return nil
	case '[':
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return err
		}
		if len(items) == 0 {
			dst[displayPath(path)] = "[]"
			return nil
		}
		for i, item := range items {
			if err := flattenJSON(item, path+"/"+strconv.Itoa(i), dst); err != nil {
				return err
			}
		}
		return nil
	default:
		dst[displayPath(path)] = string(raw)
		return nil
	}
}

func displayPath(path string) string {
	if path == "" {
		return "/"
	}
	return path
}

func escapePointer(token string) string {
	token = strings.ReplaceAll(token, "~", "~0")
	return strings.ReplaceAll(token, "/", "~1")
}
