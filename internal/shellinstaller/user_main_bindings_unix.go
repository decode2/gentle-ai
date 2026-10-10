//go:build linux || darwin

package shellinstaller

import (
	"context"
	"path/filepath"
)

// Preflight unpublished Main against verified modern Separate stock. This is
// not API qualification, activation, or permission to skip later stock checks.
func userVerifyMainBindings(ctx context.Context, root, finalRoot string, artifact userSnapshotArtifact) error {
	if err := userCompareMainSource(ctx, root, artifact); err != nil {
		return err
	}
	if err := userVerifyGlobalAction(ctx, root, filepath.Join(root, "prefix"), filepath.Join(root, "agent"),
		filepath.Join(finalRoot, "prefix"), finalRoot, "separate", "verify-main-source"); err != nil {
		return err
	}
	return userCompareMainSource(ctx, root, artifact)
}
