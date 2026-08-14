import { randomBytes } from "node:crypto"
import { spawn } from "node:child_process"
import { tool, type Plugin } from "@opencode-ai/plugin"

const LAUNCH = "gentle_direct_launch"
const DIRECT_TOOLS = new Set(["direct_read", "direct_edit", "direct_inspect"])
const DENIED = new Set(["read", "edit", "bash", "task", "question"])
const role = (value: unknown): string | undefined =>
  typeof value === "string" && /^(gentle-worker|gentle-reviewer)(-[A-Za-z0-9][A-Za-z0-9_-]*)?$/.test(value) ? value : undefined

type PendingLaunch = { parentSessionID: string; parentCallID: string }
type Child = { parentSessionID: string; parentCallID: string; agent: string; identity: string; handoffRevision: string; bindingRevision: string }

function native(cwd: string, args: string[], input: string): Promise<unknown> {
  return new Promise((resolve, reject) => {
    const child = spawn("gentle-ai", args, { cwd, stdio: ["pipe", "pipe", "pipe"] })
    const out: Buffer[] = []
    child.stdout.on("data", (chunk: Buffer) => out.push(chunk))
    child.on("error", () => reject(new Error("managed direct operation failed")))
    child.on("close", (code) => {
      if (code !== 0) return reject(new Error("managed direct operation failed"))
      try { resolve(JSON.parse(Buffer.concat(out).toString("utf8"))) } catch { reject(new Error("managed direct operation returned an invalid response")) }
    })
    child.stdin.end(input)
  })
}

function record(value: unknown): { identity: string; revision: string; repositoryIdentity?: string; handoff: { revision: string } } {
  if (!value || typeof value !== "object") throw new Error("managed direct operation returned an invalid response")
  const v = value as Record<string, unknown>, handoff = v.handoff as Record<string, unknown>
  if (typeof v.identity !== "string" || typeof v.revision !== "string" || !handoff || typeof handoff.revision !== "string") throw new Error("managed direct operation returned an invalid response")
	return { identity: v.identity, revision: v.revision, repositoryIdentity: typeof v.repository_identity === "string" ? v.repository_identity : undefined, handoff: { revision: handoff.revision } }
}

function response(value: unknown, operation: string, requestID: string): unknown {
  if (!value || typeof value !== "object") throw new Error("managed direct operation returned an invalid response")
  const v = value as Record<string, unknown>
  if (v.schema !== "gentle-ai.direct-operation/v1" || v.operation !== operation || v.request_id !== requestID || (v.status !== "ok" && v.status !== "error")) throw new Error("managed direct operation returned an invalid response")
  if (v.status === "error") throw new Error("managed direct operation denied")
  return v.result
}

