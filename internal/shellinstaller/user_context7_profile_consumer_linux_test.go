//go:build linux && amd64 && shell_context7_canonical

package shellinstaller

import (
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Actual fixed-endpoint CLI admission/readback; offline ON must report failure,
// never service health. No endpoint/proxy/DNS/TLS rewrite or new MCP client.
func TestUserContext7SelectedProfileFrozenCLI(t *testing.T) {
	if testing.Short() {
		t.Skip("authenticated external CLI fixture")
	}
	lockData, err := os.ReadFile("/source/scripts/user-locks/modern/package-lock.json")
	if err != nil {
		t.Fatal(err)
	}
	var lock struct {
		Packages map[string]struct{ Version, Resolved, Integrity string }
	}
	if err := json.Unmarshal(lockData, &lock); err != nil {
		t.Fatal(err)
	}
	pin := lock.Packages["node_modules/@earendil-works/pi-coding-agent"]
	archive, err := os.ReadFile("/frozen-pi.tgz")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha512.Sum512(archive)
	if pin.Version != "1.0.0" || pin.Resolved != "https://registry.npmjs.org/@earendil-works/pi-coding-agent/-/pi-coding-agent-1.0.0.tgz" || pin.Integrity != "sha512-"+base64.StdEncoding.EncodeToString(digest[:]) {
		t.Fatal("CLI differs from authenticated committed lock")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	pkg := t.TempDir()
	cmd := exec.CommandContext(ctx, "/usr/bin/tar", "-xzf", "/frozen-pi.tgz", "--strip-components=1", "-C", pkg)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	if err := os.Symlink("/node_modules/@earendil-works/pi-coding-agent/node_modules", filepath.Join(pkg, "node_modules")); err != nil {
		t.Fatal(err)
	}
	for _, choice := range []string{"on", "off"} {
		t.Run(choice, func(t *testing.T) {
			root := installStagePath(t)
			profile := DefaultUserExperience()
			profile.Context7 = choice
			if token, err := InspectUserInstall(UserInstallRequest{Destination: root, Mode: "separate", Experience: &profile}); err != nil || token == "" {
				t.Fatal(token, err)
			}
			if err := userPrepareInstallStage(ctx, root, "separate", &profile); err != nil {
				t.Fatal(err)
			}
			config := filepath.Join(root, "agent/mcp.json")
			before, err := os.Lstat(config)
			if err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(config)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(ctx, "/opt/node/bin/node", filepath.Join(pkg, "dist/bundle/cli.js"), "mcp", "list", "--json")
			cmd.Dir, cmd.Env = filepath.Join(root, "project"), append(userEnvironment(root, filepath.Join(root, "prefix"), filepath.Join(root, "agent")), "GENTLE_AI_TELEMETRY_DISABLE=1")
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			output, runErr := cmd.Output()
			if stderr.Len() != 0 {
				t.Log("Guest stderr (not live TLS qualification):", stderr.String())
			}
			if ctx.Err() != nil {
				t.Fatal("CLI did not settle", ctx.Err(), string(output))
			}
			var report struct {
				Servers []struct {
					Name, State, Scope, Source, Exposure, Error, Transport string
					Enabled                                                bool
					Tools                                                  []string
				}
				Errors []json.RawMessage
			}
			if err := json.Unmarshal(output, &report); err != nil {
				t.Fatal(err, string(output))
			}
			if len(report.Servers) != 1 || len(report.Errors) != 0 {
				t.Fatal(string(output))
			}
			s := report.Servers[0]
			if s.Name != "context7" || s.Scope != "global" || s.Source != config || s.Transport != "https://mcp.context7.com/mcp" || s.Exposure != "codemode" || s.Enabled != (choice == "on") || len(s.Tools) != 0 {
				t.Fatal("selected canonical registration changed", string(output))
			}
			if choice == "on" {
				var exit *exec.ExitError
				if !errors.As(runErr, &exit) || exit.ExitCode() != 1 || s.Error == "" || s.State == "connected" {
					t.Fatal("offline ON incorrectly healthy", runErr, string(output))
				}
			} else if runErr != nil || s.State != "disabled" || s.Error != "" {
				t.Fatal("OFF failed", runErr, string(output))
			}
			after, err := os.Lstat(config)
			if err != nil || privateStamp(before) != privateStamp(after) {
				t.Fatal("readback changed canonical config", err)
			}
			readback, err := os.ReadFile(config)
			if err != nil || !bytes.Equal(original, readback) {
				t.Fatal("canonical bytes changed", err)
			}
			t.Logf("FROZEN_CONTEXT7_PROFILE_CLI choice=%s report=%s", choice, output)
		})
	}
}
