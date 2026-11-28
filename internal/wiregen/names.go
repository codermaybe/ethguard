package main

import (
	"fmt"
	"github.com/codermaybe/ethguard/internal/executionapi"
)

// registerModelNames resolves explicit names against semantic shapes before
// traversal. Equivalent models use the same name regardless of which method
// is visited first; two different shapes cannot claim one public name.
func (reg *registry) registerModelNames(n *schema, path string) error {
	if name, ok := reg.namesByPath[path]; ok {
		reg.report.visited[path] = true
		if n.typeVal != "object" && len(n.branches) == 0 {
			return fmt.Errorf("spec: naming path %s is not a named model", path)
		}
		key := reg.modelFingerprint(n)
		if previous := reg.preferredNames[key]; previous != "" && previous != name {
			return fmt.Errorf("spec: naming conflict at %s: equivalent model claims %s and %s", path, previous, name)
		}
		if previous := reg.reservedNames[name]; previous != "" && previous != key {
			return fmt.Errorf("spec: naming conflict at %s: %s claims distinct schemas", path, name)
		}
		if reg.usedNames[name] && reg.preferredNames[key] != name {
			return fmt.Errorf("spec: naming conflict at %s: %s already used", path, name)
		}
		reg.preferredNames[key], reg.reservedNames[name] = name, key
	}
	for _, p := range n.properties {
		if err := reg.registerModelNames(p.schema, schemaChild(path, "properties", p.name)); err != nil {
			return err
		}
	}
	for _, p := range n.patternProperties {
		if err := reg.registerModelNames(p.schema, schemaChild(path, "patternProperties", p.name)); err != nil {
			return err
		}
	}
	if n.items != nil {
		if err := reg.registerModelNames(n.items, schemaChild(path, "items")); err != nil {
			return err
		}
	}
	for i, b := range n.branches {
		if len(n.properties) > 0 {
			b = mergeBranch(n, b)
		}
		if err := reg.registerModelNames(b, schemaChild(path, n.compKind, itoa(i))); err != nil {
			return err
		}
	}
	return nil
}

func schemaChild(path string, tokens ...string) string {
	for _, token := range tokens {
		path += "/" + executionapi.EscapePointerToken(token)
	}
	return path
}
