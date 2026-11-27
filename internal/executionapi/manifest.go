package executionapi

// Manifest is build-time policy. Nothing in the runtime Read/ReadOriginal
// path loads it, and this file has no package initializer or embedded policy.
import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"go/token"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/codermaybe/ethguard/internal/jsonsafe"
)

type Manifest struct {
	FormatVersion            int                 `json:"formatVersion"`
	ResultMethods            []string            `json:"resultMethods"`
	RequestInventoryPrefixes []string            `json:"requestInventoryPrefixes"`
	DefaultExclusionReason   string              `json:"defaultExclusionReason"`
	RequestRoots             []SchemaSelection   `json:"requestRoots"`
	Names                    []ModelNameRule     `json:"names"`
	TitleNames               map[string]string   `json:"titleNames"`
	Aliases                  map[string]string   `json:"aliases"`
	FieldOrder               map[string][]string `json:"fieldOrder"`
	ScalarPolicies           map[string]string   `json:"scalarPolicies"`
	SHA256                   string              `json:"-"`
}

// SchemaRef identifies a schema node, not a generated Go model. Pointer is
// an RFC 6901 pointer relative to a result or one request parameter schema.
type SchemaRef struct {
	Method    string `json:"method"`
	Part      string `json:"part"`
	Parameter *int   `json:"parameter,omitempty"`
	Pointer   string `json:"schemaPointer,omitempty"`
}

type SchemaSelection struct {
	Source         SchemaRef `json:"source"`
	ExpectedSHA256 string    `json:"selectedCompactSchemaSHA256,omitempty"`
}

type ModelNameRule struct {
	Source SchemaRef `json:"source"`
	Name   string    `json:"name"`
}

func (s SchemaRef) Path() string {
	if s.Part == "request" && s.Parameter != nil {
		return fmt.Sprintf("%s.params[%d]%s", s.Method, *s.Parameter, s.Pointer)
	}
	return s.Method + "." + s.Part + s.Pointer
}

func (s SchemaRef) Validate() error {
	if s.Method == "" || strings.ContainsAny(s.Method, "/.[] \t\r\n") {
		return fmt.Errorf("invalid method %q", s.Method)
	}
	switch s.Part {
	case "result":
		if s.Parameter != nil {
			return fmt.Errorf("result reference cannot select parameter")
		}
	case "request":
		if s.Parameter == nil || *s.Parameter < 0 {
			return fmt.Errorf("request reference requires nonnegative parameter")
		}
	default:
		return fmt.Errorf("invalid schema part %q", s.Part)
	}
	_, err := pointerTokens(s.Pointer)
	return err
}

func LoadManifest(path string) (Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("manifest: %w", err)
	}
	return ParseManifest(raw)
}

func ParseManifest(raw []byte) (Manifest, error) {
	if err := jsonsafe.Validate(raw); err != nil {
		return Manifest{}, fmt.Errorf("manifest JSON: %w", err)
	}
	var m Manifest
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("manifest: %w", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return Manifest{}, fmt.Errorf("manifest: trailing content")
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, fmt.Errorf("manifest: %w", err)
	}
	m.SHA256 = fmt.Sprintf("%x", sha256.Sum256(raw))
	return m, nil
}

func exportedIdentifier(name string) bool {
	r, _ := utf8.DecodeRuneInString(name)
	return token.IsIdentifier(name) && unicode.IsUpper(r)
}

func (m Manifest) Validate() error {
	if m.FormatVersion != 1 {
		return fmt.Errorf("unsupported formatVersion %d", m.FormatVersion)
	}
	if len(m.ResultMethods) == 0 || len(m.RequestInventoryPrefixes) == 0 || strings.TrimSpace(m.DefaultExclusionReason) == "" {
		return fmt.Errorf("missing result selection, request inventory scope or exclusion reason")
	}
	seen := map[string]bool{}
	for _, method := range m.ResultMethods {
		if err := (SchemaRef{Method: method, Part: "result"}).Validate(); err != nil {
			return err
		}
		if seen[method] {
			return fmt.Errorf("duplicate result method %q", method)
		}
		seen[method] = true
	}
	seen = map[string]bool{}
	for _, prefix := range m.RequestInventoryPrefixes {
		if prefix == "" || !strings.HasSuffix(prefix, "_") || strings.ContainsAny(prefix, "/.[] \t\r\n") || seen[prefix] {
			return fmt.Errorf("invalid or duplicate request prefix %q", prefix)
		}
		seen[prefix] = true
	}
	seen = map[string]bool{}
	for _, root := range m.RequestRoots {
		if root.Source.Part != "request" {
			return fmt.Errorf("requestRoots entry must reference request")
		}
		if err := root.Source.Validate(); err != nil {
			return err
		}
		if !m.IncludesRequest(root.Source.Method) {
			return fmt.Errorf("request root %s outside inventory scope", root.Source.Path())
		}
		if seen[root.Source.Path()] {
			return fmt.Errorf("duplicate request root %s", root.Source.Path())
		}
		seen[root.Source.Path()] = true
		if root.ExpectedSHA256 != "" && !isDigest(root.ExpectedSHA256, 32) {
			return fmt.Errorf("invalid selected node SHA-256 at %s", root.Source.Path())
		}
		if root.Source.Pointer != "" && root.ExpectedSHA256 == "" {
			return fmt.Errorf("selected subtree requires identity digest at %s", root.Source.Path())
		}
	}
	seen = map[string]bool{}
	for _, rule := range m.Names {
		if err := rule.Source.Validate(); err != nil {
			return err
		}
		if !exportedIdentifier(rule.Name) || seen[rule.Source.Path()] {
			return fmt.Errorf("invalid or duplicate model name rule %s -> %q", rule.Source.Path(), rule.Name)
		}
		seen[rule.Source.Path()] = true
	}
	for title, name := range m.TitleNames {
		if strings.TrimSpace(title) == "" || !exportedIdentifier(name) {
			return fmt.Errorf("invalid title name %q -> %q", title, name)
		}
	}
	for alias, target := range m.Aliases {
		if !exportedIdentifier(alias) || !exportedIdentifier(target) {
			return fmt.Errorf("invalid alias %q -> %q", alias, target)
		}
		visited := map[string]bool{}
		for next := alias; next != ""; next = m.Aliases[next] {
			if visited[next] {
				return fmt.Errorf("alias cycle at %q", next)
			}
			visited[next] = true
		}
	}
	for name, fields := range m.FieldOrder {
		if !exportedIdentifier(name) || len(fields) == 0 {
			return fmt.Errorf("invalid field order target %q", name)
		}
		seen := map[string]bool{}
		for _, field := range fields {
			if field == "" || seen[field] {
				return fmt.Errorf("invalid or duplicate field %q in %s order", field, name)
			}
			seen[field] = true
		}
	}
	for name, policy := range m.ScalarPolicies {
		if !exportedIdentifier(name) || policy == "" {
			return fmt.Errorf("invalid scalar policy %q -> %q", name, policy)
		}
	}
	return nil
}

