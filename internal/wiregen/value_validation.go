package main

import (
	"fmt"
	"sort"

	"github.com/dave/jennifer/jen"
)

func methodValidatorName(method string) string { return "Validate" + methodCamel(method) + "Result" }
func objectValidatorName(name string) string   { return "validate" + name + "Value" }

// Value validation is opt-in for complete result roots. Reject unsupported
// reachable shapes before publishing either generated output; other roots
// keep their existing JSON constructor capabilities.
func (reg *registry) valueValidationObjects(roots map[string]*ref) ([]*objectModel, error) {
	objects := map[string]*objectModel{}
	for _, obj := range reg.objects {
		objects[obj.name] = obj
	}
	seen := map[string]bool{}
	var selected []*objectModel
	var visit func(*ref) error
	visit = func(r *ref) error {
		fail := func(constraint string) error {
			return fmt.Errorf("spec: %s: value validation does not support %s", r.path, constraint)
		}
		if r.uniqueItems {
			return fail("uniqueItems")
		}
		if r.pointer {
			return visit(r.inner)
		}
		switch r.kind {
		case refScalar:
			for _, n := range natives {
				if n.name == r.native && n.representation != exactNumber {
					return nil
				}
			}
			return fail("non-string scalar " + r.native)
		case refArray:
			return visit(r.elem)
		case refObject:
			obj := objects[r.typeName]
			if obj == nil {
				return fail("missing object " + r.typeName)
			}
			if obj.retainAdditional {
				return fail("retained additional properties")
			}
			if seen[obj.name] {
				return nil
			}
			seen[obj.name] = true
			selected = append(selected, obj)
			for _, field := range obj.fields {
				if err := visit(field.ref); err != nil {
					return err
				}
			}
			return nil
		case refUnion:
			return fail("multi-branch oneOf/anyOf")
		case refMap:
			return fail("dictionary")
		default:
			return fail("unknown shape")
		}
	}
	for _, method := range reg.manifest.ValueValidationResults {
		root := roots[method]
		if root == nil {
			return nil, fmt.Errorf("spec: missing value validation result %s.result", method)
		}
		if err := visit(root); err != nil {
			return nil, err
		}
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].name < selected[j].name })
	return selected, nil
}

func emitValueValidators(f *jen.File, reg *registry, roots map[string]*ref, objects []*objectModel) {
	methods := append([]string(nil), reg.manifest.ValueValidationResults...)
	sort.Strings(methods)
	for _, method := range methods {
		r := roots[method]
		f.Commentf("%s checks the value as encoded by MarshalJSON against the complete official %s result.", methodValidatorName(method), method)
		f.Comment("It reads without modifying or encoding the value. It does not validate original JSON input or safety budgets.")
		f.Func().Id(methodValidatorName(method)).Params(jen.Id("v").Add(typeCode(r))).Error().BlockFunc(func(g *jen.Group) {
			emitValidateRef(g, r, jen.Id("v"))
			g.Return(jen.Nil())
		})
	}
	for _, obj := range objects {
		f.Func().Id(objectValidatorName(obj.name)).Params(jen.Id("v").Id(obj.name)).Error().BlockFunc(func(g *jen.Group) {
			if obj.hasForbiddenRequired {
				condition := jen.True()
				for _, key := range obj.forbiddenRequired {
					present := jen.False()
					for _, field := range obj.fields {
						if field.json == key {
							present = jen.True()
							if !field.req {
								present = jen.Id("v").Dot(field.goName).Op("!=").Nil()
							}
						}
					}
					condition.Op("&&").Add(present)
				}
				g.If(condition).Block(jen.Return(errorf(obj.name + ": forbidden required-field combination")))
			}
			for _, field := range obj.fields {
				value := jen.Id("v").Dot(field.goName)
				if field.req {
					emitValidateRef(g, field.ref, value)
				} else if valueNeedsCheck(field.ref) {
					g.If(value.Clone().Op("!=").Nil()).BlockFunc(func(g *jen.Group) {
						emitValidateRef(g, field.ref, jen.Parens(jen.Op("*").Add(value)))
					})
				}
			}
			g.Return(jen.Nil())
		})
	}
}

func valueNeedsCheck(r *ref) bool {
	if r.pointer {
		return valueNeedsCheck(r.inner)
	}
	return r.kind != refScalar || r.native != "Text" || r.pattern != "" || r.hasEnum || r.hasConst
}

// Every local out is a copy. Nullable slices need two nil checks: both a nil
// pointer and a pointer to a nil slice marshal as null. Requiredness itself
// is encoded in the generated Go field and omitempty tag, not in its zero value.
func emitValidateRef(g *jen.Group, r *ref, value *jen.Statement) {
	if !valueNeedsCheck(r) {
		return
	}
	if r.pointer {
		present := value.Clone().Op("!=").Nil()
		if r.inner.kind == refArray {
			present.Op("&&").Parens(jen.Op("*").Add(value)).Op("!=").Nil()
		}
		g.If(present).BlockFunc(func(g *jen.Group) { emitValidateRef(g, r.inner, jen.Parens(jen.Op("*").Add(value))) })
		return
	}
	if r.kind == refObject {
		g.If(jen.Err().Op(":=").Id(objectValidatorName(r.typeName)).Call(value), jen.Err().Op("!=").Nil()).Block(jen.Return(jen.Err()))
		return
	}
	g.BlockFunc(func(g *jen.Group) {
		g.Id("out").Op(":=").Add(value)
		if r.kind == refArray {
			g.If(jen.Id("out").Op("==").Nil()).Block(jen.Return(errorf(r.path + ": expected a non-null value")))
			if valueNeedsCheck(r.elem) {
				g.For(jen.List(jen.Id("_"), jen.Id("value")).Op(":=").Range().Id("out")).BlockFunc(func(g *jen.Group) {
					emitValidateRef(g, r.elem, jen.Id("value"))
				})
			}
		} else {
			// encoding/json replaces each malformed UTF-8 byte with U+FFFD.
			// A local rune conversion matches that behavior without rewriting v.
			if r.native == "Text" {
				g.If(jen.Op("!").Qual("unicode/utf8", "ValidString").Call(jen.String().Call(jen.Id("out")))).Block(
					jen.Id("out").Op("=").Id("Text").Call(jen.String().Call(jen.Index().Rune().Call(jen.Id("out")))),
				)
			}
			for _, n := range natives {
				if n.name == r.native && n.pattern != "" {
					g.If(jen.Op("!").Add(nativePatternMatch(n, jen.String().Call(jen.Id("out"))))).Block(
						jen.Return(errorf(r.path+": "+n.name+": invalid input spelling %q", jen.Id("out"))),
					)
				}
			}
		}
		emitValueAssertions(g, r, func(err jen.Code) *jen.Statement { return jen.Return(err) })
	})
}
