package main

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/dave/jennifer/jen"
)

func TestNativeStrictScalarSpecializationKeepsExactPatternBoundary(t *testing.T) {
	for _, fixture := range []struct {
		name, pattern, validator, changedPattern string
	}{
		{"Quantity", `^0x(0|[1-9a-f][0-9a-f]*)$`, "IsCanonicalQuantity", `^0x[0-9a-f]+$`},
		{"Quantity64", `^0x(0|[1-9a-f][0-9a-f]{0,15})$`, "IsCanonicalQuantity64", `^0x(0|[1-9a-f][0-9a-f]{0,14})$`},
		{"Quantity256", `^0x(0|[1-9a-f][0-9a-f]{0,63})$`, "IsCanonicalQuantity256", `^0x(0|[1-9a-f][0-9a-f]{0,64})$`},
		{"Data", `^0x([0-9a-f]{2})*$`, "IsWholeByteData", `^0x[0-9a-f]*$`},
		{"Address", `^0x[0-9a-fA-F]{40}$`, "IsAddress", `^0X[0-9a-fA-F]{40}$`},
		{"HexByte", `^0x[0-9a-f]{1,2}$`, "IsHexByte", `^0x[0-9a-fA-F]{1,2}$`},
		{"StorageKey", `^0x[0-9a-f]{0,64}$`, "IsStorageKey", `^0x[0-9a-f]{1,64}$`},
		{"DecimalString", `^(0|[1-9][0-9]*)$`, "IsCanonicalDecimal", `^[0-9]+$`},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			// The native name neither enables nor disables specialization.
			for _, name := range []string{fixture.name, "Unrelated"} {
				for _, pattern := range []string{fixture.pattern, fixture.changedPattern, strings.TrimPrefix(fixture.pattern, "^"), strings.TrimSuffix(fixture.pattern, "$")} {
					file := jen.NewFile("fixture")
					emitNative(file, nativeDescriptor{name: name, pattern: pattern, representation: preserveLiteral})
					var buffer bytes.Buffer
					if err := file.Render(&buffer); err != nil {
						t.Fatal(err)
					}
					source := buffer.String()
					if !strings.Contains(source, "json.Unmarshal(raw, &text)") {
						t.Fatal("native JSON string decoding changed")
					}
					if pattern == fixture.pattern {
						if !strings.Contains(source, fixture.validator+"(text)") || strings.Contains(source, "regexp.MustCompile") {
							t.Fatalf("exact pattern %q did not select %s for %s:\n%s", pattern, fixture.validator, name, source)
						}
					} else if strings.Contains(source, fixture.validator+"(text)") || !strings.Contains(source, "regexp.MustCompile("+strconv.Quote(pattern)+")") {
						t.Fatalf("changed pattern %q lost its regexp for %s:\n%s", pattern, name, source)
					}
				}
			}
		})
	}
}

func TestReviewedStrictScalarPoliciesDescribeASCII(t *testing.T) {
	policies, err := reviewedScalarPolicies(testManifest())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"Quantity":      "IsCanonicalQuantity",
		"Quantity64":    "IsCanonicalQuantity64",
		"Quantity256":   "IsCanonicalQuantity256",
		"Data":          "IsWholeByteData",
		"Address":       "IsAddress",
		"HexByte":       "IsHexByte",
		"StorageKey":    "IsStorageKey",
		"DecimalString": "IsCanonicalDecimal",
	}
	for _, policy := range policies {
		validator, exists := want[policy.Name]
		if !exists {
			continue
		}
		if policy.Validator != "ethcodec."+validator+": shared strict ASCII" {
			t.Fatalf("%s audit validator = %q", policy.Name, policy.Validator)
		}
		delete(want, policy.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing strict scalar policies: %v", want)
	}
}
