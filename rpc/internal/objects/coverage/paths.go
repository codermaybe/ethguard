// Package coverage contains test-side helpers for comparing JSON encoder
// struct tags with the pinned execution-apis field paths.
package coverage

import (
	"reflect"
	"sort"
	"strings"
)

// JSONPaths returns the JSON leaf paths represented by a canonical encoder
// struct. Struct fields nested below arrays use a wildcard array segment.
func JSONPaths(value any) []string {
	paths := make(map[string]bool)
	collect(reflect.TypeOf(value), "", paths)
	result := make([]string, 0, len(paths))
	for path := range paths {
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}

func collect(typ reflect.Type, path string, paths map[string]bool) {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	switch typ.Kind() {
	case reflect.Struct:
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "" || name == "-" {
				continue
			}
			fieldPath := path + "/" + name
			item := field.Type
			for item.Kind() == reflect.Pointer {
				item = item.Elem()
			}
			if item.Kind() == reflect.Slice || item.Kind() == reflect.Array {
				arrayPath := fieldPath + "/*"
				element := item.Elem()
				for element.Kind() == reflect.Pointer {
					element = element.Elem()
				}
				if element.Kind() == reflect.Struct {
					collect(element, arrayPath, paths)
				} else if element.Kind() == reflect.Slice || element.Kind() == reflect.Array {
					collectArray(element, arrayPath, paths)
				} else {
					paths[arrayPath] = true
				}
				continue
			}
			if item.Kind() == reflect.Struct {
				collect(item, fieldPath, paths)
				continue
			}
			paths[fieldPath] = true
		}
	}
}

func collectArray(typ reflect.Type, path string, paths map[string]bool) {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Slice && typ.Kind() != reflect.Array {
		paths[path] = true
		return
	}
	element := typ.Elem()
	for element.Kind() == reflect.Pointer {
		element = element.Elem()
	}
	if element.Kind() == reflect.Struct {
		collect(element, path+"/*", paths)
	} else if element.Kind() == reflect.Slice || element.Kind() == reflect.Array {
		collectArray(element, path+"/*", paths)
	} else {
		paths[path+"/*"] = true
	}
}
