//go:build linux && shell_gga_canonical

package shellinstaller

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Authentic fixed upstream CLI consuming actual native-prepared configurations.
// Configuration presentation is not provider availability/authentication/AI review.
func TestUserGGAStageFrozenCLI(t *testing.T) {
	if testing.Short() {
		t.Skip("fixed external CLI integration")
	}
	archive, err := os.ReadFile("/frozen-gga-source.tgz")
	if err != nil {
		t.Fatal(err)
	}
	const pin = "fb681f48706be5ee8f7d1efd558fdfacbc7757aaf4ca9d8fd05fce4c44d7d004"
	const prefix = "gentleman-guardian-angel-1124c3672f082c56b033c4e23a30e95d0e8cd593/"
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	source := t.TempDir()
	for _, leaf := range []string{"bin/gga", "lib/providers.sh", "lib/cache.sh", "lib/pr_mode.sh"} {
		data, err := userToolMember(ctx, archive, pin, prefix+leaf)
		if err != nil {
			t.Fatal("refuse execution of unauthenticated source", err)
		}
		path := filepath.Join(source, leaf)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := userToolWrite(path, data, 0700); err != nil {
			t.Fatal(err)
		}
	}
	ansi := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	for _, choice := range []string{"on", "off", "absent"} {
		t.Run(choice, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "stage")
			profile := UserExperience{Persona: "unmanaged", BackgroundSubagents: "off"}
			if choice != "absent" {
				profile.GGA = choice
			}
			if choice == "on" {
				profile.GGAProvider = "codex"
			}
			if err := userPrepareInstallStage(ctx, root, "separate", &profile); err != nil {
				t.Fatal(err)
			}
			before, err := userTreeStamp(root)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(ctx, "/usr/bin/bash", filepath.Join(source, "bin/gga"), "config")
			cmd.Dir = filepath.Join(root, "project")
			cmd.Env = userEnvironment(root, filepath.Join(root, "prefix"), filepath.Join(root, "agent"))
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("actual GGA config: %v\n%s", err, output)
			}
			text := ansi.ReplaceAllString(string(output), "")
			provider := "Not configured"
			if choice == "on" {
				provider = "codex"
				if !strings.Contains(text, filepath.Join(root, "config/gga/config")) {
					t.Fatal("CLI did not read canonical private XDG config", text)
				}
			}
			if !regexp.MustCompile(`PROVIDER:\s+` + regexp.QuoteMeta(provider) + `(?:\s|$)`).MatchString(text) {
				t.Fatal("actual provider presentation differs", text)
			}
			after, err := userTreeStamp(root)
			if err != nil || before != after {
				t.Fatal("configuration readback mutated the stage", err)
			}
			t.Log("FROZEN_GGA_NATIVE_STAGE_PASS", choice, "no hooks/provider execution")
		})
	}
}
