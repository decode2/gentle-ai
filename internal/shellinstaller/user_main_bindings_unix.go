//go:build linux || darwin

package shellinstaller

import (
	"context"
	"path/filepath"
	"runtime"
)

// Preflight unpublished Main against verified modern Separate stock. This is
// not API qualification, activation, or permission to skip later stock checks.
func userVerifyMainBindings(ctx context.Context, root, finalRoot string, artifact userSnapshotArtifact) error {
	return userVerifyMainBindingsAction(ctx, root, finalRoot, artifact, "verify-main-source")
}

// Explicit SDK composition only: all three stock bindings stay authoritative.
// Neither this preflight nor the fixed plan activates source or installs Main.
func userVerifyMainSDKPreviewBindings(ctx context.Context, root, finalRoot string, artifact userSnapshotArtifact) error {
	return userVerifyMainBindingsAction(ctx, root, finalRoot, artifact, "verify-main-sdk-source")
}

func userVerifyMainBindingsAction(ctx context.Context, root, finalRoot string, artifact userSnapshotArtifact, action string) error {
	if err := userCompareMainSource(ctx, root, artifact); err != nil {
		return err
	}
	if err := userVerifyGlobalAction(ctx, root, filepath.Join(root, "prefix"), filepath.Join(root, "agent"),
		filepath.Join(finalRoot, "prefix"), finalRoot, "separate", action); err != nil {
		return err
	}
	return userCompareMainSource(ctx, root, artifact)
}

// The confirmed worker owns acquisition and partial-stage cleanup. Stock stays
// installed: only the private SDK declaration changes after authenticated DATA.
func userInstallMainSDK(ctx context.Context, root, finalRoot string, profile *UserExperience) (*userSnapshotArtifact, error) {
	if profile == nil || profile.SDKSource != "main-preview" {
		return nil, nil
	}
	artifact, data, err := userMainSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	if err := userStageMainArchive(ctx, root, artifact, data); err != nil {
		return nil, err
	}
	if err := userStageMainSource(ctx, root, artifact); err != nil {
		return nil, err
	}
	if err := userComposeMainSDK(ctx, root, finalRoot, artifact); err != nil {
		return nil, err
	}
	return &artifact, nil
}

func userComposeMainSDK(ctx context.Context, root, finalRoot string, artifact userSnapshotArtifact) error {
	for _, action := range []string{"materialize-main-sdk-source", "materialize-main-sdk-runtime", "select-main-sdk-package"} {
		if err := userVerifyMainBindingsAction(ctx, root, finalRoot, artifact, action); err != nil {
			return err
		}
	}
	return userVerifyMainBindingsAction(ctx, root, finalRoot, artifact, "verify-main-sdk-selected")
}

// Historical nil metadata keeps the exact ordinary stock verifier. A selected
// SDK never bypasses the supplier/graph checks or repairs settings drift.
func userVerifyInstalledGlobal(ctx context.Context, root string, manifest userManifest) error {
	if manifest.MainSDK == nil {
		return userVerifyGlobal(ctx, root, manifest.Prefix, manifest.Agent, manifest.Prefix, root, manifest.Mode)
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || manifest.Mode != "separate" || manifest.Destination != root || manifest.Prefix != filepath.Join(root, "prefix") || manifest.Agent != filepath.Join(root, "agent") || manifest.CodeGraph || manifest.GGA {
		return experienceError("Main SDK manifest requires qualified Linux amd64 Separate bindings")
	}
	return userVerifyMainBindingsAction(ctx, root, root, *manifest.MainSDK, "verify-main-sdk-selected")
}
