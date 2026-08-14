package assets

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// managedDirectRunHarness loads the embedded plugin with the pinned OpenCode
// plugin API. It drives the returned hooks and custom-tool executors directly,
// which is the smallest deterministic runtime boundary before a real model.
const managedDirectRunHarness = `import plugin from "./plugin.mts"

const scenario = process.argv[2]
const cwd = process.argv[3]
const trace = { calls: [], prompts: [], deleted: [], native: [] }
let sequence = 0
const sessions = new Map()
const client = {
  session: {
    create: async ({ body }) => { const id = "child-" + ++sequence; sessions.set(id, body.parentID); trace.calls.push(["create", id, body.parentID]); return { data: { id, parentID: body.parentID } } },
    get: async ({ path }) => ({ data: scenario === "bad-parent" ? { id: path.id, parentID: "forged-parent" } : { id: path.id, parentID: sessions.get(path.id) } }),
    prompt: async ({ path, body }) => { trace.calls.push(["prompt", path.id]); trace.prompts.push([path.id, body.agent]); if (scenario === "prompt-failure") throw new Error("provider path /private must stay hidden") },
    delete: async ({ path }) => { trace.calls.push(["delete", path.id]); trace.deleted.push(path.id) },
  },
}
const hooks = await plugin({ client, directory: cwd, worktree: cwd })
const before = hooks["tool.execute.before"]
const ask = hooks["permission.ask"]
const launch = hooks.tool.gentle_direct_launch.execute
const direct = (name) => hooks.tool[name].execute
const fail = async (fn) => { try { const value = await fn(); return value === undefined ? "NO_ERROR" : value } catch (e) { return e instanceof Error ? e.message : String(e) } }
const launchChild = async (agent = "gentle-worker") => {
  const output = { args: { agent, handoff: "{\"identity\":\"issued\"}", _gentle_call: "forged-call", _gentle_nonce: "forged-nonce" } }
  await before({ tool: "gentle_direct_launch", sessionID: "parent", callID: "trusted-call" }, output)
  const result = await launch(output.args, { sessionID: "parent", agent: "parent-agent" })
  return { output, result }
}

if (scenario === "prompt-failure" || scenario === "register-failure") {
  const error = await fail(() => launchChild())
  console.log(JSON.stringify({ error, trace }))
} else if (scenario === "roles") {
  const values = ["gentle-worker", "gentle-reviewer", "gentle-worker-fast", "gentle-reviewer-r1", "worker", "reviewer", "sdd-apply", "RDD", "4R", "gentle-worker prose", "gentle-worker--"]
  const result = {}
  for (const agent of values) result[agent] = await fail(async () => { const x = await launchChild(agent); return x.result })
  console.log(JSON.stringify(result))
} else if (scenario === "restart") {
  const output = { args: { agent: "gentle-worker", handoff: "{}", _gentle_call: "x", _gentle_nonce: "x" } }
  await before({ tool: "gentle_direct_launch", sessionID: "parent", callID: "trusted-call" }, output)
  await hooks.event({ event: { type: "session.created", properties: { info: { id: "untrusted" } } } })
  const createdOnly = await fail(() => direct("direct_read")({ path: "a", offset: 0, limit: 1 }, { sessionID: "untrusted", agent: "gentle-worker" }))
  await hooks.dispose()
  const afterDispose = await fail(() => launch(output.args, { sessionID: "parent", agent: "parent-agent" }))
  console.log(JSON.stringify({ createdOnly, afterDispose }))
} else if (scenario === "denied") {
  const child = await launchChild()
  const denied = {}
  for (const tool of ["read", "edit", "bash", "task", "question", "gentle_direct_launch", "unknown"]) denied[tool] = await fail(() => before({ tool, sessionID: child.result.output, callID: "x" }, { args: {} }))
  // use the actual returned child id instead of a hand-written child context
  for (const tool of Object.keys(denied)) denied[tool] = await fail(() => before({ tool, sessionID: "child-1", callID: "x" }, { args: {} }))
  const permission = { status: "ask" }; await ask({ sessionID: "child-1" }, permission)
  console.log(JSON.stringify({ denied, permission: permission.status }))
} else if (scenario === "replay") {
  const output = { args: { agent: "gentle-worker", handoff: "{\"identity\":\"issued\"}", _gentle_call: "bad", _gentle_nonce: "bad" } }
  await before({ tool: "gentle_direct_launch", sessionID: "parent", callID: "trusted-call" }, output)
  const outcomes = await Promise.all([fail(() => launch(output.args, { sessionID: "parent", agent: "p" })), fail(() => launch(output.args, { sessionID: "parent", agent: "p" }))])
  console.log(JSON.stringify({ output: output.args, outcomes, trace }))
} else {
  const agent = scenario === "reviewer-edit" ? "gentle-reviewer" : "gentle-worker"
  const child = await launchChild(agent)
  const childID = child.result.output
  let directResult = ""
  let directError = ""
  if (scenario === "bad-parent" || scenario === "bad-response" || scenario === "leaky-error") directError = await fail(() => direct("direct_read")({ path: "a", offset: 0, limit: 1 }, { sessionID: childID, agent }))
  else if (scenario === "wrong-agent") directError = await fail(() => direct("direct_read")({ path: "a", offset: 0, limit: 1 }, { sessionID: childID, agent: "gentle-worker-forged" }))
  else if (scenario === "reviewer-edit") directError = await fail(() => direct("direct_edit")({ path: "a", base_sha256: "0".repeat(64), replacements: [] }, { sessionID: childID, agent }))
  else directResult = JSON.stringify([
    await direct("direct_read")({ path: "a", offset: 0, limit: 1 }, { sessionID: childID, agent }),
    await direct("direct_edit")({ path: "a", base_sha256: "0".repeat(64), replacements: [] }, { sessionID: childID, agent }),
    await direct("direct_inspect")({ query: "tree" }, { sessionID: childID, agent }),
  ])
  console.log(JSON.stringify({ output: child.output.args, childID, directResult, directError, trace, hasExec: !!hooks.tool.direct_exec }))
}
`

