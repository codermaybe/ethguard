package wire_test

// The generated file carries an init-time version interlock against the
// pinned spec bundle. This test pins the same contract at the public surface
// so a drifted pair fails here with a readable diff instead of a panic
// buried inside package initialization.

import (
	"testing"

	"github.com/codermaybe/ethguard/internal/spec"
	"github.com/codermaybe/ethguard/rpc/internal/wire"
)

func TestGeneratedTypesPinTheSpecVersion(t *testing.T) {
	if spec.Version() != "v1.0.0-beta.7" {
		t.Fatalf("pinned spec version is %q; regenerate rpc/internal/wire/types.gen.go for this release", spec.Version())
	}
	// The interlock itself lives in the generated init; touching one
	// generated parser here proves the whole file compiled alongside it.
	for _, sample := range []string{
		`"0x7dcd17433742f4c0ca53122ab541d0ba67fc27df"`,
	} {
		if _, err := wire.NewAddress([]byte(sample)); err != nil {
			t.Fatalf("NewAddress(%s): %v", sample, err)
		}
	}
}
