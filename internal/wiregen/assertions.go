package main

import (
	"fmt"

	"github.com/dave/jennifer/jen"
)

// Both JSON parsing and explicit value validation compile these assertions
// from the same ref. Only their error return signatures differ. The local
// value is named out in either generated context.
func emitValueAssertions(g *jen.Group, r *ref, fail func(jen.Code) *jen.Statement) {
	if r.kind == refArray {
		if r.minItems != nil {
			g.If(jen.Len(jen.Id("out")).Op("<").Lit(*r.minItems)).Block(fail(errorf(r.path + ": fewer than minItems")))
		}
		if r.maxItems != nil {
			g.If(jen.Len(jen.Id("out")).Op(">").Lit(*r.maxItems)).Block(fail(errorf(r.path + ": more than maxItems")))
		}
		if r.uniqueItems {
			// Selected value-validator graphs reject uniqueItems before emission.
			emitUniqueItems(g, r)
		}
	}
	if r.hasEnum {
		emitEnumCheck(g, r, fail)
	}
	if r.hasConst && r.native != "int" {
		var condition *jen.Statement
		if r.native == "Number" {
			condition = compareNumber(fmt.Sprint(r.constant)).Op("!=").Lit(0)
		} else {
			condition = jen.Id("out").Op("!=").Add(constCode(r.constant))
		}
		g.If(condition).Block(fail(errorf(r.path + ": expected constant")))
	}
	if r.pattern != "" && nativeForPattern(r.pattern) != r.native {
		g.If(jen.Op("!").Id(r.parser + "Pattern").Dot("MatchString").Call(jen.String().Call(jen.Id("out")))).Block(
			fail(errorf(r.path + ": does not match official pattern")),
		)
	}
	if r.minimum != nil {
		g.If(compareNumber(r.minimum.String()).Op("<").Lit(0)).Block(fail(errorf(r.path + ": below official minimum")))
	}
	if r.maximum != nil {
		g.If(compareNumber(r.maximum.String()).Op(">").Lit(0)).Block(fail(errorf(r.path + ": above official maximum")))
	}
}

func nativePatternMatch(n nativeDescriptor, text jen.Code) *jen.Statement {
	if validator := sharedStrictValidator(n.pattern); validator != "" {
		return jen.Qual(codecPackage, validator).Call(text)
	}
	if digits := fixedLowerHexPatternDigits(n.pattern); digits != 0 {
		return jen.Qual(codecPackage, "IsLowerFixedHex").Call(text, jen.Lit(digits))
	}
	return jen.Id("native" + n.name + "Pattern").Dot("MatchString").Call(text)
}
