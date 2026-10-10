# Complete the Gentle Shell install experience on three operating systems

Status: **in progress; no complete installed flow qualified on any target**.
Parent session `01a10f0e` owns integration. Approved umbrella: #4935.
This scope supersedes the earlier component-first work order; completed evidence and consumed reviews remain valid within their original bounds.

## Outcome and priority

Deliver **Install Gentle Shell → Configure experience → Destination → two-panel Ready → physical confirmation → actual installation → launch** from one ordinary Gentle AI TUI on Linux amd64, Darwin arm64/macOS14+ and Windows11 x64.

**Finish the whole principal flow first; harden additional cases afterward.**
Reuse the working components and existing contracts. A task belongs on the delivery path only if it connects this flow, verifies an offered effect, preserves an essential guarantee, or fixes an observed blocker. Do not create a new framework or expand a negative-case matrix merely because another failure is imaginable.

Ready is the pre-install review of the exact selection, not a claim that installation or tool health has already succeeded. Installation runs only after the owning `tea.Program` successfully returns and fully restores the terminal. Escape/Ctrl-C returns to Welcome without installing. Ordinary `NewModel` changes only through `WithShellInstaller()`.

Separate is the required complete experience on each target. Preserve existing supported Shared behavior and disclose its real limitations; do not add Windows Shared or silently drop an unsupported Shared choice. A refusal preserves safety but does not qualify the required Separate feature.

## In scope for the first complete delivery

| Surface | Required behavior and evidence |
|---|---|
| Common TUI | Welcome entry; all experience controls; destination/mode; two-panel Ready; reachable confirm/back/cancel and usable resize/scroll. One parent program, no nested installer TUI. |
| Selection | One immutable complete profile reaches existing inspection, physical consent and the matching backend. Keep nil/historical JSON, confirmation hashes, 256-byte input and platform argument compatibility. |
| Persona/background | Gentleman or neutral and on/off affect only the selected private runtime. Unmanaged performs no persona lookup/write/replacement, including malformed historical files. |
| Strict TDD | The installed SDK consumes the additional applicable test-first instruction policy; OFF retains ordinary discipline. No hard write gate or claim of model compliance. |
| Global RDD | Compatible installed native setter/status reads the selected runtime's `TARGET/home/.gentle-ai` without Git discovery. Source API alone is insufficient. |
| CodeGraph | Opt-in authenticated private executable/library, launcher/SDK discovery and real version/status readback. No automatic project indexing or copied personal binary. |
| Context7 | Canonical `TARGET/agent/mcp.json`, fixed `https://mcp.context7.com/mcp`, real installed Pi MCP registration and observable availability/health. Disclose project precedence; never invent model-call provenance or permission. |
| Main preview | Explicit opt-in exact frozen compatible snapshot, consent and effective channel readback; stable default remains. Do not substitute a local version label for authenticated acquisition. |
| Optional GGA | Available only when enabled and configured according to its documentation. Verify private CLI/config/launch exposure; OFF/absence has no provisioning. Provider installation/authentication and `gga init`/`gga install` remain separately consented, not automatic hooks or an invented Pi bridge. |
| SDD retirement | No new SDD/OpenSpec installation, UI offer or management. Preserve historical files/profiles byte-for-byte. |
| Installation/launch | Actual backend effects, truthful success/failure and canonical installed readback; both owned command bindings launch the selected runtime/config and settle without changing personal Pi/projects. |
| Delivery | Compatible authentic native/SDK artifacts and bounded reviewable slices. Source-only or private-fixture success is not an available release. Remote publication/activation requires fresh exact authority. |

## Delivery TODO — execute in this order

Each milestone is an outcome, not a demand for another component research campaign.
Implement it through independently meaningful local units of at most400 additions plus deletions, including tests/docs; never compress code or publish the cumulative feature as one PR.

