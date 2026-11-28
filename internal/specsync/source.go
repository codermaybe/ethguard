// Package specsync stages reproducible upstream upgrades without changing the
// active checkout. Source acquisition and offline extraction are separate.
package specsync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/codermaybe/ethguard/internal/executionapi"
	"github.com/codermaybe/ethguard/internal/jsonsafe"
)

const Repository = "https://github.com/ethereum/execution-apis"

var officialArguments = []string{"-deref", "-schemas", "src/schemas", "-schemas", "src/engine/openrpc/schemas", "-methods", "src/eth", "-methods", "src/debug", "-methods", "src/txpool", "-methods", "src/engine/openrpc/methods", "-methods", "src/testing", "-error-groups", "src/error-groups"}

type Inputs struct{ Commit, UpstreamDir, OpenRPC, VectorsDir, Source string }

func (i Inputs) Mode() (string, error) {
	modes := 0
	mode := ""
	if i.Commit != "" {
		modes++
		mode = "online-commit"
	}
	if i.UpstreamDir != "" {
		modes++
		mode = "offline-checkout"
	}
	if i.OpenRPC != "" || i.VectorsDir != "" || i.Source != "" {
		modes++
		mode = "offline-materials"
		if i.OpenRPC == "" || i.VectorsDir == "" || i.Source == "" {
			return "", fmt.Errorf("offline materials require -openrpc, -vectors-dir and -source")
		}
	}
	if modes != 1 {
		return "", fmt.Errorf("select exactly one source input mode")
	}
	if mode == "online-commit" && !validCommit(i.Commit) {
		return "", fmt.Errorf("-commit must be a complete lowercase commit hash")
	}
	return mode, nil
}

type VectorSource struct {
	Repository              string            `json:"repository"`
	Version                 string            `json:"version"`
	Commit                  string            `json:"commit"`
	UpstreamVectorDirectory string            `json:"upstreamVectorDirectory"`
	Vectors                 map[string]string `json:"vectors"`
}

// MaterialSource carries the complete supplied material inventory. Official
// material mode accepts only the already reviewed active source identity;
// a different official commit must be obtained through a verified checkout.
type MaterialSource struct {
	Source  executionapi.SourceMetadata `json:"source"`
	Vectors VectorSource                `json:"vectors"`
}
type materials struct {
	OpenRPC      []byte
	Vectors      map[string][]byte
	Source       executionapi.SourceMetadata
	VectorSource VectorSource
	Mode         string
}

func readJSON(path string, out any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := jsonsafe.Validate(raw); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	return nil
}
func hash(raw []byte) string { return fmt.Sprintf("%x", sha256.Sum256(raw)) }
func validCommit(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 20 && value == strings.ToLower(value)
}

