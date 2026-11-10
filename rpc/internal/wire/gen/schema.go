// Package main contains the concrete Go source generator for the wire package.
// Generator machinery is not part of the package surface at build time; see
// emit.go and model.go here, where the wire/RPC contract rules live.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// schema mirrors the JSON Schema subset used by the execution-apis bundle,
// parsed in document order so that property order survives into the model.
type schema struct {
	unsupported          []string
	additionalProperties json.RawMessage
	title                string
	typeVal              string // "object" | "string" | "array" | "boolean" | "number" | "null" | ""

	pattern  string
	constVal string
	hasConst bool

	minimum *json.Number
	maximum *json.Number

	properties []prop
	required   []string

	items    *schema
	compKind string // "oneOf" | "anyOf" | ""
	branches []*schema
}

// prop is one ordered property entry.
type prop struct {
	name   string
	schema *schema
}

// knownSchemaKeys lists the supported schema vocabulary. Unsupported semantics
// are retained on the node so the walker can reject them with the full path.
var knownSchemaKeys = map[string]bool{
	"title": true, "type": true, "pattern": true, "const": true,
	"minimum": true, "maximum": true,
	"properties": true, "required": true,
	"items": true, "oneOf": true, "anyOf": true, "allOf": true,
	"description": true, "additionalProperties": true, "$schema": true,
}

// parseSchema decodes one schema node from raw bytes, preserving the order of
// object properties as spelled in the official document.
func parseSchema(raw []byte) (*schema, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("empty schema node")
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	out, err := decodeSchema(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing content after schema node")
	}
	return out, nil
}

func decodeSchema(dec *json.Decoder) (*schema, error) {
	out := &schema{}
	first, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch first.(type) {
	case json.Delim:
		if first.(json.Delim) != '{' {
			return nil, fmt.Errorf("schema: expected object node, got %v", first)
		}
	case string, bool, nil, json.Number:
		out.unsupported = append(out.unsupported, "non-object schema")
		return out, nil
	default:
		return nil, fmt.Errorf("schema: unexpected token %v", first)
	}
	seen := map[string]bool{}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return nil, err
		}
		name, ok := key.(string)
		if !ok {
			return nil, fmt.Errorf("schema: non-string key")
		}
		if seen[name] {
			out.unsupported = append(out.unsupported, "duplicate "+name)
		}
		seen[name] = true
		switch name {
		case "title":
			if err := decodeString(dec, &out.title); err != nil {
				return nil, err
			}
		case "type":
			if err := decodeString(dec, &out.typeVal); err != nil {
				return nil, err
			}
		case "pattern":
			if err := decodeString(dec, &out.pattern); err != nil {
				return nil, err
			}
		case "const":
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				return nil, err
			}
			out.constVal, out.hasConst = string(raw), true
		case "minimum", "maximum":
			token, err := dec.Token()
			if err != nil {
				return nil, err
			}
			num, ok := token.(json.Number)
			if !ok {
				return nil, fmt.Errorf("schema: %s must be a JSON number", name)
			}
			if name == "minimum" {
				out.minimum = &num
			} else {
				out.maximum = &num
			}
		case "required":
			if err := decodeStringList(dec, &out.required); err != nil {
				return nil, err
			}
		case "items":
			node, err := parseRawLoop(dec)
			if err != nil {
				return nil, err
			}
			out.items = node
		case "properties":
			props, err := decodeProperties(dec)
			if err != nil {
				return nil, err
			}
			out.properties = props
		case "oneOf", "anyOf", "allOf":
			branches, err := decodeBranches(dec)
			if err != nil {
				return nil, err
			}
			if name == "allOf" || out.compKind != "" {
				out.unsupported = append(out.unsupported, name)
			}
			out.compKind = name
			out.branches = branches
		default:
			if !knownSchemaKeys[name] {
				out.unsupported = append(out.unsupported, name)
			}
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				return nil, err
			}
			if name == "additionalProperties" {
				out.additionalProperties = raw
				if string(raw) != "false" && string(raw) != "true" {
					out.unsupported = append(out.unsupported, name)
				}
			}
		}
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return out, nil
}

// parseRawLoop decodes one value with the same decoder and wraps it back into
// parseSchema, retaining ordered properties and exact numeric spellings.
func parseRawLoop(dec *json.Decoder) (*schema, error) {
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}
	return parseSchema(raw)
}

func decodeProperties(dec *json.Decoder) ([]prop, error) {
	if t, err := dec.Token(); err != nil {
		return nil, err
	} else if t != json.Delim('{') {
		return nil, fmt.Errorf("schema: properties must be an object")
	}
	var props []prop
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return nil, err
		}
		name, ok := key.(string)
		if !ok {
			return nil, fmt.Errorf("schema: non-string property name")
		}
		node, err := parseRawLoop(dec)
		if err != nil {
			return nil, err
		}
		props = append(props, prop{name: name, schema: node})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return props, nil
}

func decodeBranches(dec *json.Decoder) ([]*schema, error) {
	if t, err := dec.Token(); err != nil {
		return nil, err
	} else if t != json.Delim('[') {
		return nil, fmt.Errorf("schema: composition must be an array")
	}
	var out []*schema
	for dec.More() {
		node, err := parseRawLoop(dec)
		if err != nil {
			return nil, err
		}
		out = append(out, node)
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return out, nil
}

func decodeStringList(dec *json.Decoder, dst *[]string) error {
	if t, err := dec.Token(); err != nil {
		return err
	} else if t != json.Delim('[') {
		return fmt.Errorf("schema: required must be an array")
	}
	for dec.More() {
		var s string
		if err := dec.Decode(&s); err != nil {
			return err
		}
		*dst = append(*dst, s)
	}
	if _, err := dec.Token(); err != nil {
		return err
	}
	return nil
}

func decodeString(dec *json.Decoder, dst *string) error {
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("schema: %w", err)
	}
	return nil
}