func (m Manifest) IncludesRequest(method string) bool {
	for _, prefix := range m.RequestInventoryPrefixes {
		if strings.HasPrefix(method, prefix) {
			return true
		}
	}
	return false
}

func (m Manifest) Selections() []SchemaSelection {
	methods := append([]string(nil), m.ResultMethods...)
	sort.Strings(methods)
	result := make([]SchemaSelection, 0, len(methods)+len(m.RequestRoots))
	for _, method := range methods {
		result = append(result, SchemaSelection{Source: SchemaRef{Method: method, Part: "result"}})
	}
	return append(result, m.RequestRoots...)
}

func SelectSchema(bundle Bundle, selection SchemaSelection) (json.RawMessage, error) {
	s := selection.Source
	if err := s.Validate(); err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if s.Part == "result" {
		raw = bundle.Results[s.Method]
	} else {
		params := bundle.Requests[s.Method]
		if *s.Parameter >= len(params) {
			return nil, fmt.Errorf("schema reference missing %s", s.Path())
		}
		raw = params[*s.Parameter].Schema
	}
	selected, err := SchemaAtPointer(raw, s.Pointer)
	if err != nil {
		return nil, fmt.Errorf("schema %s: %w", s.Path(), err)
	}
	if selection.ExpectedSHA256 != "" {
		var compact bytes.Buffer
		if err := json.Compact(&compact, selected); err != nil {
			return nil, err
		}
		got := fmt.Sprintf("%x", sha256.Sum256(compact.Bytes()))
		if got != selection.ExpectedSHA256 {
			return nil, fmt.Errorf("selected node contract changed at %s: compact SHA-256 %s != %s", s.Path(), got, selection.ExpectedSHA256)
		}
	}
	return selected, nil
}

func pointerTokens(pointer string) ([]string, error) {
	if pointer == "" {
		return nil, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, fmt.Errorf("schema pointer must be empty or start with /")
	}
	tokens := strings.Split(pointer[1:], "/")
	for j, encoded := range tokens {
		var decoded strings.Builder
		for i := 0; i < len(encoded); i++ {
			if encoded[i] != '~' {
				decoded.WriteByte(encoded[i])
				continue
			}
			i++
			if i >= len(encoded) || (encoded[i] != '0' && encoded[i] != '1') {
				return nil, fmt.Errorf("invalid JSON Pointer escape")
			}
			if encoded[i] == '0' {
				decoded.WriteByte('~')
			} else {
				decoded.WriteByte('/')
			}
		}
		tokens[j] = decoded.String()
	}
	return tokens, nil
}

func EscapePointerToken(token string) string {
	return strings.ReplaceAll(strings.ReplaceAll(token, "~", "~0"), "/", "~1")
}

func SchemaAtPointer(raw json.RawMessage, pointer string) (json.RawMessage, error) {
	tokens, err := pointerTokens(pointer)
	if err != nil {
		return nil, err
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("invalid schema JSON")
	}
	for _, key := range tokens {
		switch bytes.TrimSpace(raw)[0] {
		case '{':
			var object map[string]json.RawMessage
			if err := json.Unmarshal(raw, &object); err != nil {
				return nil, err
			}
			var ok bool
			raw, ok = object[key]
			if !ok {
				return nil, fmt.Errorf("schema pointer member %q does not exist", key)
			}
		case '[':
			var array []json.RawMessage
			if err := json.Unmarshal(raw, &array); err != nil {
				return nil, err
			}
			index, err := strconv.Atoi(key)
			if err != nil || key != strconv.Itoa(index) || index < 0 || index >= len(array) {
				return nil, fmt.Errorf("invalid schema pointer array index %q", key)
			}
			raw = array[index]
		default:
			return nil, fmt.Errorf("schema pointer traverses a scalar")
		}
	}
	if len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' {
		return nil, fmt.Errorf("selected schema must be an object")
	}
	return raw, nil
}
