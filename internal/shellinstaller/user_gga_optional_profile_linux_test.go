//go:build linux && amd64

package shellinstaller

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestUserGGAOptionalPassPreservesSupportedProfile(t *testing.T) {
	for _, choice := range []string{"", "off"} {
		t.Run("gga="+choice, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "stage")
			profile := DefaultUserExperience()
			profile.CodeGraph, profile.Context7, profile.GGA = "on", "on", choice
			before := profile
			if err := userPrepareInstallStage(context.Background(), root, "separate", &profile); err != nil {
				t.Fatal(err)
			}
			stamp, err := userTreeStamp(root)
			if err != nil {
				t.Fatal(err)
			}
			// userRunInstall calls this producer unconditionally after CodeGraph.
			// Nil context proves an unselected producer needs no acquisition client.
			if err := userGGAProvision(nil, root, "separate", &profile); err != nil {
				t.Fatal("disabled GGA blocks the selected CodeGraph/Context7 profile", err)
			}
			after, err := userTreeStamp(root)
			if err != nil || stamp != after || before != profile {
				t.Fatal("unselected producer changed stage or selection", err)
			}
			if _, err := os.Lstat(filepath.Join(root, "runtime/gga")); !os.IsNotExist(err) {
				t.Fatal("unselected producer exposed GGA", err)
			}
			profile.CodeGraph = "invalid"
			if err := userGGAProvision(nil, root, "separate", &profile); err == nil {
				t.Fatal("disabled producer silently accepts malformed raw selection")
			}
		})
	}
}