const ManagedDirectRun: Plugin = async ({ client, directory, worktree }) => {
  const launches = new Map<string, PendingLaunch>()
  const children = new Map<string, Child>()
  const cwd = worktree || directory
  const child = async (context: { sessionID: string; agent: string }, operation: string, payload: unknown) => {
    const pending = children.get(context.sessionID)
    if (!pending || context.agent !== pending.agent) throw new Error("managed direct operation denied")
    const got = await client.session.get({ path: { id: context.sessionID } })
    if (!got.data || got.data.parentID !== pending.parentSessionID) throw new Error("managed direct operation denied")
    const requestID = randomBytes(16).toString("hex")
    const value = await native(cwd, ["direct-run", "execute", "--cwd", cwd], JSON.stringify({ schema: "gentle-ai.direct-operation/v1", identity: pending.identity, operation, request_id: requestID, session_id: context.sessionID, handoff_revision: pending.handoffRevision, binding_revision: pending.bindingRevision, parent_session_id: pending.parentSessionID, parent_call_id: pending.parentCallID, agent: pending.agent, payload }))
    return response(value, operation, requestID)
  }
  return {
    dispose: async () => { launches.clear(); children.clear() },
    event: async ({ event }) => { if (event.type === "session.deleted") children.delete(event.properties.info.id) },
    tool: {
      [LAUNCH]: tool({ description: "Launch a bounded managed direct worker or reviewer", args: { agent: tool.schema.string(), handoff: tool.schema.string(), _gentle_call: tool.schema.string(), _gentle_nonce: tool.schema.string() }, async execute(args, context) {
        const expected = role(args.agent), launch = launches.get(args._gentle_nonce)
        launches.delete(args._gentle_nonce)
        if (!expected || !launch || args._gentle_call !== launch.parentCallID || context.sessionID !== launch.parentSessionID) throw new Error("managed direct launch denied")
        const issued = record(await native(cwd, ["direct-run", "issue", "--cwd", cwd], args.handoff))
        const created = await client.session.create({ body: { parentID: context.sessionID } })
        if (!created.data || created.data.parentID !== context.sessionID) throw new Error("managed direct launch denied")
        let registered: ReturnType<typeof record> | undefined
        try {
          registered = record(await native(cwd, ["direct-run", "register", "--cwd", cwd], JSON.stringify({ identity: issued.identity, revision: issued.revision, parent_session_id: context.sessionID, parent_call_id: launch.parentCallID, agent: expected })))
          children.set(created.data.id, { parentSessionID: context.sessionID, parentCallID: launch.parentCallID, agent: expected, identity: registered.identity, handoffRevision: issued.handoff.revision, bindingRevision: registered.revision })
          await client.session.prompt({ path: { id: created.data.id }, body: { agent: expected, parts: [{ type: "text", text: "Use only direct_read, direct_edit, and direct_inspect. Native tools and delegation are denied." }] } })
        } catch {
          children.delete(created.data.id)
		  if (registered?.repositoryIdentity) await native(cwd, ["direct-run", "abort", "--cwd", cwd], JSON.stringify({ schema: "gentle-ai.direct-run-abort/v1", identity: registered.identity, revision: registered.revision, handoff_revision: issued.handoff.revision, parent_session_id: context.sessionID, parent_call_id: launch.parentCallID, agent: expected, repository_identity: registered.repositoryIdentity, child_session_id: "", reason: "cancelled" })).catch(() => undefined)
          await client.session.delete({ path: { id: created.data.id } }).catch(() => undefined)
          throw new Error("managed direct launch denied")
        }
        return { title: "Managed direct child launched", output: created.data.id }
      } }),
      direct_read: tool({ description: "Read an admitted direct-run file", args: { path: tool.schema.string(), offset: tool.schema.number(), limit: tool.schema.number() }, async execute(args, context) { return JSON.stringify(await child(context, "direct_read", args)) } }),
      direct_edit: tool({ description: "Edit an admitted direct-run file", args: { path: tool.schema.string(), base_sha256: tool.schema.string(), replacements: tool.schema.array(tool.schema.object({ start: tool.schema.number(), end: tool.schema.number(), text: tool.schema.string() })) }, async execute(args, context) { return JSON.stringify(await child(context, "direct_edit", args)) } }),
      direct_inspect: tool({ description: "Inspect an admitted direct-run tree", args: { query: tool.schema.literal("tree"), path: tool.schema.string().optional() }, async execute(args, context) { return JSON.stringify(await child(context, "direct_inspect", args)) } }),
    },
    "tool.execute.before": async (input, output) => {
      const managed = children.get(input.sessionID)
      if (managed && (DENIED.has(input.tool) || input.tool === LAUNCH || DIRECT_TOOLS.has(input.tool) === false)) throw new Error("managed direct tool denied")
      if (input.tool === LAUNCH) {
        const nonce = randomBytes(32).toString("hex")
        launches.set(nonce, { parentSessionID: input.sessionID, parentCallID: input.callID })
        output.args._gentle_call = input.callID
        output.args._gentle_nonce = nonce
        return
      }
      if (input.tool === "task" && role(output.args?.subagent_type)) throw new Error("managed direct roles require gentle_direct_launch")
    },
    "permission.ask": async (input, output) => { if (children.has(input.sessionID)) output.status = "deny" },
  }
}

export default ManagedDirectRun
