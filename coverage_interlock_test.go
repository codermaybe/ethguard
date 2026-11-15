package ethguard

import (
	"os/exec"
	"sort"
	"strings"
	"testing"
)

// zeroDirectTestAllowlist is the test-inventory interlock: it lists every
// package currently maintained without direct *_test.go files, each with the
// reason its behavior is asserted elsewhere. It works like R4: the list is an
// audited boundary, not a garbage dump —
//
//   - a zero-test package missing from the list fails CI (unsanctioned gap),
//   - an entry whose package now carries tests fails CI (retire the entry),
//   - growing the list requires a reason line, so drift is a reviewable diff.
//
// Root-package integration tests are the designed coverage vehicle for
// several internal packages; this records that choice structurally instead
// of hoping it stays obvious.
var zeroDirectTestAllowlist = map[string]string{
	"github.com/codermaybe/ethguard/cmd/rpcsample":                 "CLI recording tool; exercised via internal/rpcsample tests",
	"github.com/codermaybe/ethguard/cmd/shapeprobe":                "CLI probe tool; exercised via internal/shapeprobe tests",
	"github.com/codermaybe/ethguard/cmd/soak":                      "load tool; driven manually during soak drills",
	"github.com/codermaybe/ethguard/cmd/specbundle":                "spec utility; output validated by internal/spec/builder",
	"github.com/codermaybe/ethguard/cmd/specdiff":                  "spec utility; output compared against pinned VERSION",
	"github.com/codermaybe/ethguard/examples/basic":                "README runnable demo",
	"github.com/codermaybe/ethguard/examples/migration":            "README runnable demo",
	"github.com/codermaybe/ethguard/internal/refcontext":           "per-invocation reference propagation covered via root anchor tests",
	"github.com/codermaybe/ethguard/internal/spec":                 "data-only embed accessor; validated by spec/builder and spec/conformance",
	"github.com/codermaybe/ethguard/rpc/internal/objects/coverage": "table coverage; enforced by the methods inventory interlock",
	"github.com/codermaybe/ethguard/rpc/internal/policy":           "shared admission decorator exercised through methods admission tests",
}

// TestPackageTestInventoryInterlock keeps the zero-direct-test set in sync
// with the allowlist in both directions.
func TestPackageTestInventoryInterlock(t *testing.T) {
	out, err := exec.Command("go", "list", "-f",
		"{{.ImportPath}}\t{{len .TestGoFiles}}\t{{len .XTestGoFiles}}", "./...").Output()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}

	var failures []string
	seen := 0
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 3 {
			t.Fatalf("unexpected go list output: %q", line)
		}
		pkg, testFiles, xtestFiles := fields[0], fields[1], fields[2]
		seen++
		zero := testFiles == "0" && xtestFiles == "0"

		reason, listed := zeroDirectTestAllowlist[pkg]
		switch {
		case zero && !listed:
			failures = append(failures, "unsanctioned zero-test package: "+pkg+
				" — add tests or sign the gap with a reason in zeroDirectTestAllowlist")
		case !zero && listed:
			failures = append(failures, "retire obsolete allowlist entry (package now has tests): "+pkg+
				" — remove it from zeroDirectTestAllowlist")
		case zero && listed && reason == "":
			failures = append(failures, "allowlist entry without reason: "+pkg)
		}
	}
	if seen == 0 {
		t.Fatal("go list returned no packages — inspect the working tree")
	}

	var listed []string
	for pkg := range zeroDirectTestAllowlist {
		listed = append(listed, pkg)
	}
	sort.Strings(listed)
	if len(listed) != len(zeroDirectTestAllowlist) {
		t.Fatal("allowlist contains duplicate keys") // defensive; map literal cannot
	}

	if len(failures) > 0 {
		t.Errorf("package test inventory drift:\n  %s", strings.Join(failures, "\n  "))
	}
}
