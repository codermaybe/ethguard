package main

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/dave/jennifer/jen"
)

func TestNativeFixedHexSpecializationKeepsExactPatternBoundary(t *testing.T) {
	for _, fixture := range []struct {
		pattern string
		digits  int
	}{
		{`^0x[0-9a-f]{64}$`, 64},
		{`^0x[0-9a-f]{512}$`, 512},
		{`^0x[0-9a-f]{16}$`, 16},
		{`^0x[0-9a-f]{40}$`, 0},
		{`^0x[0-9a-fA-F]{64}$`, 0},
		{`^0x[a-f0-9]{64}$`, 0},
		{`^0x[0-9a-f]{64}`, 0},
		{`0x[0-9a-f]{64}$`, 0},
		{`^0x([0-9a-f]{2})*$`, 0},
		{`^0x(0|[1-9a-f][0-9a-f]*)$`, 0},
	} {
		t.Run(fixture.pattern, func(t *testing.T) {
			file := jen.NewFile("fixture")
			emitNative(file, nativeDescriptor{name: "Candidate", pattern: fixture.pattern, representation: preserveLiteral})
			var buffer bytes.Buffer
			if err := file.Render(&buffer); err != nil {
				t.Fatal(err)
			}
			source := buffer.String()
			if !strings.Contains(source, "json.Unmarshal(raw, &text)") {
				t.Fatal("native string decoder was removed")
			}
			if fixture.digits != 0 {
				if !strings.Contains(source, "IsLowerFixedHex(text, "+strconv.Itoa(fixture.digits)+")") || strings.Contains(source, "regexp.MustCompile") {
					t.Fatalf("fixed native did not specialize its exact pattern:\n%s", source)
				}
			} else if strings.Contains(source, "IsLowerFixedHex") || !strings.Contains(source, "regexp.MustCompile("+strconv.Quote(fixture.pattern)+")") {
				t.Fatalf("unrecognized pattern lost its regexp fallback:\n%s", source)
			}
		})
	}
}

func TestSchemaValueFixedHexPatternRetainsRegexp(t *testing.T) {
	file := jen.NewFile("fixture")
	emitValue(file, &ref{kind: refScalar, native: "Text", parser: "parseFixture", path: "fixture", pattern: `^0x[0-9a-f]{64}$`})
	var buffer bytes.Buffer
	if err := file.Render(&buffer); err != nil {
		t.Fatal(err)
	}
	source := buffer.String()
	if strings.Contains(source, "IsLowerFixedHex") || !strings.Contains(source, `regexp.MustCompile("^0x[0-9a-f]{64}$")`) {
		t.Fatalf("non-native schema value changed its pattern implementation:\n%s", source)
	}
}
