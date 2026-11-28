package ethguard

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Run the production candidate gate with local git/gh responses. The real jq
// checks release responses; no remote calls or publication are possible.
func TestReleaseCandidateGate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the release candidate gate runs on an Ubuntu runner")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("release candidate gate requires bash")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("release candidate gate requires jq")
	}
	script, err := os.ReadFile("scripts/release_candidate.sh")
	if err != nil {
		t.Fatal(err)
	}
	const sha = "cdb8f2b9b0b321136d1d6bf2245dce543b7c7693"
	type response struct {
		out  string
		err  string
		code int
	}
	tests := []struct {
		name       string
		version    string
		endpoint   string
		response   response
		existing   string
		wantError  string
		wantCause  string
		noPrevious bool
	}{
		{name: "candidate without environment"},
		{name: "first release", endpoint: "releases", response: response{out: `[[]]`}, noPrevious: true},
		{name: "stable version", version: "v1.2.3"},
		{name: "numeric prerelease zero", version: "v1.2.3-0"},
		{name: "missing version prefix", version: "0.0.1-beta.4", wantError: "v-prefixed SemVer"},
		{name: "build metadata rejected", version: "v1.2.3+build", wantError: "v-prefixed SemVer"},
		{name: "numeric leading zero", version: "v1.2.3-beta.04", wantError: "leading zeros"},
		{name: "checkout mismatch", endpoint: "head", response: response{out: strings.Repeat("0", 40)}, wantError: "checkout HEAD"},
		{name: "checkout read failure", endpoint: "head", response: response{err: "fatal: not a git repository", code: 128}, wantError: "read checkout HEAD", wantCause: "not a git repository"},
		{name: "dirty checkout", endpoint: "status", response: response{out: "?? unexpected-file"}, wantError: "checkout must be clean"},
		{name: "status read failure", endpoint: "status", response: response{err: "fatal: unreadable index", code: 128}, wantError: "read checkout status", wantCause: "unreadable index"},
		{name: "existing build directory", existing: "dist", wantError: "dist already exists"},
		{name: "existing evidence directory", existing: "release-evidence", wantError: "release-evidence already exists"},
		{name: "master advanced", endpoint: "master", response: response{out: strings.Repeat("1", 40)}, wantError: "master has moved"},
		{name: "master API denied", endpoint: "master", response: response{err: "gh: Resource not accessible by integration (HTTP 403)", code: 1}, wantError: "read remote master", wantCause: "HTTP 403"},
		{name: "existing tag", endpoint: "tag", response: response{out: sha + "\trefs/tags/v0.0.1-beta.4"}, wantError: "tag v0.0.1-beta.4 already exists"},
		{name: "tag lookup failed", endpoint: "tag", response: response{err: "fatal: remote unavailable", code: 128}, wantError: "look up remote tag", wantCause: "remote unavailable"},
		{name: "release API denied", endpoint: "releases", response: response{err: "gh: Resource not accessible by integration (HTTP 403)", code: 1}, wantError: "list existing releases", wantCause: "HTTP 403"},
		{name: "draft release exists", endpoint: "releases", response: response{out: `[[{"tag_name":"v0.0.1-beta.4","draft":true}]]`}, wantError: "release v0.0.1-beta.4 already exists"},
		{name: "release on later page exists", endpoint: "releases", response: response{out: `[[{"tag_name":"v0.0.1-beta.2","draft":false}],[{"tag_name":"v0.0.1-beta.4","draft":false}]]`}, wantError: "release v0.0.1-beta.4 already exists"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			bin := filepath.Join(root, "bin")
			fixture := filepath.Join(root, "fixture")
			work := filepath.Join(root, "work")
			for _, dir := range []string{bin, fixture, work} {
				if err := os.Mkdir(dir, 0755); err != nil {
					t.Fatal(err)
				}
			}
			write := func(path string, data []byte, mode os.FileMode) {
				t.Helper()
				if err := os.WriteFile(path, data, mode); err != nil {
					t.Fatal(err)
				}
			}
			responses := map[string]response{
				"head":     {out: sha},
				"status":   {},
				"master":   {out: sha},
				"tag":      {code: 2},
				"releases": {out: `[[{"tag_name":"v0.0.1-beta.2","draft":false}]]`},
			}
			if tt.endpoint != "" {
				responses[tt.endpoint] = tt.response
			}
			for name, response := range responses {
				write(filepath.Join(fixture, name+".out"), []byte(response.out), 0644)
				write(filepath.Join(fixture, name+".err"), []byte(response.err), 0644)
				write(filepath.Join(fixture, name+".code"), fmt.Appendf(nil, "%d\n", response.code), 0644)
			}
			for _, tool := range []string{"git", "gh"} {
				write(filepath.Join(bin, tool), []byte("#!"+bash+"\n"+releaseCandidateStub), 0755)
			}
			if tt.existing != "" {
				if err := os.Mkdir(filepath.Join(work, tt.existing), 0755); err != nil {
					t.Fatal(err)
				}
			}
			version := tt.version
			if version == "" {
				version = "v0.0.1-beta.4"
			}
			scriptPath := filepath.Join(root, "candidate.sh")
			write(scriptPath, script, 0644)
			outputPath := filepath.Join(root, "github-output")
			summaryPath := filepath.Join(root, "github-summary")
			cmd := exec.CommandContext(t.Context(), bash, scriptPath)
			cmd.Dir = work
			// Do not pass real credentials to subprocesses. Unexpected git/gh
			// operations fail inside the stub instead of reaching the network.
			cmd.Env = []string{
				"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
				"VERSION=" + version, "EXPECTED_SHA=" + sha,
				"GITHUB_REPOSITORY=fixture/ethguard", "GH_TOKEN=fixture-only",
				"GITHUB_OUTPUT=" + outputPath, "GITHUB_STEP_SUMMARY=" + summaryPath,
				"REPLAY_FIXTURE=" + fixture,
			}
			output, runErr := cmd.CombinedOutput()
			commitPath := filepath.Join(work, "release-evidence", "commit.txt")
			if tt.wantError != "" {
				if runErr == nil || !strings.Contains(string(output), "::error::") || !strings.Contains(string(output), tt.wantError) || !strings.Contains(string(output), tt.wantCause) {
					t.Fatalf("expected actionable rejection %q and cause %q, got %v: %s", tt.wantError, tt.wantCause, runErr, output)
				}
				for _, path := range []string{outputPath, summaryPath, commitPath} {
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatalf("failed candidate wrote success evidence %s: %v", path, err)
					}
				}
				return
			}
			if runErr != nil {
				t.Fatalf("candidate failed: %v: %s", runErr, output)
			}
			previous := "v0.0.1-beta.2"
			if tt.noPrevious {
				previous = ""
			}
			for path, want := range map[string]string{
				outputPath: fmt.Sprintf("sha=%s\nversion=%s\nprevious=%s\n", sha, version, previous),
				commitPath: sha + "\n",
			} {
				got, err := os.ReadFile(path)
				if err != nil || string(got) != want {
					t.Fatalf("candidate evidence %s: got %q, want %q: %v", path, got, want, err)
				}
			}
		})
	}
}

const releaseCandidateStub = `set -euo pipefail
case "${0##*/}:$*" in
  'git:rev-parse HEAD') response=head ;;
  'git:status --porcelain --untracked-files=all') response=status ;;
  'git:ls-remote --exit-code --tags https://github.com/fixture/ethguard.git refs/tags/'*) response=tag ;;
  'gh:api repos/fixture/ethguard/git/ref/heads/master --jq .object.sha') response=master ;;
  'gh:api repos/fixture/ethguard/releases?per_page=100 --paginate --slurp') response=releases ;;
  *) printf 'unexpected release tool invocation: %s %s\n' "$0" "$*" >&2; exit 97 ;;
esac
cat "$REPLAY_FIXTURE/$response.out"
cat "$REPLAY_FIXTURE/$response.err" >&2
read -r status < "$REPLAY_FIXTURE/$response.code"
exit "$status"
`