const managedDirectRunStub = `#!/usr/bin/env node
const fs = require("node:fs")
let input = ""
process.stdin.on("data", chunk => input += chunk)
process.stdin.on("end", () => {
  const log = process.env.GENTLE_AI_RUNTIME_LOG
  const args = process.argv.slice(2)
  const request = input ? JSON.parse(input) : {}
  if (log) fs.appendFileSync(log, JSON.stringify({ args, input: request }) + "\n")
  const operation = args[1]
  if (process.env.GENTLE_AI_RUNTIME_FAIL === operation) process.exit(1)
  if (operation === "issue") return console.log(JSON.stringify({ identity: "issued", revision: "issued-revision", handoff: { revision: "handoff-revision" } }))
  if (operation === "register") return console.log(JSON.stringify({ identity: "registered", revision: "binding-revision", repository_identity: "repo", handoff: { revision: "handoff-revision" } }))
  if (operation === "abort") return console.log(JSON.stringify({ schema: "gentle-ai.direct-operation/v1", operation: "abort", request_id: request.request_id || "abort", status: "ok", result: {} }))
  if (operation === "execute") {
    if (process.env.GENTLE_AI_RUNTIME_BAD_RESPONSE) return console.log(JSON.stringify({ schema: "gentle-ai.direct-operation/v1", operation: "wrong", request_id: request.request_id, status: "ok", result: {} }))
    if (process.env.GENTLE_AI_RUNTIME_LEAKY_ERROR || request.agent === "gentle-reviewer" && request.operation === "direct_edit") return console.log(JSON.stringify({ schema: "gentle-ai.direct-operation/v1", operation: request.operation, request_id: request.request_id, status: "error", error: { message: "/private/path\\nsecret" } }))
    return console.log(JSON.stringify({ schema: "gentle-ai.direct-operation/v1", operation: request.operation, request_id: request.request_id, status: "ok", result: { accepted: true } }))
  }
  process.exit(1)
})
`