| Order / TODO | Outcome | Completion gate |
|---|---|---|
| E1 / #72 | Complete the common configure/destination/Ready flow and profile handoff | Model/navigation and real terminal-boundary proof; all offered choices survive unchanged; no worker during review/cancel. |
| E2 / #53 | Connect all selected effects to the real backends and compatible installed SDK/native artifacts | Admission only with actual producers/consumers/readbacks; replace refusals on the required Separate path, not with bypasses. Reuse existing persona/BG/TDD/C7/GGA/CodeGraph work. |
| E3 / #84 | Run the whole installed Linux Separate happy path | One coherent artifact set, all offered effects/readbacks, both launchers, terminal/process settlement and personal preservation. Resolve only blockers actually preventing this run. |
| E4 / #85 | Run the same complete flow on native Apple silicon/macOS14+ | Actual installation/launch/readback, not cross-compilation or an unfetched prior CI artifact. Obtain access authority before native execution. |
| E5 / #86 | Run the same complete flow on native Windows11 x64, unelevated/local NTFS | Actual new-profile installation/launch/readback and settled cancellation; cross-builds/WSL/Server are not substitutes. Fresh exact VM/access authority required. |
| E6 / #87 | Close acceptance and prepare authorized incremental delivery | Evidence per offered item on all three OS, human visual acceptance, exact artifact identities and no unresolved principal-flow blocker. Request publication/release authority at that boundary. |

The active TODO mirrors E1–E6 first. Older pending IDs are retained as subordinate traceability or deferred work, not an alternative execution order. Completed tasks keep their original identities and evidence.

## Proportionate functional verification

For each behavior-changing unit: relevant deterministic behavioral RED where applicable, minimal change, focused GREEN, pertinent regression and functional proof. Compiler RED, observer errors and infrastructure failures are not behavioral RED. Passive scope documentation needs structural readback, not invented tests.

For each required OS, exercise the actual ordinary TUI, configure the profile, inspect the fresh destination, review/physically confirm, install, launch and read the installed effects. Cover selected ON/OFF behavior and the three persona/two background choices where pertinent; do not require an exhaustive Cartesian product before delivery.

Minimum alternate controls protect cancellation/back, rejected unsupported input before effects, authenticated input rejection, personal preservation and settled failure. Preserve existing custody/recovery contracts and run their relevant regressions when touched. A model test, callback fixture, inert worker, cross-build or green generic CI never replaces the integrated installed run.

Record exact source/artifact identities, scenario/command, result and limitations. Do not hide a failed check or count SKIP, forced kill, missing artifact or a no-tests command as PASS. An unrelated benchmark failure remains visible but does not become a detour when ordinary operation works.

## Essential guarantees — not deferred

- Reuse physical consent, inspection, acquisition, custody, manager, process/cancellation and recovery contracts; validate the full selection before effects.
- Keep authenticated fixed inputs, release-age rules and32MiB per-file custody. No silent bound widening, fake ownership, copied stock helper or relaxed Guest limits.
- Preserve personal Pi, projects, credentials, preferences, PATH, shell initialization and unselected Shared assets.
- Keep configuration in its real private scope: RDD in target home; persona/BG/TDD and GGA in target config; MCP in target agent home. Disclose trusted-project/environment precedence.
- Do not claim readiness, health, authentic distribution or installed acceptance from source/component evidence.
- Keep `tui → cli`, never the reverse; one terminal owner and installation only after successful restoration.
- Follow fresh native assessment for each candidate; when review is required, its exact ACK precedes the source commit. Consumed reviews are never reopened or borrowed for later edits.

## Deferred until after the principal flow

