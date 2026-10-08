# Gentle Shell from the Windows installer TUI

**Implementation candidate, not yet qualified for Windows users.** This change
ports the existing Separate installer TUI to Windows 11 x64. Actual Windows
installation, both UI openings and terminal restoration remain unverified.
Cross-compilation is not a substitute for those checks.

## Candidate path

From a build of this feature, in a normal **non-Administrator** Windows terminal:

```powershell
gentle-ai shell install
```

Choose a private local NTFS destination, review its physical selection, and
confirm installation. After a successful installation, the owned bindings are
`TARGET\bin\gentle-shell.cmd` and `TARGET\bin\pi.cmd`. No personal PATH,
shell profile, existing Pi or personal configuration is overwritten.
These commands are candidate instructions, not a claim that stock native 4.0.0
already contains this unreleased installer.

## Review boundaries

| Boundary | Implementation |
|---|---|
| Scope | Windows 11 x64, Separate only; Server and ARM64 refused |
| Tooling | Private pinned Node 24.18.0 and Go 1.27.1 ZIPs |
| Composition | Stock Pi 1.0.0 and gentle-pi 4.0.0, complete SHA512 source lock, no implicit lifecycle scripts |
| Native | Explicit authenticated stock Go SumDB source-build API for native 4.0.0 |
| Ownership | Current SID, private DACLs, NTFS volume/file IDs, no reparse/drive aliases or selected hard links |
| Publication | Owned sibling stage, confirmation readback, no replace-existing rename |
| Processes | Suspended child bound before resume; Job Object CPU/memory/process readback, inherited console, bounded cleanup |
| Preservation | Isolated HOME/agent/config, caller CWD inherited; console modes saved/restored; personal PATH unchanged |

Job committed-memory limits do not claim to disable Windows' pagefile.
Ownership controls assume cooperative same-account actors; they are not
hostile same-SID custody, loaded-byte attestation or escaped-descendant immunity.

Bootstrap bounds: Node ZIP 64 MiB, Go ZIP 128 MiB, tools 32 MiB; authenticated
bootstrap executables up to 128 MiB; complete Windows inventory up to 2 GiB.
The old Linux controls and frozen Linux work are unchanged.

## Acceptance still required

- [ ] Original exact-head formatting, builds and applicable tests pass.
- [ ] Credentialless, resource-bounded **actual Windows 11 x64** laboratory is independently established.
- [ ] A user installs through the existing TUI, not a fake installer or helper.
- [ ] Both owned bindings open authentic UIs and settle their exits.
- [ ] Caller CWD/console/foreground and personal configuration/project preimages are preserved.
- [ ] Required target checks and exact-head Windows smoke pass before ready-for-review.

`shell-windows-crosscheck.yml` runs formatting, portable TUI tests, JavaScript
syntax and Windows cross-compilation in a bounded Linux Guest. It explicitly
does **not** run Windows, establish a Windows laboratory or qualify this feature.
After successful checks it exports the exact-head Windows product and test
executables with a source-commit record and SHA256 manifest. These are inert
qualification artifacts, not a signed release: extract and execute them only
inside the qualified isolated Windows Guest, never on the operator's system.
Shared, update, force/recovery, Darwin, registration and the full Ready contract
are outside this minimum change.

## Preparing the shared parent TUI

`cli.NewShellInstallModel(cancel)` provides an opt-in selection-only child. It
starts neither a `tea.Program` nor an installation worker. Confirmation returns
a copied request through `cli.ShellInstallOutcome`; cancellation returns an
unconfirmed terminal outcome and calls the optional callback once. Backend
completion messages are ignored because this child owns no backend operation.
An unfinished outcome is not permission to install.

The parent must collect that outcome, finish its own program and restore the
terminal before handing the request to the native entrypoint, which still must
validate the physical selection. This API does not yet connect the shared
Welcome/Configure/Ready route: the existing standalone Windows CLI path remains
unchanged until that integration is implemented and tested.

This unit does not add persona/background persistence or change the Windows
five-value/default-channel and seven-value/explicit-channel entry protocol.
It does not accept Linux's sixth profile value. State-machine fixture tests do
not prove Windows installation, physical consent or terminal restoration.

## Integration and rollback

The feature starts from Main, not the unmerged Linux PR #5242. It reuses that
installer's TUI/request vocabulary without importing the large Linux backend.
When Linux is integrated by a separately authorized maintainer, reconcile the
shared portable route and unsupported-platform build tag; do not replace a
working Linux backend with this change's unsupported-platform stub.

Rollback removes the early `shell` app route and the new Windows installer,
helper, tests, documentation and static-check workflow together. It does not
remove a user's existing Pi, project, personal configuration or installed data.
Delivery leaf: #5271 under canonical cross-platform tracker #4935. No Main
merge, release or issue closure is authorized by this candidate.
