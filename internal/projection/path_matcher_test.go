package projection

import (
	"strings"
	"testing"
)

// Frozen matcher from before the candidate path stopped using strings.Split.
// Keep this independent of both production matching helpers.
func frozenProjectionPathMatch(fields []StandardField, path string) bool {
	actual := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for _, field := range fields {
		pattern := strings.Split(strings.TrimPrefix(field.Path, "/"), "/")
		if len(pattern) != len(actual) {
			continue
		}
		matches := true
		for i, expected := range pattern {
			if expected != "*" && expected != actual[i] {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}

func TestProjectionPathMatchingAllocations(t *testing.T) {
	fields := []StandardField{
		{Path: "/logs/*/topics/*"}, {Path: "/logs/*/address"}, {Path: "/blockHash"},
		{Path: "/a/*/b/*/c/*/d/*"},
	}
	projector := compileStandardFields(fields)
	for _, path := range []string{
		"/logs/0/topics/1", "/logs//address", "/logs/0/unknown", "/blockHash",
		"/a/0/b/1/c/2/d/3", "/a//b//c//d/", "/a/0/b/1/c/2/unknown/3",
	} {
		want := frozenProjectionPathMatch(fields, path)
		if allocations := testing.AllocsPerRun(100, func() {
			if got := projector.matchesPath(path); got != want {
				t.Fatalf("path %q matched=%v, want %v", path, got, want)
			}
		}); allocations != 0 {
			t.Fatalf("path %q allocated %v times", path, allocations)
		}
	}
}

func TestProjectionPathMatchingLongPaths(t *testing.T) {
	for _, segments := range []int{7, 8, 9, 16, 17} {
		fields := []StandardField{
			{Path: "/a/*"}, {Path: "/a/*/different"},
			{Path: "/" + strings.Repeat("*/", segments-1) + "leaf"},
		}
		projector := compileStandardFields(fields)
		prefix := "/" + strings.Repeat("a/", segments-1)
		for _, path := range []string{
			prefix + "leaf", prefix, prefix + "leaf/", prefix + "other",
			strings.Repeat("/", segments) + "leaf", "/" + prefix + "leaf",
		} {
			if got, want := projector.matchesPath(path), frozenProjectionPathMatch(fields, path); got != want {
				t.Fatalf("segments=%d path=%q matched=%v, want %v", segments, path, got, want)
			}
		}
	}
}

func FuzzProjectionPathMatching(f *testing.F) {
	for _, seed := range [][2]string{
		{"", ""}, {"/", ""}, {"*", ""}, {"/logs/*/address", "/logs//address"},
		{"/logs/*/topics/*", "/logs/*/topics/*"}, {"/logs/*/address", "/logs/0/address/"},
		{"/logs/*suffix", "/logs/suffix"}, {"/a~1b\x00/a~0b", "/a~1b"},
		{"/balance", "//balance"}, {"/*/*", "///"}, {"/a/*\x00/*/b", "/a/b"},
		{"/" + strings.Repeat("*/", 8) + "leaf", "/" + strings.Repeat("a/", 8) + "leaf"},
		{"/" + strings.Repeat("*/", 16), strings.Repeat("/", 17)},
	} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, declarations, path string) {
		var fields []StandardField
		for _, declaration := range strings.Split(declarations, "\x00") {
			fields = append(fields, StandardField{Path: declaration})
		}
		projector := compileStandardFields(fields)
		if got, want := projector.matchesPath(path), frozenProjectionPathMatch(fields, path); got != want {
			t.Fatalf("fields=%q path=%q matched=%v want=%v", declarations, path, got, want)
		}
	})
}
