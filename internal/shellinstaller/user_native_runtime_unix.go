//go:build linux && amd64

package shellinstaller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// NativeInstallerRuntime is a process client, not installation authority or a
// sandbox. The caller must authenticate the whole source snapshot, keep it
// immutable, establish the existing bounded worker, restore the terminal and
// obtain physical consent before preparing dependencies or calling Run.
// This transport does not replace the qualified npm backend or publish roots.
type NativeInstallerRuntime struct {
	root, source, node, sourceStamp string
}

// NativeInstallerPlan retains the exact release plan for this private client.
// A copy for display cannot mutate the reviewed snapshot.
type NativeInstallerPlan struct {
	owner *NativeInstallerRuntime
	data  json.RawMessage
}

func (p NativeInstallerPlan) JSON() []byte { return append([]byte(nil), p.data...) }

// NativeInstallerResult preserves the native verdict and full result; neither
// "ready" nor a successful process exit proves publication or installed health.
type NativeInstallerResult struct {
	Outcome   string          `json:"outcome"`
	Reason    string          `json:"reason"`
	Completed []string        `json:"completed"`
	Details   json.RawMessage `json:"-"`
}

func NewNativeInstallerRuntime(ctx context.Context, root, source string) (*NativeInstallerRuntime, error) {
	if err := nativeRuntimeError(ctx, nil); err != nil {
		return nil, err
	}
	if !privateHierarchyPath(root) || !privateHierarchyPath(source) {
		return nil, privateError("refused", nil)
	}
	if err := nativeRuntimePaths(root); err != nil {
		return nil, err
	}
	// This is only a physical entry check; it does not authenticate imports.
	entry, err := privateForeignPhysical(filepath.Join(source, "scripts/installer-runtime.mjs"))
	if err != nil {
		return nil, err
	}
	node := filepath.Join(root, "runtime/node/bin/node")
	return &NativeInstallerRuntime{root: root, source: source, node: node, sourceStamp: privateStamp(entry)}, nil
}

func (w *NativeInstallerRuntime) Collect(ctx context.Context) (NativeInstallerPlan, error) {
	data, err := w.invoke(ctx, "collect", nil, false)
	if err != nil {
		return NativeInstallerPlan{}, err
	}
	if len(data) == 0 || data[0] != '{' {
		return NativeInstallerPlan{}, privateError("output", nil)
	}
	return NativeInstallerPlan{owner: w, data: data}, nil
}

func (w *NativeInstallerRuntime) Run(ctx context.Context, plan NativeInstallerPlan, consent bool) (NativeInstallerResult, error) {
	if plan.owner != w || len(plan.data) == 0 {
		return NativeInstallerResult{}, privateError("refused", nil)
	}
	data, err := w.invoke(ctx, "run", plan.data, consent)
	if err != nil {
		return NativeInstallerResult{}, err
	}
	var result NativeInstallerResult
	if json.Unmarshal(data, &result) != nil || result.Outcome == "" {
		return NativeInstallerResult{}, privateError("output", nil)
	}
	result.Details = append(json.RawMessage(nil), data...)
	return result, nil
}

