# Complete the Gentle Shell install experience on three operating systems

Status: **in progress; not a qualified release**. Parent session `01a10f0e` owns integration and delivery. Reuse approved umbrella #4935; do not close it against source-only progress.

## Outcome and completion rule

One ordinary Gentle AI TUI must offer **Install Gentle Shell → Configure experience → Destination → Ready**. It must install the reviewed selection after the owning `tea.Program` restores the terminal. Cancel returns to Welcome without installing. The ordinary `NewModel` remains unchanged unless explicitly opted in. CLI and TUI reuse the same selection, physical consent, backend and recovery contracts.

Every offered item needs a real effect, canonical readback, preservation checks and target-platform evidence. A checkbox, source merge, cross-build or green generic CI is not proof of an installed feature. Unsupported actions must remain explicit, effect-free refusals until implemented and qualified.

## Verified baseline

| Surface | Observed source/evidence | What it does not prove |
|---|---|---|
| Native Main | `8ae991148db783459efa311252e014956af96585` | New experience controls are not present |
| SDK Main | `8a7aa1c9b313c79a70f5d9f3f8071816f0c9e124` | Packed/install lifecycle qualification |
| Linux | #5242 merged; amd64/systemd user manager backend | All new choices or installed release parity |
| Windows | #5279 merged `ddf5a48aae0dfa19c90dd3df89fd0a26bd92ae1f` | Win11 x64 actual acceptance; Shared/update/force/recover |
| Darwin | #5407 merged; Apple silicon, macOS14+, Separate/Shared/recover | New experience controls or every supported OS version |
| Publication | Latest native and SDK releases remain `v4.0.0` | Main source changes are not in those artifacts |
| Global RDD prerequisite | #5411 at `b8bbea5`, fresh review/ACK, hosted checks green, still open | No repository-free global mode in published4.0.0 |
| Retired routing/migration | #1956 at `93158f1`, fresh review/ACK, hosted checks green, still open | Companion ownership or release qualification |
| Retired ownership | #1952 remote378 lines; local reshape review is STOP | Do not bypass, reset or invent recovery inputs |

Native entry is `internal/cli/shell_install.go`; the ordinary TUI Welcome has no shell-installer entry. POSIX exits its own program before execution; Windows starts its worker inside that program. Existing private UI/profile commits are references, not an integration base.

## Architecture boundaries

- One common UI and selection contract; OS adapters supply supported actions and native effects, not another planner/frontend.
- `tui` may import `cli`; `cli` must not import `tui`. Installation belongs after parent `runTUI`, never a nested program.
- Preserve byte-identical nil-profile legacy protocol and physical-selection hashes. Windows channel arguments must not be mistaken for experience JSON.
- Explicit experience choices require a new empty target. Never overwrite preferences of an existing installation.
- Persona/background belong to the new runtime's configuration. Native RDD belongs to `TARGET/home/.gentle-ai`, including Shared; disclose that scope.
- Do not mutate personal Pi, projects, PATH, shell initialization, global Git controls or unselected Shared assets.
- Preserve retired SDD/OpenSpec file bytes and profiles; stop management, do not delete user history.
- Keep acquisition frozen: exact versions/commits, authenticated hashes/SRIs, full platform graphs, minimum three-day release age and unchanged custody/preimages/consent safeguards.

## Item acceptance matrix

| Item | Intended real effect/readback | Implementation dependency and negative control |
|---|---|---|
| Welcome entry | Opens existing installer selector in the owning TUI | Opt-in API; ordinary Welcome unchanged; Escape returns Welcome |
| Persona | Gentleman/neutral written using the SDK's `{mode}` contract; unmanaged means no installer write, not a third SDK mode | Global/project precedence and files preserved; omission currently defaults to Gentleman, disclose it |
| Background | Target-global canonical on/off policy and resolver readback | Project policy wins; malformed winning policy fails closed; no personal policy changes |
| Strict TDD | A real SDK-consumed TDD policy with observable runtime behavior | Current assets are guidance, not an enforcement switch; implement canonical interface before enabling UI |
| Global RDD | Installed native global-only setter/status, independent of ancestor clone | Merge/publication prerequisite5411; damaged-clone continuation; ordinary clone-off precedence unchanged |
| CodeGraph | Authenticated private provisioning plus actual executable/tool health | Workspace indexing is a separate explicit action; no `@latest` or invented global-index toggle |
| Context7 | Real Pi MCP registration and server health in the new runtime | Current banner only reads `mcp.json`; establish the canonical provider/schema/consumer first |
| Main preview | Exact acquired snapshot and persisted SDK channel contract/readback | Native Windows stable/main differs from SDK release/main; POSIX currently refuses channel selection; stable default preserved |
| GGA | Private CLI availability and truthful project-hook capability/readback | No SDK GGA interface established; no unsolicited hooks or project changes |
| Destination/mode | Existing inspection, visible bindings, fresh physical selection token | Separate all supported targets; Shared only where implemented; collisions/refusals have no effects |
| Ready | Two-panel selection plus explicit changes, scopes and command bindings | Full resize/scroll/key reachability; confirm changes bind to the same inspected selection |
| Install/cancel | Exactly one terminal owner; settled cancellation, reap, preserved failure evidence | No worker during embedded review; no uncertain cleanup or inferred completion |
| Launch | Both owned command bindings launch correct runtime/agent/config | Personal runtime unchanged; restored terminal/CWD and actual settled exit |
| Update/force/recover | Only actions the backend can actually qualify | Preserve Shared preimages and failure journals; unsupported Windows actions remain refused until separate implementation/approval |

## Ordered work units

