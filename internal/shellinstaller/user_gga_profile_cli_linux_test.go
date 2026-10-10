//go:build linux && amd64 && shell_gga_canonical

package shellinstaller

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Original authenticated CLI consuming relocated canonical selection through
// the real launch environment/group, not userLaunch/installed acceptance.
func TestUserGGASelectedProfileRelocatedCLI(t *testing.T) {
	if testing.Short() {
		t.Skip("authenticated external CLI integration")
	}
	archive, err := os.ReadFile("/frozen-gga-source.tgz")
	if err != nil {
		t.Fatal(err)
	}
	for _, choice := range []string{"on", "off"} {
		t.Run(choice, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			parent := t.TempDir()
			root, destination := filepath.Join(parent, "stage"), filepath.Join(parent, "selected-final")
			profile := DefaultUserExperience()
			profile.CodeGraph, profile.Context7, profile.GGA = "on", "on", choice
			if choice == "on" {
				profile.GGAProvider = "codex"
			}
			beforeProfile := profile
			if token, err := InspectUserInstall(UserInstallRequest{Destination: destination, Mode: "separate", Experience: &profile}); err != nil || token == "" {
				t.Fatal(token, err)
			}
			if err := userPrepareInstallStage(ctx, root, "separate", &profile); err != nil {
				t.Fatal(err)
			}
			if choice == "on" {
				if err := os.Mkdir(filepath.Join(root, "runtime"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := userStageGGAExecutable(ctx, root, archive); err != nil {
					t.Fatal(err)
				}
			} else if err := userGGAProvision(nil, root, "separate", &profile); err != nil {
				t.Fatal("OFF attempted acquisition", err)
			}
			if err := os.Rename(root, destination); err != nil {
				t.Fatal(err)
			}
			root = destination
			before, err := userTreeStamp(root)
			if err != nil {
				t.Fatal(err)
			}
			manifest := userManifest{Mode: "separate", GGA: choice == "on", CodeGraph: true, Agent: filepath.Join(root, "agent"), Prefix: filepath.Join(root, "prefix")}
			planned := userLaunchCommand(ctx, root, manifest, "/not-executed/cli.js", nil)
			if planned.Err != nil {
				t.Fatal(planned.Err)
			}
			private := filepath.Join(root, "runtime/gga/bin/gga")
			// No Node/supervisor replacement. This executes the actual source CLI
			// via ordinary shell lookup; GGA is not an invented SDK tool.
			script := `resolved=$(command -v gga || true); test "$resolved" != "$1"`
			if choice == "on" {
				script = `test "$(command -v gga)" = "$1" && gga --version && gga config`
			}
			cmd := exec.CommandContext(ctx, "/usr/bin/bash", "-c", script, "probe", private)
			cmd.Dir, cmd.Env = filepath.Join(root, "project"), planned.Env
			var output bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &output
			readbacks := 0
			if err := userLaunchGroup(cmd, nil, func(waitErr error) error {
				readbacks++
				return userGGAExitReadback(context.Background(), root, manifest.GGA, waitErr)
			}); err != nil {
				t.Fatal("actual CLI/group/readback", err, output.String())
			}
			if readbacks != 1 || ctx.Err() != nil {
				t.Fatal("group did not settle", readbacks, ctx.Err())
			}
			if choice == "on" && (!strings.Contains(output.String(), "gga v2.10.1") || !strings.Contains(output.String(), "codex") || !strings.Contains(output.String(), filepath.Join(root, "config/gga/config"))) {
				t.Fatal("selected CLI/config absent", output.String())
			}
			if choice == "off" {
				if _, err := os.Lstat(filepath.Dir(private)); !os.IsNotExist(err) {
					t.Fatal("OFF exposed private payload", err)
				}
			}
			after, err := userTreeStamp(root)
			if err != nil || before != after || profile != beforeProfile {
				t.Fatal("CLI changed stage or profile", err)
			}
			t.Log("SELECTED_GGA_RELOCATED_CLI_PASS", choice, "no provider/hook/model execution", output.String())
		})
	}
}