func (w *NativeInstallerRuntime) invoke(ctx context.Context, operation string, plan json.RawMessage, consent bool) (json.RawMessage, error) {
	if err := nativeRuntimeError(ctx, nil); err != nil {
		return nil, err
	}
	// Recheck owned paths/Node and the source entry before every process.
	// Transitively authenticated immutable source remains the caller's duty.
	if err := nativeRuntimePaths(w.root); err != nil {
		return nil, err
	}
	if err := UserKernelCheck(); err != nil {
		return nil, privateError("refused", err)
	}
	entry, err := privateForeignPhysical(filepath.Join(w.source, "scripts/installer-runtime.mjs"))
	if err != nil || privateStamp(entry) != w.sourceStamp {
		return nil, privateError("refused", err)
	}
	if _, err := privateNativeFile(ctx, w.node, 0700, privateNativeNodeSize, privateNativeNodeSHA); err != nil {
		return nil, nativeRuntimeError(ctx, err)
	}
	request := struct {
		Operation string          `json:"operation"`
		Plan      json.RawMessage `json:"plan,omitempty"`
		Consent   bool            `json:"consent"`
	}{operation, plan, consent}
	input, err := json.Marshal(request)
	if err != nil || len(input) > 4096 {
		return nil, privateError("refused", err)
	}
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(child, w.node, "--liftoff-only", "--input-type=module", "-e", nativeRuntimeClient, w.source)
	cmd.Dir = filepath.Join(w.root, "project")
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = []string{
		"HOME=" + filepath.Join(w.root, "home"), "TMPDIR=" + filepath.Join(w.root, "tmp"),
		"XDG_CONFIG_HOME=" + filepath.Join(w.root, "config"), "XDG_STATE_HOME=" + filepath.Join(w.root, "state"),
		"XDG_DATA_HOME=" + filepath.Join(w.root, "home/.local/share"), "PNPM_HOME=" + filepath.Join(w.root, "pnpm"),
		"PI_CODING_AGENT_DIR=" + filepath.Join(w.root, "agent"), "GENTLE_PI_CONFIG_ROOT=" + filepath.Join(w.root, "config"),
		"NPM_CONFIG_USERCONFIG=" + filepath.Join(w.root, "config/user.npmrc"), "NPM_CONFIG_GLOBALCONFIG=" + filepath.Join(w.root, "config/global.npmrc"),
		"PATH=" + filepath.Dir(w.node) + ":" + filepath.Join(w.root, "pnpm") + ":/usr/bin:/bin",
	}
	// Existing bounded output, process-group kill, wait/reap and error taxonomy.
	output, err := userOwnedRun(child, cmd, cancel)
	if err != nil {
		return nil, err
	}
	data := json.RawMessage(output)
	if !json.Valid(data) {
		return nil, privateError("output", nil)
	}
	return data, nil
}

func nativeRuntimeError(ctx context.Context, err error) error {
	if ctx == nil {
		return privateError("canceled", err)
	}
	if ctx.Err() == nil {
		return err
	}
	kind := "canceled"
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		kind = "deadline"
	}
	return privateError(kind, errors.Join(err, ctx.Err()))
}

func nativeRuntimePaths(root string) error {
	for _, name := range []string{"", "home", "tmp", "config", "state", "agent", "project", "pnpm", "home/.local", "home/.local/share"} {
		path := filepath.Join(root, name)
		_, err := os.Lstat(path)
		if os.IsNotExist(err) && (name == "pnpm" || strings.HasPrefix(name, "home/.local")) {
			continue
		}
		if _, err := privateDirectory(path); err != nil {
			return err
		}
	}
	for _, name := range []string{"user.npmrc", "global.npmrc"} {
		info, err := privatePhysical(filepath.Join(root, "config", name))
		if err != nil {
			return err
		}
		st := info.Sys().(*syscall.Stat_t)
		if info.Mode() != 0600 || st.Uid != uint32(os.Getuid()) || st.Nlink != 1 {
			return privateError("refused", nil)
		}
	}
	return nil
}

// Invocation glue only: no server, web entry, bootstrap reconstruction or
// installation actions here. The existing shared factory owns their logic.
const nativeRuntimeClient = `
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
const request = JSON.parse(readFileSync(0, "utf8"));
const { createInstallerRuntime } = await import(pathToFileURL(join(process.argv[1], "scripts/installer-runtime.mjs")).href);
const runtime = createInstallerRuntime({ env: process.env });
const fresh = await runtime.collectPlan("release");
let reply;
if (request.operation === "collect") {
  reply = fresh.plan;
} else if (request.operation === "run") {
  if (JSON.stringify(request.plan) !== JSON.stringify(fresh.plan)) {
    reply = { outcome: "blocked", reason: "selection-changed", completed: [] };
  } else {
    reply = await runtime.runInstall({ plan: fresh.plan, consent: request.consent });
  }
} else {
  throw new Error("Unsupported native runtime operation");
}
process.stdout.write(JSON.stringify(reply));
`
