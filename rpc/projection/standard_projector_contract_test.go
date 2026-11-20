package projection

import (
	"encoding/json"
	"reflect"
	"sync"
	"testing"

	"github.com/codermaybe/ethguard/rpc"
)

func TestStandardProjectionPathContracts(t *testing.T) {
	for _, tt := range []struct {
		name       string
		fields     []StandardField
		accepted   []string
		core       []string
		extensions []string
		missing    []string
	}{
		{name: "empty declaration and evaluation"},
		{name: "undeclared root", accepted: []string{""}, extensions: []string{""}},
		{name: "scalar root", fields: []StandardField{{Path: "", Required: true}}, accepted: []string{"/vendor", ""}, core: []string{""}, extensions: []string{"/vendor"}},
		{name: "missing scalar stays missing", fields: []StandardField{{Path: "", Required: true}}, accepted: []string{"/vendor"}, extensions: []string{"/vendor"}, missing: []string{""}},
		{name: "empty root matches slash but remains missing", fields: []StandardField{{Path: "", Required: true}}, accepted: []string{"/"}, core: []string{"/"}, missing: []string{""}},
		{name: "slash matches empty root but remains missing", fields: []StandardField{{Path: "/", Required: true}}, accepted: []string{""}, core: []string{""}, missing: []string{"/"}},
		{name: "optional duplicate does not cancel required", fields: []StandardField{{Path: "/balance", Required: true}, {Path: "/balance"}}, missing: []string{"/balance"}},
		{name: "required duplicate after optional", fields: []StandardField{{Path: "/balance"}, {Path: "/balance", Required: true}}, missing: []string{"/balance"}},
		{name: "duplicate required missing paths stay repeated and sorted", fields: []StandardField{{Path: "/z", Required: true}, {Path: "/a", Required: true}, {Path: "/z", Required: true}}, missing: []string{"/a", "/z", "/z"}},
		{name: "duplicate accepted and declared paths", fields: []StandardField{{Path: "/balance", Required: true}, {Path: "/balance"}}, accepted: []string{"/vendor", "/balance", "/balance"}, core: []string{"/balance", "/balance"}, extensions: []string{"/vendor"}},
		{name: "only the first slash is optional", fields: []StandardField{{Path: "balance", Required: true}}, accepted: []string{"/balance", "//balance"}, core: []string{"/balance"}, extensions: []string{"//balance"}, missing: []string{"balance"}},
		{name: "bare accepted path", fields: []StandardField{{Path: "/balance", Required: true}}, accepted: []string{"balance"}, core: []string{"balance"}, missing: []string{"/balance"}},
		{name: "wildcard consumes exactly one segment including empty", fields: []StandardField{{Path: "/logs/*/address", Required: true}}, accepted: []string{"/logs/0/address", "/logs//address", "/logs/address", "/logs/0/extra/address", "/logs/0/address/"}, core: []string{"/logs//address", "/logs/0/address"}, extensions: []string{"/logs/0/address/", "/logs/0/extra/address", "/logs/address"}},
		{name: "nested wildcard and literal star", fields: []StandardField{{Path: "/logs/*/topics/*", Required: true}}, accepted: []string{"/logs/0/topics/1", "/logs/*/topics/*", "/logs/0/topics", "/logs/0/topics/1/deeper"}, core: []string{"/logs/*/topics/*", "/logs/0/topics/1"}, extensions: []string{"/logs/0/topics", "/logs/0/topics/1/deeper"}},
		{name: "bare wildcard remains literally required", fields: []StandardField{{Path: "*", Required: true}}, accepted: []string{"", "/", "name", "/name", "/name/child"}, core: []string{"", "/", "/name", "name"}, extensions: []string{"/name/child"}, missing: []string{"*"}},
		{name: "embedded star is literal but slash star skips required", fields: []StandardField{{Path: "/logs/*suffix", Required: true}, {Path: "prefix*", Required: true}}, accepted: []string{"/logs/*suffix", "/logs/suffix", "prefix*", "prefix1"}, core: []string{"/logs/*suffix", "prefix*"}, extensions: []string{"/logs/suffix", "prefix1"}},
		{name: "embedded slash star required is skipped even when absent", fields: []StandardField{{Path: "/logs/*suffix", Required: true}, {Path: "prefix*", Required: true}}, missing: []string{"prefix*"}},
		{name: "escaped pointer tokens remain literal", fields: []StandardField{{Path: "/a~1b"}, {Path: "/a~0b"}}, accepted: []string{"/a/b", "/a~b", "/a~1b", "/a~0b"}, core: []string{"/a~0b", "/a~1b"}, extensions: []string{"/a/b", "/a~b"}},
		{name: "both output groups sorted", fields: []StandardField{{Path: "/z"}, {Path: "/a"}}, accepted: []string{"/vendorZ", "/z", "/vendorA", "/a"}, core: []string{"/a", "/z"}, extensions: []string{"/vendorA", "/vendorZ"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			evaluation := rpc.Evaluation{Accepted: projectionObservations(tt.accepted...)}
			got := ProjectStandard(evaluation, tt.fields)
			if !reflect.DeepEqual(projectionObservationPaths(got.Core), tt.core) || !reflect.DeepEqual(projectionObservationPaths(got.AcceptedExtensions), tt.extensions) || !reflect.DeepEqual(got.Missing, tt.missing) {
				t.Fatalf("projection = core %q, extensions %q, missing %q; want %q, %q, %q", projectionObservationPaths(got.Core), projectionObservationPaths(got.AcceptedExtensions), got.Missing, tt.core, tt.extensions, tt.missing)
			}
			want, err := rpc.NewExtensionSet(json.RawMessage(`"0x1"`), projectionObservations(tt.extensions...))
			if err != nil {
				t.Fatal(err)
			}
			projector := NewStandardProjector(tt.fields)
			for i := 0; i < 2; i++ {
				set, err := projector.ProjectObservation(json.RawMessage(`"0x1"`), evaluation)
				if err != nil || !reflect.DeepEqual(set, want) {
					t.Fatalf("reused projector call %d = %#v, %v; want %#v", i, set, err, want)
				}
			}
			if !reflect.DeepEqual(projectionObservationPaths(evaluation.Accepted), tt.accepted) {
				t.Fatal("projection reordered or overwrote accepted input")
			}
		})
	}
}

