//go:build linux && amd64 && shell_main_owned_preflight

package shellinstaller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Qualification-only native/dependency exposure. Not an installed profile or
// selector: the composition source is verified again after removing overlays.
func userMainComposedSDKConsumer(t *testing.T, ctx context.Context, root string, artifact userSnapshotArtifact) {
	t.Helper()
	t.Cleanup(func() { userMainComposedAgentReadback(t, root) })
	main := filepath.Join(root, "runtime/main-sdk")
	modules := filepath.Join(root, "prefix/lib/node_modules")
	native := filepath.Join(main, ".gentle-ai")
	dependency := filepath.Join(root, "runtime/node_modules")
	for _, path := range []string{native, dependency} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("fixture overlay destination occupied", path, err)
		}
	}
	defer func() {
		if err := os.RemoveAll(native); err != nil {
			t.Error(err)
		}
		if err := os.Remove(dependency); err != nil && !os.IsNotExist(err) {
			t.Error(err)
		}
		// Retain any consumer timeout failure, but reserve bounded readback
		// time independently, including setup refusal or partial DATA copies.
		verifyCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := userVerifyMainBindingsAction(verifyCtx, root, root, artifact, "verify-main-sdk-composition"); err != nil {
			t.Error("source-only composition readback after SDK", err)
		}
	}()
	cmd := exec.CommandContext(ctx, "/usr/bin/cp", "-R", "--preserve=mode", "--no-dereference", filepath.Join(modules, "gentle-pi/.gentle-ai"), native)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatal("authenticated stock native DATA overlay", err, string(output))
	}
	if err := userNativeReadback(ctx, native); err != nil {
		t.Fatal("composed package-local native readback", err)
	}
	if err := os.Symlink(filepath.Join(modules, "gentle-pi/node_modules"), dependency); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"project", "home", "config"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	project := filepath.Join(root, "project")
	for _, name := range []string{"README.md", "AGENTS.md", "historical-openspec.md"} {
		if err := os.WriteFile(filepath.Join(project, name), []byte("private project preimage: "+name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd = exec.CommandContext(ctx, "/usr/bin/git", "init", "--quiet", "--template=", project)
	cmd.Env = []string{"HOME=" + filepath.Join(root, "home"), "PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1"}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	protected := []string{main, filepath.Join(project, ".git"), filepath.Join(project, "README.md"),
		filepath.Join(project, "AGENTS.md"), filepath.Join(project, "historical-openspec.md")}
	before := map[string]string{}
	for _, path := range protected {
		var err error
		before[path], err = userTreeStamp(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	cmd = exec.CommandContext(ctx, filepath.Join(root, "runtime/node/bin/node"), "/source/e2e/shell-main-sdk-composed.mjs", root, filepath.Join(modules, "@earendil-works/pi-coding-agent"))
	cmd.Dir = project
	cmd.Env = []string{"HOME=" + filepath.Join(root, "home"), "PATH=" + filepath.Join(root, "runtime/node/bin") + ":/usr/bin:/bin",
		"PI_CODING_AGENT_DIR=" + filepath.Join(root, "agent"), "GENTLE_PI_CONFIG_HOME=" + filepath.Join(root, "config"),
		"GIT_CONFIG_NOSYSTEM=1"}
	output, runErr := cmd.CombinedOutput()
	t.Log(string(output))
	for _, path := range protected {
		after, err := userTreeStamp(path)
		if err != nil || after != before[path] {
			t.Errorf("composed SDK changed protected DATA %s: %v", path, err)
		}
	}
	if err := ctx.Err(); err != nil {
		t.Fatal("composed SDK did not settle", err)
	}
	if runErr != nil {
		t.Fatal("actual composed SDK consumer refused", runErr)
	}
}

// Readback also runs after setup refusal or a killed consumer and fails closed
// on the first drift; it does not promise an exhaustive failed-state inventory.
// Only known partial package-owned additions may survive a failed consumer.
func userMainComposedAgentReadback(t *testing.T, root string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	files := map[string]string{}
	for _, name := range strings.Fields("gentle-ai-explore gentle-ai-verify gentle-ai-worker jd-fix-agent jd-judge-a jd-judge-b review-readability review-reliability review-resilience review-risk") {
		files["agents/"+name+".md"] = "assets/agents/" + name + ".md"
	}
	files["chains/4r-review.chain.md"] = "assets/chains/4r-review.chain.md"
	for _, name := range []string{"strict-tdd", "strict-tdd-verify"} {
		files["gentle-ai/support/"+name+".md"] = "assets/support/" + name + ".md"
	}
	dirs := map[string]bool{}
	for _, name := range strings.Fields(". agents chains gentle-agents gentle-agents/catalog gentle-agents/presence gentle-agents/transport gentle-agents/transport/presence gentle-ai gentle-ai/support") {
		dirs[name] = true
	}
	hashes := map[string]any{}
	contents := map[string][]byte{}
	read := func(path string) ([]byte, error) {
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		return userRetainedSource(ctx, path, info.Size())
	}
	for name, source := range files {
		data, err := read(filepath.Join(root, "runtime/main-sdk", source))
		if err != nil {
			t.Error(err)
			return
		}
		contents[name] = data
		hashes[name] = fmt.Sprintf("%x", sha256.Sum256(data))
	}
	err := filepath.WalkDir(filepath.Join(root, "agent"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		name, err := filepath.Rel(filepath.Join(root, "agent"), path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if entry.IsDir() && dirs[name] && info.Mode() == os.ModeDir|0700 && info.Sys().(*syscall.Stat_t).Uid == 1000 {
			return nil
		}
		data, err := read(path)
		if err != nil {
			return err
		}
		if name == "settings.json" || (contents[name] != nil && bytes.Equal(data, contents[name])) {
			return nil // Settings preimage is independently checked by the parent.
		}
		var manifest any
		if name == "gentle-ai/managed-assets.json" && json.Unmarshal(data, &manifest) == nil && reflect.DeepEqual(manifest, map[string]any{"schemaVersion": float64(1), "assets": hashes}) {
			return nil
		}
		return fmt.Errorf("unexpected or changed composed SDK agent entry %s", name)
	})
	if err != nil {
		t.Error("failure-safe agent readback", err)
	}
}
