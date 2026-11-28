package livecanary

import (
	"context"
	"fmt"
)

// Rerun executes the plan and membership fixed by an existing manifest. It is
// the replication path for T10.7: the manifest bytes remain the sole basis and
// no Chainlist acquisition is allowed. The returned report therefore carries
// the original run_id and manifest digest; only its observation window differs.
func Rerun(ctx context.Context, manifestBytes []byte, executor Executor) (Report, error) {
	manifest, err := ParseManifest(manifestBytes)
	if err != nil {
		return Report{}, fmt.Errorf("rerun: %w", err)
	}
	membership := MembershipFixed
	switch {
	case manifest.Registry.Status != "ok" || manifest.Selection == nil:
		membership = MembershipRegistryUnavailable
	case len(manifest.Selection.Selected) < MinMembers:
		membership = MembershipInsufficient
	}
	return executor.Run(ctx, RunInput{
		Manifest:       *manifest,
		ManifestBytes:  manifestBytes,
		ManifestSHA256: ManifestSHA256(manifestBytes),
		Membership:     membership,
	})
}
