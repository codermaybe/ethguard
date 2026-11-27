package rpc

import (
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/codermaybe/ethguard/internal/jsonsafe"
)

func TestNewExtensionSetWithoutAcceptedFieldsValidatesCoreOnly(t *testing.T) {
	set, err := NewExtensionSet(json.RawMessage(`{"b":2,"a":1}`), nil)
	if err != nil {
		t.Fatalf("NewExtensionSet() error = %v", err)
	}
	if set.Fields != nil {
		t.Fatalf("fields = %#v, want nil", set.Fields)
	}
	set, err = NewExtensionSet(json.RawMessage(`null`), []FieldObservation{})
	if err != nil || set.Fields != nil {
		t.Fatalf("empty accepted slice = %#v, %v; want nil fields", set, err)
	}
}

func TestExtensionCoreFingerprintGoldenValues(t *testing.T) {
	// These hashes were computed from the listed literal canonical bytes,
	// independently of CanonicalJSON and either extension constructor.
	for _, tt := range []struct {
		name        string
		inputs      []string
		fingerprint string
	}{
		{"one", []string{`1`, `1.0`, `1e0`}, "6b86b273ff34fce19d6b804eff5a3f5747ada4eaa22f1d49c01e52ddb7875b4b"},
		{"different core", []string{`2`}, "d4735e3a265e16eee03f59718b9b5d03019c07d8b6c51f90da3a666eec13ab35"},
		{"ordered object", []string{`{"a":1,"b":2}`, ` { "b": 2, "a": 1.0 } `}, "43258cff783fe7036d8a43033f830adfc60ec037382473548ac742b888292777"},
		{"HTML escaping", []string{`"<>&"`, `"\u003c\u003e\u0026"`}, "386d184405c75e79dd1e915ce0109a04c5c4711b874b1c46ec54715bbcdab90f"},
		// The existing any decode rounds this integer to 9007199254740992.
		{"float64 integer rounding", []string{`9007199254740993`, `9007199254740992`}, "c681da39d7273a6a24c15c9cac3a75526ff2ecf8ba4ee60346a0c70c8163bdb2"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, raw := range tt.inputs {
				fingerprint, err := CoreObjectFingerprint(json.RawMessage(raw))
				if err != nil || fingerprint != tt.fingerprint {
					t.Fatalf("CoreObjectFingerprint(%s) = %q, %v; want %q", raw, fingerprint, err, tt.fingerprint)
				}
				accepted := []FieldObservation{{Path: "/vendor", Value: json.RawMessage(`"v"`), Sources: []SourceRef{{ID: "a"}, {ID: "b"}}}}
				set, err := NewExtensionSet(json.RawMessage(raw), accepted)
				want := ExtensionSet{Fields: []ExtensionField{{Kind: AcceptedExtension, Path: "/vendor", Value: json.RawMessage(`"v"`), Sources: []SourceRef{{ID: "a"}, {ID: "b"}}, CoreObjectFingerprint: tt.fingerprint}}}
				if err != nil || !reflect.DeepEqual(set, want) {
					t.Fatalf("NewExtensionSet(%s) = %#v, %v; want %#v", raw, set, err, want)
				}
			}
		})
	}
}

func TestExtensionCoreErrorsWithAndWithoutFields(t *testing.T) {
	for _, tt := range []struct {
		name string
		raw  string
		kind string
		want error
	}{
		{name: "empty", raw: "", want: io.EOF},
		{name: "truncated", raw: `{"broken":`, want: io.EOF},
		{name: "syntax", raw: `?`, kind: "syntax"},
		{name: "float64 overflow", raw: `1e400`, kind: "float64"},
		{name: "duplicate", raw: `{"x":1,"x":2}`, want: jsonsafe.ErrDuplicateKey},
		{name: "escaped duplicate", raw: `{"x":1,"\u0078":2}`, want: jsonsafe.ErrDuplicateKey},
		{name: "trailing", raw: `true false`, want: jsonsafe.ErrTrailingData},
		{name: "depth", raw: strings.Repeat("[", jsonsafe.MaxNestingDepth+1) + `0` + strings.Repeat("]", jsonsafe.MaxNestingDepth+1), want: jsonsafe.ErrTooDeep},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, accepted := range [][]FieldObservation{nil, {{Path: "/vendor", Value: json.RawMessage(`true`)}}} {
				set, err := NewExtensionSet(json.RawMessage(tt.raw), accepted)
				if err == nil || set.Fields != nil {
					t.Fatalf("NewExtensionSet(%q, %d fields) = %#v, %v; want empty set and error", tt.raw, len(accepted), set, err)
				}
				switch tt.kind {
				case "syntax":
					var syntax *json.SyntaxError
					if !errors.As(err, &syntax) {
						t.Fatalf("error = %T %v, want *json.SyntaxError", err, err)
					}
				case "float64":
					var overflow *json.UnmarshalTypeError
					if !errors.As(err, &overflow) || overflow.Type != reflect.TypeFor[float64]() || overflow.Value != "number 1e400" {
						t.Fatalf("error = %T %v, want float64 overflow for number 1e400", err, err)
					}
				default:
					if !errors.Is(err, tt.want) {
						t.Fatalf("error = %v, want %v", err, tt.want)
					}
				}
			}
		})
	}
}