func runManagedDirectRunScenario(t *testing.T, scenario string) (map[string]any, []map[string]any) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("runtime stub requires a POSIX executable path")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is unavailable")
	}
	npm, err := exec.LookPath("npm")
	if err != nil {
		t.Skip("npm is unavailable")
	}
	root := t.TempDir()
	for _, dir := range []string{filepath.Join(root, "bin"), filepath.Join(root, "work")} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"private":true,"dependencies":{"@opencode-ai/plugin":"1.18.10"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	install := exec.Command(npm, "install", "--ignore-scripts", "--no-audit", "--no-fund", "--package-lock=false")
	install.Dir = root
	if output, err := install.CombinedOutput(); err != nil {
		t.Fatalf("install pinned OpenCode plugin: %v\n%s", err, output)
	}
	source, err := Read("opencode/plugins/managed-direct-run.ts")
	if err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{"plugin.mts": source, "harness.mts": managedDirectRunHarness, filepath.Join("bin", "gentle-ai"): managedDirectRunStub} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	log := filepath.Join(root, "native.log")
	command := exec.Command(node, "harness.mts", scenario, filepath.Join(root, "work"))
	command.Dir = root
	command.Env = append(os.Environ(), "PATH="+filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"), "GENTLE_AI_RUNTIME_LOG="+log)
	if scenario == "bad-response" {
		command.Env = append(command.Env, "GENTLE_AI_RUNTIME_BAD_RESPONSE=1")
	}
	if scenario == "register-failure" {
		command.Env = append(command.Env, "GENTLE_AI_RUNTIME_FAIL=register")
	}
	if scenario == "leaky-error" {
		command.Env = append(command.Env, "GENTLE_AI_RUNTIME_LEAKY_ERROR=1")
	}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("pinned runtime harness failed (%v): %s", err, output)
	}
	var result map[string]any
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode harness output %q: %v", output, err)
	}
	var native []map[string]any
	if data, err := os.ReadFile(log); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			var entry map[string]any
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				t.Fatal(err)
			}
			native = append(native, entry)
		}
	}
	return result, native
}

func TestManagedDirectRunPinnedRuntimeAdmission(t *testing.T) {
	result, native := runManagedDirectRunScenario(t, "launch")
	output := result["output"].(map[string]any)
	if output["_gentle_call"] != "trusted-call" || output["_gentle_nonce"] == "forged-nonce" {
		t.Fatalf("hook did not replace forged authority: %#v", output)
	}
	if result["childID"] != "child-1" {
		t.Fatalf("created child = %#v", result)
	}
	if len(native) != 5 {
		t.Fatalf("native calls = %#v", native)
	}
	if calls := result["trace"].(map[string]any)["calls"].([]any); len(calls) != 2 || calls[0].([]any)[0] != "create" || calls[1].([]any)[0] != "prompt" {
		t.Fatalf("child was not created and registered before prompt: %#v", calls)
	}
	if args := native[1]["args"].([]any); strings.Join([]string{args[0].(string), args[1].(string)}, " ") != "direct-run register" {
		t.Fatalf("native argv = %#v", args)
	}
	register := native[1]["input"].(map[string]any)
	if register["parent_session_id"] != "parent" || register["parent_call_id"] != "trusted-call" || register["agent"] != "gentle-worker" {
		t.Fatalf("register provenance = %#v", register)
	}
	for index, operation := range []string{"direct_read", "direct_edit", "direct_inspect"} {
		execute := native[index+2]["input"].(map[string]any)
		if execute["operation"] != operation || execute["session_id"] != "child-1" || execute["parent_session_id"] != "parent" || execute["parent_call_id"] != "trusted-call" || execute["agent"] != "gentle-worker" || execute["request_id"] == "" {
			t.Fatalf("custom tool provenance = %#v", execute)
		}
	}
	if result["hasExec"] != false {
		t.Fatal("direct_exec was exposed by the plugin")
	}
}