| Deferred work | Rule for returning it to scope |
|---|---|
| #142 CodeGraph partial-stage advisory R3-001 | Receipt `review-3e637e2d3c47a950` stands. Resume only after delivery or evidence that existing stage disposal blocks the real flow; reuse existing cleanup. |
| #89/#94/#96/#103/#113 extra observer, negative-case and advisory hardening | Not independent delivery gates without an observed principal-flow or essential-guarantee defect. |
| #111 auxiliary startup benchmark diagnosis | Preserve its failed evidence; fix only if it reproduces a blocker in ordinary operation. |
| #105 additional genuine-model permission qualification | No fabricated assistant provenance; does not authorize provider credentials or broaden install integration into model-policy research. Actual MCP availability/health remains in scope. |
| New Shared/platform modes, Windows update/force/recover and rare recovery permutations | Do not add unsupported capabilities to finish this install feature; preserve existing supported behavior. |
| GGA provider bridge, automatic hooks, MCP clients, tool repairs and indexing | Not this installer's job. Documented opt-in prerequisites remain explicit. |

A genuinely blocking failure may re-enter E1–E6 with a short description, direct evidence and the smallest existing-contract fix. Hypothetical failures alone do not justify reprioritization.

## Current evidence and real blockers

Latest native component-chain source: `cdfa5bd44ce132dc5e2af67bcdcd7a02ebe50d64`; common-TUI integration source: `77fc62dc495e7fb4dc0ba24d667c1c7e09739dba`.
SDK Strict TDD/cleanup source: `1fb0cc1749c7055f8d199ae2db094064890dcdd6` / `27ae302542bce7c22f70e0bd00ec89e396956194`.
These are local source boundaries, not compatible installed release identities.

| Target/surface | What works within recorded evidence | Still needed |
|---|---|---|
| Linux | Existing backend; common entry/selector/cancel and real terminal boundary; private component effects | Complete new-profile install from the common TUI and installed launch. Guest stock `/usr/bin/setpriv` owner65534 is a real qualification prerequisite, not GGA RED; no custody bypass. |
| Darwin | Existing backend, source tests and cross-builds | Native new-experience installation and launch on Apple silicon/macOS14+. |
| Windows | Existing Separate backend/source/cross-builds | Native new-profile packet and complete flow; prior VM/media/network lease is closed/restored and not replayable. |
| Experience/SDK | Persona/BG, Strict TDD, C7 registration/CLI and optional GGA private proofs; CodeGraph selection/library/real CLI | Connect the actual UI/profile/backend/launcher/SDK chain. Public profiles and real CodeGraph preparer still refuse; never call this installed admission. |
| Distribution | Global-only RDD source and SDD retirement groundwork | Compatible authentic packed distribution/channel binding. #1952 STOP remains unresolved; no invented recovery disposition or signing/pin substitution. |

Component evidence stays useful: `e2e/shell-codegraph-{selection,library,shared-node}.md`, `e2e/shell-strict-tdd-stage.md`, `e2e/shell-gga-{selection,stage,cli}.md` and `docs/gentle-shell-common-selection.md`. Their narrower scopes do not close E1–E6.

## Authority and closure

Local implementation/tests/native reviews/commits are authorized only in isolated Gentle AI/Gentle Pi worktrees. No host source SDK/compiler/test/formatter/CodeGraph execution; reuse the existing offline credentialless Guest. Children, if useful, are read-only and explicitly forbidden from CodeGraph indexing/execution.

`main-current`, active preview, personal package registration and binaries remain unchanged. Sync grants are spent. No new push, PR, release/signing/pin, personal install/activation, credential change, VM action or cross-orchestrator contact is granted by this replan. Request the concrete missing authority only when it blocks the next real milestone.

No Main merge, Main push, auto-merge or merge API without current explicit repository/action approval. Do not close #4935 or claim the feature delivered while any required OS, offered effect or distribution gate remains unqualified.

- [ ] E1 — common experience flow complete.
- [ ] E2 — real selected effects and compatible artifact chain connected.
- [ ] E3 — complete installed Linux flow accepted.
- [ ] E4 — complete installed Darwin flow accepted.
- [ ] E5 — complete installed Windows flow accepted.
- [ ] E6 — human acceptance and authorized delivery gates complete.
