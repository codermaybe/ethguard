package builder

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/codermaybe/ethguard/internal/executionapi"
	"github.com/codermaybe/ethguard/internal/jsonsafe"
)

type Artifacts struct{ Methods, Requests, Inventory []byte }

// BuildSelected is the only production extraction policy entry. Both the raw
// bundle selector and compiler consume this same explicitly loaded manifest.
func BuildSelected(openRPC []byte, version string, manifest executionapi.Manifest) (Artifacts, error) {
	if err := manifest.Validate(); err != nil {
		return Artifacts{}, err
	}
	if err := jsonsafe.Validate(openRPC); err != nil {
		return Artifacts{}, err
	}
	methods, err := Build(openRPC, version, manifest.ResultMethods)
	if err != nil {
		return Artifacts{}, err
	}
	requests, err := BuildRequests(openRPC, version, manifest.RequestInventoryPrefixes...)
	if err != nil {
		return Artifacts{}, err
	}
	inventory, err := BuildInventory(openRPC, version)
	if err != nil {
		return Artifacts{}, err
	}
	return Artifacts{methods, requests, inventory}, nil
}

func BuildInventory(openRPC []byte, version string) ([]byte, error) {
	var document struct {
		Methods []struct {
			Name   string
			Result struct{ Schema json.RawMessage }
			Params json.RawMessage
		}
	}
	if err := json.Unmarshal(openRPC, &document); err != nil {
		return nil, err
	}
	inventory := executionapi.SourceInventory{Version: version, Methods: []executionapi.InventoryMethod{}}
	seen := map[string]bool{}
	for index, method := range document.Methods {
		if method.Name == "" || seen[method.Name] {
			return nil, fmt.Errorf("duplicate or unnamed upstream method %q", method.Name)
		}
		seen[method.Name] = true
		sum, err := executionapi.CompactSHA256(method.Result.Schema)
		if err != nil {
			return nil, fmt.Errorf("%s result: %w", method.Name, err)
		}
		var params []struct{ Schema json.RawMessage }
		if err := json.Unmarshal(method.Params, &params); err != nil {
			return nil, err
		}
		descriptorSum, err := executionapi.CompactSHA256(method.Params)
		if err != nil {
			return nil, err
		}
		item := executionapi.InventoryMethod{Name: method.Name, OpenRPCPointer: "/methods/" + strconv.Itoa(index), ParameterDescriptorsSHA256: descriptorSum, ResultSchemaSHA256: sum, ParameterCount: len(params), ParameterSchemaSHA256: []string{}}
		for _, param := range params {
			sum, err := executionapi.CompactSHA256(param.Schema)
			if err != nil {
				return nil, fmt.Errorf("%s param: %w", method.Name, err)
			}
			item.ParameterSchemaSHA256 = append(item.ParameterSchemaSHA256, sum)
		}
		inventory.Methods = append(inventory.Methods, item)
	}
	if version == "" || len(inventory.Methods) == 0 {
		return nil, fmt.Errorf("missing source inventory identity or methods")
	}
	sort.Slice(inventory.Methods, func(i, j int) bool { return inventory.Methods[i].Name < inventory.Methods[j].Name })
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetIndent("", " ")
	if err := enc.Encode(inventory); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