func TestManagedDirectRunPinnedRuntimeFailClosed(t *testing.T) {
	roles, _ := runManagedDirectRunScenario(t, "roles")
	for _, role := range []string{"gentle-worker", "gentle-reviewer", "gentle-worker-fast", "gentle-reviewer-r1"} {
		if roles[role] == "managed direct launch denied" {
			t.Fatalf("canonical role refused: %s", role)
		}
	}
	for _, role := range []string{"worker", "reviewer", "sdd-apply", "RDD", "4R", "gentle-worker prose", "gentle-worker--"} {
		if roles[role] != "managed direct launch denied" {
			t.Fatalf("non-canonical role accepted: %s = %#v", role, roles[role])
		}
	}
	for _, scenario := range []string{"bad-parent", "wrong-agent", "reviewer-edit", "leaky-error"} {
		result, _ := runManagedDirectRunScenario(t, scenario)
		if result["directError"] != "managed direct operation denied" {
			t.Fatalf("%s = %#v", scenario, result)
		}
	}
	badResponse, _ := runManagedDirectRunScenario(t, "bad-response")
	if badResponse["directError"] != "managed direct operation returned an invalid response" {
		t.Fatalf("malformed response was not refused: %#v", badResponse)
	}
	restart, _ := runManagedDirectRunScenario(t, "restart")
	if restart["createdOnly"] != "managed direct operation denied" || restart["afterDispose"] != "managed direct launch denied" {
		t.Fatalf("restart authority = %#v", restart)
	}
	denied, _ := runManagedDirectRunScenario(t, "denied")
	for tool, value := range denied["denied"].(map[string]any) {
		if value != "managed direct tool denied" {
			t.Fatalf("%s = %#v", tool, value)
		}
	}
	if denied["permission"] != "deny" {
		t.Fatalf("permission defense = %#v", denied)
	}
	promptFailure, native := runManagedDirectRunScenario(t, "prompt-failure")
	if promptFailure["error"] != "managed direct launch denied" || strings.Contains(promptFailure["error"].(string), "private") {
		t.Fatalf("prompt failure leaked or succeeded: %#v", promptFailure)
	}
	if len(native) != 3 || native[2]["args"].([]any)[1] != "abort" || len(promptFailure["trace"].(map[string]any)["deleted"].([]any)) != 1 {
		t.Fatalf("prompt failure cleanup = native %#v trace %#v", native, promptFailure["trace"])
	}
	registerFailure, native := runManagedDirectRunScenario(t, "register-failure")
	if registerFailure["error"] != "managed direct launch denied" || len(native) != 2 || native[0]["args"].([]any)[1] != "issue" || native[1]["args"].([]any)[1] != "register" || len(registerFailure["trace"].(map[string]any)["deleted"].([]any)) != 1 {
		t.Fatalf("registration failure cleanup = native %#v trace %#v", native, registerFailure["trace"])
	}
}

// The plugin consumes one local nonce before awaiting registration. Cross-process
// first-child races are intentionally proved at the Go storage boundary by
// TestRunDirectRunAdmissionRaceAndWireRefusals and TestStoreCASConflictHasNoRetry.
func TestManagedDirectRunPinnedRuntimeReplayConsumesOneLocalAdmission(t *testing.T) {
	result, _ := runManagedDirectRunScenario(t, "replay")
	outcomes := result["outcomes"].([]any)
	winner, loser := outcomes[0], outcomes[1]
	if winner == "managed direct launch denied" {
		winner, loser = loser, winner
	}
	if winner.(map[string]any)["output"] != "child-1" {
		t.Fatalf("replay outcomes = %#v", outcomes)
	}
	if loser != "managed direct launch denied" {
		t.Fatalf("replay was not refused: %#v", outcomes)
	}
}
