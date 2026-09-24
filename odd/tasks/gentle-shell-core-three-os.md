# Gentle-Shell core installer on three operating systems

## Goal

Deliver a Gentle AI-owned, explicitly chosen `pi` or `gentle-shell` terminal installation on Linux, Darwin, and Windows for Stable (exact reviewed npm version/integrity) or Main (full lowercase source SHA). Inspect and approve source, commands, scope, physical instance, and any removal separately; install after approval; independently verify Ready **before** persisting managed state. Recommend ODD, leave SDD unmanaged, and keep GGA optional/off.

## Starting point

- Isolated branch `feat/gentle-shell-core-three-os-01` at reviewed documentation parent `9442ab6fd247e776863325bde0b1ac27951e63a4` (draft PR `Gentleman-Programming/gentle-ai#4936`). This is a clean starting point, not completion of the core terminal route.
- Dirty prototype `/home/devel/projects/gentle-ai-worktrees/gentle-shell-runtime` and reserved `main-current` are out of scope for edits or test execution. Reuse behavior only after focused read-only mapping and strict test-first implementation.
- Reviewed terminal-entrypoint guard exists separately at `1c485439` in `shell-terminal-entrypoint-01`; it rejects `gentle-shell` pending a complete independent route. The clean parent has no `internal/shellinstaller` package, so cherry-picking only this guard is not dependency-closed. Use the older branch as a behavior reference, not a source transplant; never enable a profile value alone.
- Gentle-Shell fork PR #5 has an escalated native review without a recovered exact finding. PR #6 deliberately disables Windows external-ready marker access. A plain Windows default-home launch without a marker can run ordinary automatic setup before Pi; this **blocks** claiming a complete three-OS Gentle-Shell route. The separate upstream worktree `shell-windows-external-ready-01` tracks a safe persistent handoff. A marker is advisory, never proof of Ready.
- Historical real Main Apply reached final Verify and returned `Pi package settings could not be read.` Specific cause is unverified. No real Apply retry until a safe reproduction and verified candidate exist.

## Tasks

- [ ] Build a fresh minimal `internal/shellinstaller` domain foundation on this clean parent: Stable/Main channel identity without a floating Main source spec, valid terminal identifiers, Pi/Stable defaults, and fail-closed profile validation that rejects `gentle-shell` until the independent route is complete. Strict focused RED/GREEN tests and a ≤400-line reviewable work-unit commit.
- [ ] Bind exact Stable/Main Gentle-Shell source and immutable **executable** identity at inspection and Apply, not merely manifest/bin metadata; bind resolved home and runtime. Keep each independently reviewed slice at most 400 changed lines.
- [ ] Build independent Gentle-Shell inspection, explicit choice, conflict/removal consent, and approval-bound install plan; reject stale source or physical instance at execution. Split into dependency-closed reviewed units.
- [ ] Implement execution and independent Ready verification before state persistence, including safe public typed failures, rollback boundaries, and the approved post-Ready external handoff. Windows remains blocked until native launcher read/write security and normal launch behavior are proven.
- [ ] Wire explicit `pi`/`gentle-shell` Stable/Main choices through the TUI without changing Welcome hierarchy (`Logo → tagline/advisory → hero → menu`), ODD-first defaults, or pre-/post-failure consent boundaries. Keep private failure-report saving `ErrUnsupported` on Darwin and Windows.
- [ ] Prove a minimal-root, allowlisted, network-denied disposable CLI test sandbox before running dirty-prototype CLI tests; then reproduce and classify the historical Main settings-read failure without reading or changing real Pi configuration.
- [ ] Execute disposable-home and native Linux/Darwin/Windows tests for both terminal choices and channels; investigate the separately observed Windows routing-guidance install-cursor failure. Report skipped/blocked cells rather than assuming parity, and require exact separate user approval before any preview, publication, real Apply, or merge.

## Review and safety

- Strict TDD for behavior changes; each dependency-closed unit has its own work-unit commit with tests/docs, independent review, and no more than 400 changed lines. Fork feature-branch draft PRs may target pinned non-`main` bases. No merge, auto-merge, push to `main`, or merge API in Gentleman-Programming repositories without fresh exact repository/action approval.
- No real Apply, global installation, preview replacement, real Pi configuration change, or optional Darwin/Windows private report writer is authorized.
- Public diagnostics never include paths, argv, raw errors, stderr, URLs, package specs, or private report contents. Gentle Engram Stable is reviewed and pinned; no Engram Main publication path.
- Do not set `GENTLE_AI_NO_ANIMATION` for TUI tests.
- Engram mirror `odd/gentle-shell-core-three-os/tasks` is pending: the local Engram service cannot resolve its identity. This file is the recoverable record until it succeeds.

## Evidence

First unit: six new `internal/shellinstaller` files, with RED before implementation and GREEN in an isolated test environment. An independent verifier ran the full focused package (`go test ./internal/shellinstaller -count=1 -v` with disposable HOME/XDG/Pi overrides and Go caches, networking disabled for Go modules): three tests and all subtests passed, with no skips; `gofmt -l` returned no files. The complete untracked inventory was exactly those six Go files and this task document; they total 188 added diff lines. No CLI, TUI, native cross-OS, Apply, or live settings test was run. Native review and work-unit commit remain pending, so the first task is not yet closed. Parent draft `#4936` was previously observed green at CI `35952744377`; that does not validate this route or this new branch.
