package executionapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"

	"github.com/codermaybe/ethguard/internal/jsonsafe"
)

// SourceMetadata identifies original extraction inputs. Checksums prove byte
// identity; official provenance still requires review of the source acquisition.
type SourceMetadata struct {
	Repository                string            `json:"repository"`
	Version                   string            `json:"version"`
	Commit                    string            `json:"commit"`
	Category                  string            `json:"category"`
	DereferencedOpenRPCSHA256 string            `json:"dereferencedOpenRPCSHA256"`
	Bundles                   map[string]string `json:"bundles"`
	VectorManifestSHA256      string            `json:"vectorManifestSHA256"`
	Build                     SourceBuild       `json:"build"`
}

type SourceBuild struct {
	Tool      string   `json:"tool"`
	Commit    string   `json:"commit"`
	Arguments []string `json:"arguments"`
	GoVersion string   `json:"goVersion"`
}

type InventoryMethod struct {
	Name                       string   `json:"name"`
	OpenRPCPointer             string   `json:"openrpcPointer"`
	ParameterDescriptorsSHA256 string   `json:"parameterDescriptorsSHA256"`
	ResultSchemaSHA256         string   `json:"resultSchemaSHA256"`
	ParameterCount             int      `json:"parameterCount"`
	ParameterSchemaSHA256      []string `json:"parameterSchemaSHA256"`
}

type SourceInventory struct {
	Version string            `json:"version"`
	Methods []InventoryMethod `json:"methods"`
}

func CompactSHA256(raw []byte) (string, error) {
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return "", err
	}
	return digest(compact.Bytes()), nil
}

// ReadOriginalFrom reads a spec-root filesystem without consulting a Manifest
// or generated audit. Offline tools use this same assembler for candidates.
func ReadOriginalFrom(files fs.FS) (Bundle, error) {
	version, err := fs.ReadFile(files, "VERSION")
	if err != nil {
		return Bundle{}, err
	}
	pin := strings.TrimSpace(string(version))
	sourceRaw, err := fs.ReadFile(files, "SOURCE.json")
	if err != nil {
		return Bundle{}, err
	}
	var source SourceMetadata
	if err := decodeStrict(sourceRaw, &source); err != nil {
		return Bundle{}, fmt.Errorf("source metadata: %w", err)
	}
	if source.Version != pin || !isDigest(source.Commit, 20) || source.Version != source.Commit || !isDigest(source.DereferencedOpenRPCSHA256, 32) || source.Build.Commit != source.Commit || source.Build.Tool == "" || len(source.Build.Arguments) == 0 || source.Build.GoVersion == "" {
		return Bundle{}, fmt.Errorf("source metadata: invalid fixed source/build identity")
	}
	if (source.Category != "official" && source.Category != "fixture") || (source.Category == "official" && source.Repository != "https://github.com/ethereum/execution-apis") {
		return Bundle{}, fmt.Errorf("source metadata: invalid category or repository")
	}
	originals := map[string][]byte{}
	if len(source.Bundles) != 3 {
		return Bundle{}, fmt.Errorf("source metadata: expected methods, requests and complete inventory digests")
	}
	for _, name := range []string{"methods.json", "requests.json", "inventory.json"} {
		raw, err := fs.ReadFile(files, name)
		if err != nil {
			return Bundle{}, err
		}
		if !isDigest(source.Bundles[name], 32) || digest(raw) != source.Bundles[name] {
			return Bundle{}, fmt.Errorf("source %s: digest mismatch", name)
		}
		if err := jsonsafe.Validate(raw); err != nil {
			return Bundle{}, fmt.Errorf("source %s: %w", name, err)
		}
		originals[name] = raw
	}
	vectorManifest, err := fs.ReadFile(files, "vectors/SOURCE.json")
	if err != nil {
		return Bundle{}, err
	}
	if !isDigest(source.VectorManifestSHA256, 32) || digest(vectorManifest) != source.VectorManifestSHA256 {
		return Bundle{}, fmt.Errorf("source vectors/SOURCE.json: digest mismatch")
	}
	bundle, err := load(originals["methods.json"], pin)
	if err != nil {
		return Bundle{}, err
	}
	bundle.Requests, err = loadRequests(originals["requests.json"], pin)
	if err != nil {
		return Bundle{}, err
	}
	var inventory SourceInventory
	if err := decodeStrict(originals["inventory.json"], &inventory); err != nil {
		return Bundle{}, fmt.Errorf("inventory: %w", err)
	}
	if inventory.Version != pin || len(inventory.Methods) == 0 {
		return Bundle{}, fmt.Errorf("inventory identity or method set is empty")
	}
	available := map[string]InventoryMethod{}
	last := ""
	for _, method := range inventory.Methods {
		if method.Name == "" || method.Name <= last || !strings.HasPrefix(method.OpenRPCPointer, "/methods/") || !isDigest(method.ParameterDescriptorsSHA256, 32) || !isDigest(method.ResultSchemaSHA256, 32) || method.ParameterCount != len(method.ParameterSchemaSHA256) {
			return Bundle{}, fmt.Errorf("inventory invalid method %q", method.Name)
		}
		for _, sum := range method.ParameterSchemaSHA256 {
			if !isDigest(sum, 32) {
				return Bundle{}, fmt.Errorf("inventory invalid parameter digest: %s", method.Name)
			}
		}
		available[method.Name] = method
		last = method.Name
	}
	for method, raw := range bundle.Results {
		sum, _ := CompactSHA256(raw)
		if sum != available[method].ResultSchemaSHA256 {
			return Bundle{}, fmt.Errorf("result %s differs from official inventory", method)
		}
	}
	var descriptors struct{ Requests map[string]json.RawMessage }
	if err := json.Unmarshal(originals["requests.json"], &descriptors); err != nil {
		return Bundle{}, err
	}
	for method, params := range bundle.Requests {
		item, ok := available[method]
		descriptorSum, _ := CompactSHA256(descriptors.Requests[method])
		if descriptorSum != item.ParameterDescriptorsSHA256 {
			return Bundle{}, fmt.Errorf("request %s descriptors differ from official inventory", method)
		}
		if !ok || item.ParameterCount != len(params) {
			return Bundle{}, fmt.Errorf("request %s differs from official inventory", method)
		}
		for i, param := range params {
			sum, _ := CompactSHA256(param.Schema)
			if sum != item.ParameterSchemaSHA256[i] {
				return Bundle{}, fmt.Errorf("request %s parameter %d differs from official inventory", method, i)
			}
		}
	}
	bundle.Source = source
	bundle.Inventory = inventory.Methods
	return bundle, nil
}

func ReadFrom(files fs.FS) (Bundle, error) {
	bundle, err := ReadOriginalFrom(files)
	if err != nil {
		return Bundle{}, err
	}
	raw, err := fs.ReadFile(files, "SOURCE.json")
	if err != nil {
		return Bundle{}, err
	}
	if err := applyErrata(&bundle, raw, files); err != nil {
		return Bundle{}, err
	}
	return bundle, nil
}