Each unit includes its meaningful tests/docs and stays at or below400 additions plus deletions. Forecast before edits; split by behavior, never by file type or code compression. Existing chain choice is bounded independently reviewed units; do not publish one cumulative oversized feature PR.

| Order | Unit | Completion evidence |
|---|---|---|
| 01 | Export a common CLI selection/outcome API; opt-in selection-only Windows | Public defaults/outcome/cancel/confirmation; no worker started; native builds all targets |
| 02 | Two bounded subunits:02a opt-in Welcome embedding;02b parent post-`runTUI` handoff | One `tea.Program`; cancel/back; execution occurs after terminal restoration |
| 03 | Common configure/destination/Ready rendering and navigation | Behavioral model tests and real PTY resize/review/cancel, without backend duplication |
| 04 | Bounded subunits:04a codec;04b request/hash/refusal guard;04c transport only with consumers05/06 before UI enablement | Nil bytes identical; invalid/oversized choices fail before writes; Windows channel not decoded as profile |
| 05 | 05a writer;05b POSIX wiring;05c frozen-package consumer proof | Full installed/lifecycle qualification remains; public profiles refused until transport/platform support |
| 06 | 06a shared canonical bytes;06b Windows writer/wiring | SID/DACL/file identity/custody retained; actual Win11 target proof still required |
| 07 | Resolve RDD publication prerequisite and bind installed artifact | Authentic compatible native version; global-only effect/readback and no Git discovery |
| 08 | SDK Strict TDD canonical policy consumer | Observable runtime behavior and ordinary/off negative controls; not prompt-only enablement |
| 09 | Private frozen CodeGraph provisioning and tool health | Version/graph integrity, executable/tool health, no automatic project indexing |
| 10 | Canonical Pi Context7 integration | Real MCP schema, registration, readiness and failure readback on each target |
| 11 | Private GGA provisioning and explicit hook boundary | CLI health, no incidental project hooks; selected project behavior independently verified |
| 12 | Common Main channel acquisition/consent/readback | Frozen snapshot, stable default, age/signature/hash qualification; no implicit activation |
| 13 | Finish SDD retirement across startup/migration/ownership | Legitimate1952 authority continuation; unchanged user bytes/history and ordinary-agent controls |
| 14 | Compatible native/SDK packed publication and frozen graph binding | Complete prepack/packed lifecycle, authentic provenance/signatures/SRIs/age; no version invented |
| 15 | Linux actual matrix | All available choices, both launchers, cancel/invalid/update/force/recovery/manager and preservation |
| 16 | Darwin actual matrix | Native Apple silicon/macOS14+ installation and available Shared/recovery plus all choices |
| 17 | Windows actual matrix | Native Win11 x64 Separate, all choices, both launchers, custody/cancellation/preservation; Server/WSL not substitutes |
| 18 | Release-facing acceptance/readiness | Exact artifacts, complete per-item receipts, human visual acceptance and no unresolved failures |

## Verification and current blockers

Tests are test-first where applicable: distinguish behavioral RED from unsupported-interface/compiler RED and infrastructure failure. Run relevant suites, negative controls, normalization and functional checks before exact-candidate native review/ACK. An approved receipt cannot approve future edits. Independent read-only verification is required for high-risk changes and source delivery follows repository policy.

No host source SDK/compiler/test/formatter execution. Reuse the existing offline credentialless asserted Guest, without sudo, Docker, a new lab, copied stock helpers, fake ownership or relaxed limits. Source execution summaries and physical unit completion are separate evidence.

- The existing Linux Guest has an independently diagnosed systemd cgroup inotify ENOSPC → stop timeout, despite some test children exiting0. Do not call that full Guest PASS; restoring host capacity requires an authorized operator action, not a source workaround.
- No confirmed authentic Win11 x64 acceptance environment is recorded here. Hosted Windows Server checks and cross-builds do not close it.
- Darwin qualification CI succeeded on the prior source; its artifact fetch failed with a network error and no receipt contents have been admitted. Preserve the failed fetch; do not infer every new item passed.
- Published4.0.0 lacks the new RDD interface and pending SDK fixes. Source-only progress does not authorize signing, release, pins or a preview activation.
- #1952's prior review STOP has no negotiated disposition/continuation; do not bypass it with another lineage or invent the six recovery inputs.

## Delivery and progress

Only dedicated isolated worktrees are used. `main-current`, active preview, official hardlinks, Pi package registration and personal preferences stay unchanged. Every reviewed unit records parent/commit/tree, changed lines, commands, exact results, omissions and rollback boundary.

No Main merge, auto-merge, Main push, release/tag/signing/pin or runtime activation is inferred from implementation authorization. Request exact current authority when a real delivery boundary needs it. Do not close any umbrella issue until all claimed items have named target-platform evidence.

- [x] Actual merged source acquired in isolated native/SDK worktrees; source revisions recorded.
- [x] Common UI/consumer gaps mapped, not inferred from merge status.
- [ ] Work units01–14 implemented, tested, independently checked where required and exact-reviewed.
- [ ] Platform matrices15–17 admitted against the same authentic installed artifacts.
- [ ] Human acceptance and distribution gate18 complete.

Unit01 source groundwork: `e0b0a773`,249 changed lines including this roadmap; unit02a opt-in embedding: `4abda8b6`,204 lines; both exact reviewed/ACKed, with retained Guest controller failures. Unit02b connects the ordinary application to guarded execution only after its TUI returns; newer source checks have observed controller exit0, not an authorized lab repair or closure of prior failures. Unit03a adds Linux real-PTY public selection/confirm/cancel and physical terminal-restoration coverage with an inert worker-boundary spy; no acquisition or installed-platform acceptance is inferred. Actual common experience controls, packed distribution and installed-platform qualification remain open.