func offlineEnvironment() []string {
	blocked := map[string]string{"GOTOOLCHAIN": "local", "GOPROXY": "off", "GOSUMDB": "off", "GONOSUMDB": "*", "GOVCS": "*:off", "GOTELEMETRY": "off"}
	return environment(blocked)
}
func environment(blocked map[string]string) []string {
	env := []string{}
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if _, replace := blocked[key]; !replace {
			env = append(env, value)
		}
	}
	keys := make([]string, 0, len(blocked))
	for k := range blocked {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+blocked[k])
	}
	return env
}
func command(ctx context.Context, dir string, offline bool, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if offline {
		cmd.Env = offlineEnvironment()
	} else {
		// Even online acquisition uses the explicitly selected installed Go.
		// Module downloads may be online; toolchain downloads are never implicit.
		cmd.Env = environment(map[string]string{"GOTOOLCHAIN": "local"})
	}
	raw, err := cmd.CombinedOutput()
	if err != nil {
		return raw, fmt.Errorf("%s %s: %w: %s", filepath.Base(name), strings.Join(args, " "), err, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}
func acquire(ctx context.Context, input Inputs, workspace, temp string, manifest executionapi.Manifest, toolchain GoToolchain) (materials, error) {
	mode, err := input.Mode()
	if err != nil {
		return materials{}, err
	}
	if mode == "offline-materials" {
		return readMaterials(input, workspace, mode)
	}
	checkout := input.UpstreamDir
	if mode == "online-commit" {
		checkout = filepath.Join(temp, "upstream")
		if err := os.Mkdir(checkout, 0755); err != nil {
			return materials{}, err
		}
		for _, args := range [][]string{{"init", "--quiet"}, {"remote", "add", "origin", Repository}, {"-c", "core.hooksPath=/dev/null", "fetch", "--quiet", "--depth=1", "origin", input.Commit}, {"-c", "core.hooksPath=/dev/null", "checkout", "--quiet", "--detach", "FETCH_HEAD"}} {
			if _, err := command(ctx, checkout, false, "git", args...); err != nil {
				return materials{}, err
			}
		}
	}
	checkout, err = filepath.Abs(checkout)
	if err != nil {
		return materials{}, err
	}
	commitRaw, err := command(ctx, checkout, true, "git", "rev-parse", "HEAD")
	if err != nil {
		return materials{}, err
	}
	commit := strings.TrimSpace(string(commitRaw))
	if !validCommit(commit) || (input.Commit != "" && commit != input.Commit) {
		return materials{}, fmt.Errorf("checkout identity mismatch")
	}
	remote, err := command(ctx, checkout, true, "git", "remote", "get-url", "origin")
	if err != nil {
		return materials{}, err
	}
	origin := strings.TrimSuffix(strings.TrimSpace(string(remote)), ".git")
	if origin != Repository && origin != "git@github.com:ethereum/execution-apis" {
		return materials{}, fmt.Errorf("checkout origin is not official execution-apis")
	}
	status, err := command(ctx, checkout, true, "git", "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return materials{}, err
	}
	if len(bytes.TrimSpace(status)) != 0 {
		return materials{}, fmt.Errorf("upstream checkout must have clean tracked and untracked inputs")
	}
	binary := filepath.Join(temp, "specgen")
	offline := mode != "online-commit"
	if _, err := command(ctx, filepath.Join(checkout, "tools"), offline, toolchain.Path, "build", "-o", binary, "./cmd/specgen"); err != nil {
		return materials{}, err
	}
	output := filepath.Join(temp, "openrpc.json")
	args := append([]string{"-o", output}, officialArguments...)
	if _, err := command(ctx, checkout, offline, binary, args...); err != nil {
		return materials{}, err
	}
	raw, err := os.ReadFile(output)
	if err != nil {
		return materials{}, err
	}
	vectors := map[string][]byte{}
	for _, method := range manifest.ResultMethods {
		dir := filepath.Join(checkout, "tests", method)
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return materials{}, err
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".io") {
				continue
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return materials{}, fmt.Errorf("upstream vector symlink is not supported")
			}
			data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				return materials{}, err
			}
			vectors[method+"/"+entry.Name()] = data
		}
	}
	source := executionapi.SourceMetadata{Repository: Repository, Version: commit, Commit: commit, Category: "official", DereferencedOpenRPCSHA256: hash(raw), Build: executionapi.SourceBuild{Tool: "tools/cmd/specgen", Commit: commit, Arguments: append([]string(nil), officialArguments...), GoVersion: toolchain.Version}}
	vectorSource := VectorSource{Repository: Repository, Version: commit, Commit: commit, UpstreamVectorDirectory: "tests", Vectors: map[string]string{}}
	for path, raw := range vectors {
		vectorSource.Vectors[path] = hash(raw)
	}
	return materials{raw, vectors, source, vectorSource, mode}, nil
}
func readMaterials(input Inputs, workspace, mode string) (materials, error) {
	var metadata MaterialSource
	if err := readJSON(input.Source, &metadata); err != nil {
		return materials{}, fmt.Errorf("material source: %w", err)
	}
	source := metadata.Source
	if !validCommit(source.Commit) || source.Version != source.Commit || (source.Category != "official" && source.Category != "fixture") || source.Build.Commit != source.Commit || source.Build.Tool == "" || len(source.Build.Arguments) == 0 || source.Build.GoVersion == "" {
		return materials{}, fmt.Errorf("invalid material source/build identity")
	}
	if metadata.Vectors.Version != source.Version || metadata.Vectors.Commit != source.Commit || metadata.Vectors.Repository != source.Repository || metadata.Vectors.UpstreamVectorDirectory != "tests" {
		return materials{}, fmt.Errorf("vector/source identity mismatch")
	}
	raw, err := os.ReadFile(input.OpenRPC)
	if err != nil {
		return materials{}, err
	}
	if hash(raw) != source.DereferencedOpenRPCSHA256 {
		return materials{}, fmt.Errorf("OpenRPC digest mismatch")
	}
	if source.Category == "official" {
		var trusted executionapi.SourceMetadata
		var trustedVectors VectorSource
		if err := readJSON(filepath.Join(workspace, "internal/spec/SOURCE.json"), &trusted); err != nil {
			return materials{}, err
		}
		if err := readJSON(filepath.Join(workspace, "internal/spec/vectors/SOURCE.json"), &trustedVectors); err != nil {
			return materials{}, err
		}
		if source.Repository != Repository || trusted.Category != "official" || source.Commit != trusted.Commit || source.DereferencedOpenRPCSHA256 != trusted.DereferencedOpenRPCSHA256 {
			return materials{}, fmt.Errorf("official offline materials are not the already reviewed source; use a verified local checkout for a new commit")
		}
		if !equalJSON(metadata.Vectors, trustedVectors) || !equalJSON(source.Build, trusted.Build) {
			return materials{}, fmt.Errorf("official offline vector/build metadata differs from reviewed provenance")
		}
	}
	files := os.DirFS(input.VectorsDir)
	vectors := map[string][]byte{}
	if err := fs.WalkDir(files, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("vector symlinks are forbidden: %s", path)
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".io") {
			return nil
		}
		raw, err := fs.ReadFile(files, path)
		if err != nil {
			return err
		}
		if metadata.Vectors.Vectors[path] != hash(raw) {
			return fmt.Errorf("unlisted or changed vector: %s", path)
		}
		vectors[path] = raw
		return nil
	}); err != nil {
		return materials{}, err
	}
	if len(vectors) != len(metadata.Vectors.Vectors) {
		return materials{}, fmt.Errorf("missing vector material")
	}
	for path := range metadata.Vectors.Vectors {
		if !fs.ValidPath(path) || !strings.HasSuffix(path, ".io") {
			return materials{}, fmt.Errorf("invalid vector inventory path: %s", path)
		}
	}
	return materials{raw, vectors, source, metadata.Vectors, mode}, nil
}
func equalJSON(a, b any) bool {
	ar, err := json.Marshal(a)
	if err != nil {
		return false
	}
	br, err := json.Marshal(b)
	return err == nil && bytes.Equal(ar, br)
}