func TestStandardProjectorOwnsDeclarationsAndDeliveredEvidence(t *testing.T) {
	fields := []StandardField{{Path: "", Required: true}, {Path: "/logs/*/address"}}
	projector := NewStandardProjector(fields)
	fields[0] = StandardField{Path: "/vendor"}
	fields[1] = StandardField{Path: "/different/*"}
	fields = append(fields, StandardField{Path: "/other"})

	evaluation := rpc.Evaluation{Accepted: projectionObservations("/vendor", "", "/logs/0/address")}
	want, err := rpc.NewExtensionSet(json.RawMessage(`"0x1"`), projectionObservations("/vendor"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := projector.ProjectObservation(json.RawMessage(`"0x1"`), evaluation)
	if err != nil || !reflect.DeepEqual(first, want) {
		t.Fatalf("declaration mutation changed projector: %#v, %v; want %#v", first, err, want)
	}
	first.Fields[0].Path = "/changed"
	first.Fields[0].Value[1] = '9'
	first.Fields[0].Sources[0].ID = "changed"
	second, err := projector.ProjectObservation(json.RawMessage(`"0x1"`), evaluation)
	if err != nil || !reflect.DeepEqual(second, want) {
		t.Fatalf("delivery mutation changed later result: %#v, %v", second, err)
	}
	evaluation.Accepted[0].Value[1] = '8'
	evaluation.Accepted[0].Sources[0].ID = "input changed"
	if !reflect.DeepEqual(second, want) {
		t.Fatal("delivered evidence aliases accepted values or sources")
	}
}

func TestStandardProjectionOwnsClassificationSlices(t *testing.T) {
	evaluation := rpc.Evaluation{Accepted: projectionObservations("/vendorZ", "/z", "/vendorA", "/a")}
	before := append([]rpc.FieldObservation(nil), evaluation.Accepted...)
	projection := ProjectStandard(evaluation, []StandardField{{Path: "/z"}, {Path: "/a"}})
	projection.Core[0].Path = "/changed-core"
	projection.AcceptedExtensions[0].Path = "/changed-extension"
	projection.Core = append(projection.Core, rpc.FieldObservation{Path: "/appended-core"})
	projection.AcceptedExtensions = append(projection.AcceptedExtensions, rpc.FieldObservation{Path: "/appended-extension"})
	if !reflect.DeepEqual(evaluation.Accepted, before) {
		t.Fatal("classification slices alias accepted input")
	}
}

func TestStandardProjectorPreservesFingerprintErrorsAndDiagnosticBoundary(t *testing.T) {
	for _, core := range []string{`"0x1"`, `"0x00"`, `null`, `42`, `{"b":2,"a":1}`, ` { "a": 1, "b": 2 } `, ``, `{"broken":`, `true false`} {
		for _, withExtension := range []bool{false, true} {
			t.Run(core+"/"+map[bool]string{false: "without-extension", true: "with-extension"}[withExtension], func(t *testing.T) {
				evaluation := rpc.Evaluation{Diagnostics: rpc.FieldDiagnostics{
					Residuals: projectionObservations("/residual"),
					Conflicts: []rpc.ConflictField{{Path: "/conflict", Values: projectionObservations("/conflict")}},
				}}
				var extensions []rpc.FieldObservation
				if withExtension {
					extensions = projectionObservations("/vendor")
					evaluation.Accepted = extensions
				}
				want, wantErr := rpc.NewExtensionSet(json.RawMessage(core), extensions)
				got, err := ScalarStandardProjector().ProjectObservation(json.RawMessage(core), evaluation)
				if (err == nil) != (wantErr == nil) || (err != nil && err.Error() != wantErr.Error()) || !reflect.DeepEqual(got, want) {
					t.Fatalf("carrier = %#v, %v; want %#v, %v", got, err, want, wantErr)
				}
				projection := ProjectStandard(evaluation, []StandardField{{Path: "", Required: true}})
				if len(projection.Core) != 0 || !reflect.DeepEqual(projection.Missing, []string{""}) {
					t.Fatal("extensions or diagnostics supplied the missing scalar")
				}
				if len(evaluation.Diagnostics.Residuals) != 1 || len(evaluation.Diagnostics.Conflicts) != 1 {
					t.Fatal("projection changed diagnostics")
				}
			})
		}
	}
	projector := ScalarStandardProjector()
	evaluation := rpc.Evaluation{Accepted: projectionObservations("/vendor")}
	left, err := projector.ProjectObservation(json.RawMessage(`{"b":2,"a":1}`), evaluation)
	if err != nil {
		t.Fatal(err)
	}
	right, err := projector.ProjectObservation(json.RawMessage(` { "a": 1, "b": 2 } `), evaluation)
	if err != nil || !reflect.DeepEqual(left, right) {
		t.Fatalf("canonical fingerprint changed with formatting: %#v, %v", right, err)
	}
	other, err := projector.ProjectObservation(json.RawMessage(`{"a":2,"b":2}`), evaluation)
	if err != nil || left.Fields[0].CoreObjectFingerprint == other.Fields[0].CoreObjectFingerprint {
		t.Fatalf("evidence lost its core binding: %#v, %v", other, err)
	}
}

func TestStandardProjectorConcurrentReuse(t *testing.T) {
	projector := NewStandardProjector([]StandardField{{Path: "", Required: true}, {Path: "/logs/*/address"}})
	evaluation := rpc.Evaluation{Accepted: projectionObservations("/logs/0/address", "/vendor", "")}
	want, err := rpc.NewExtensionSet(json.RawMessage(`"0x1"`), projectionObservations("/vendor"))
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for i := 0; i < 16; i++ {
		workers.Go(func() {
			for j := 0; j < 32; j++ {
				got, err := projector.ProjectObservation(json.RawMessage(`"0x1"`), evaluation)
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Errorf("concurrent delivery = %#v, %v; want %#v", got, err, want)
					return
				}
				got.Fields[0].Value[1] = '9'
				got.Fields[0].Sources[0].ID = "local mutation"
			}
		})
	}
	workers.Wait()
}

func projectionObservations(paths ...string) []rpc.FieldObservation {
	var fields []rpc.FieldObservation
	for _, path := range paths {
		fields = append(fields, rpc.FieldObservation{Path: path, Value: json.RawMessage(`"0x1"`), Sources: []rpc.SourceRef{{ID: "a"}, {ID: "b"}}})
	}
	return fields
}

func projectionObservationPaths(fields []rpc.FieldObservation) []string {
	var paths []string
	for _, field := range fields {
		paths = append(paths, field.Path)
	}
	return paths
}
