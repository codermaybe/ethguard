package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/codermaybe/ethguard/internal/executionapi"
)

// Source extraction and compilation must interpret the same Manifest scope.
// The inventory is mandatory even for synthetic compiler integration inputs;
// low-level walker tests can construct registries without this entry point.
func validateSourceSelection(bundle executionapi.Bundle, manifest executionapi.Manifest) error {
	if len(bundle.Inventory) == 0 {
		return fmt.Errorf("spec: complete upstream inventory is required for compilation")
	}
	available := map[string]bool{}
	expected := map[string]bool{}
	matched := map[string]int{}
	for _, method := range bundle.Inventory {
		if method.Name == "" || available[method.Name] {
			return fmt.Errorf("spec: invalid or duplicate upstream inventory method %q", method.Name)
		}
		available[method.Name] = true
		for _, prefix := range manifest.RequestInventoryPrefixes {
			if strings.HasPrefix(method.Name, prefix) {
				expected[method.Name] = true
				matched[prefix]++
			}
		}
	}
	for _, method := range manifest.ResultMethods {
		if !available[method] {
			return fmt.Errorf("spec: result selection %s absent from upstream inventory", method)
		}
	}
	for _, prefix := range manifest.RequestInventoryPrefixes {
		if matched[prefix] == 0 {
			return fmt.Errorf("spec: unused request inventory prefix %q", prefix)
		}
		for _, other := range manifest.RequestInventoryPrefixes {
			if prefix != other && strings.HasPrefix(prefix, other) {
				return fmt.Errorf("spec: redundant request inventory prefix %q covered by %q", prefix, other)
			}
		}
	}
	var missing, extra []string
	for method := range expected {
		if _, ok := bundle.Requests[method]; !ok {
			missing = append(missing, method)
		}
	}
	for method := range bundle.Requests {
		if !expected[method] {
			extra = append(extra, method)
		}
	}
	if len(missing)+len(extra) > 0 {
		sort.Strings(missing)
		sort.Strings(extra)
		return fmt.Errorf("spec: Manifest request inventory scope differs from extracted source: missing=%v extra=%v; re-extract source with the same Manifest", missing, extra)
	}
	return nil
}

func (reg *registry) validatePolicyUsage() error {
	var unused []string
	for path := range reg.namesByPath {
		if !reg.report.visited[path] {
			unused = append(unused, "name:"+path)
		}
	}
	for title := range reg.manifest.TitleNames {
		if !reg.usedTitles[title] {
			unused = append(unused, "title:"+title)
		}
	}
	for model := range reg.manifest.FieldOrder {
		if !reg.usedOrders[model] {
			unused = append(unused, "fieldOrder:"+model)
		}
	}
	sort.Strings(unused)
	if len(unused) > 0 {
		return fmt.Errorf("spec: unused or shadowed Manifest rules: %v", unused)
	}
	return nil
}

func (reg *registry) validatePublicNames(roots map[string]*ref) error {
	names := map[string]string{}
	add := func(name, owner string) error {
		if previous := names[name]; previous != "" {
			return fmt.Errorf("spec: public name collision %s: %s and %s", name, previous, owner)
		}
		names[name] = owner
		return nil
	}
	model := func(name, owner string) error {
		if err := add(name, owner); err != nil {
			return err
		}
		return add("New"+name, owner+" constructor")
	}
	for _, n := range natives {
		if err := model(n.name, "native"); err != nil {
			return err
		}
	}
	for _, m := range reg.objects {
		if err := model(m.name, "object"); err != nil {
			return err
		}
	}
	for _, m := range reg.unionList {
		if err := model(m.name, "union"); err != nil {
			return err
		}
	}
	for _, m := range reg.maps {
		if err := model(m.name, "dictionary"); err != nil {
			return err
		}
	}
	aliases := make([]string, 0, len(reg.manifest.Aliases))
	for alias := range reg.manifest.Aliases {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	for _, alias := range aliases {
		target := reg.manifest.Aliases[alias]
		seen := map[string]bool{alias: true}
		for reg.manifest.Aliases[target] != "" {
			if seen[target] {
				return fmt.Errorf("spec: alias cycle at %s", target)
			}
			seen[target] = true
			target = reg.manifest.Aliases[target]
		}
		if names[target] == "" {
			return fmt.Errorf("spec: alias %s targets absent model %s", alias, target)
		}
		if err := model(alias, "alias"); err != nil {
			return err
		}
	}
	methods := make([]string, 0, len(roots))
	for method := range roots {
		methods = append(methods, method)
	}
	sort.Strings(methods)
	for _, method := range methods {
		if rootUsesConstructor(method, roots[method]) {
			continue
		}
		if err := add(methodShimName(method), "result of "+method); err != nil {
			return err
		}
	}
	for _, method := range reg.manifest.ValueValidationResults {
		if err := add(methodValidatorName(method), "value validator of "+method); err != nil {
			return err
		}
	}
	return nil
}
