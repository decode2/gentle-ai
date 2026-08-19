<!-- gentle-ai:opencode-background-subagents -->
### OpenCode Background Subagent Policy

Use OpenCode's Task tool with `background: true` only for independent, read-only exploration, audit, or review work where the parent can continue non-overlapping work.

At the parent level, allow no more than 2 concurrent background tasks. Completion notifications only: do not poll, sleep, run status checks, or proactively read for completion.

Use foreground tasks when the result is needed before the next action, for user decisions, dependent verify evidence, archive, formal RDD/4R lenses, refuters, fix validators, Judgment Day actors, or dependent phases.

Do not duplicate launches or work, and do not overlap files or topics. Never run parallel writers in one worktree.

Background jobs are process-local and non-durable. A restart loses them; make no recovery claim. If `background` is absent from the Task tool schema, or the capability is disabled or unknown, omit `background` and run the task in the foreground.

### SDD Item Apply Scheduling

`parallel_apply` is a coordinator-owned change policy with only `serialized` and `auto`. It is never runtime-ledger authority and is never written into `gentle-ai.sdd-items/v1` metadata. Background capability does not select it.

Default to `serialized`. Launch at most one ready item actor, then require that actor's own successful `sdd-attempt acquire --item`, opaque token, `settle`, and coordinator-only projection before considering another item. Preserve the immutable plan, budgets, evidence, item-selected authority, and the final join barrier.

Resolve an unset policy lazily, only when `sdd-status --json` exposes at least two ready items with compatible metadata, satisfied dependencies, pairwise disjoint canonical scopes, and available OpenCode background launch capability. In automatic execution, silently cache `serialized` for the change. In interactive execution, only at that opportunity ask once whether to use serialized or automatic scheduling, then cache that answer for the change; if capability is absent, disabled, or unknown, serialize silently. Do not add this policy to the mandatory four-group SDD Session Preflight or create a fifth preflight group.

Only explicit `auto` may use OpenCode `background: true`, and only at that real opportunity. Launch only the ready items whose dependencies and canonical scopes remain runtime-admissible; every item still acquires and settles independently, and only the coordinator projects results. Missing or incompatible metadata, unavailable capability, overlapping, dependent, malformed, shared, or unknown scopes stay serialized or blocked by existing authority. Do not add a queue, `max_parallel`, task-schema field, ledger field, or another attempt authority.
<!-- /gentle-ai:opencode-background-subagents -->
