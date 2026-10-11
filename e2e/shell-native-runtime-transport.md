# Native installer process transport

Linux amd64 Go client → Alan's shared factory/runner. **Not production TUI routing or installed acceptance.**

## Caller responsibilities

- Authenticate the **whole immutable supplier**; checking the factory entry's physical identity does not authenticate its imports.
- Use the existing bounded worker and physical confirmation/post-terminal-restoration boundary. This client checks the kernel but creates neither isolation, delegation nor consent.
- Prepare an unpublished stage with `userPrepareInstallStage` and the qualified private Node. HOME/XDG/PNPM_HOME/npm configuration/agent/cwd stay private; caller environment is not inherited.
- Prepare bootstrap prerequisites through the existing native consumer seam. The client does not bootstrap pnpm or reconstruct installation actions.

## Transport contract

`Collect` seals this client's release plan. `Run` recollects, compares that snapshot and calls the genuine runner. Changed selection and native blocked outcomes remain blocked. The full native result is retained: `ready`/exit zero does not prove publication. Existing process-group cancellation, bounded output, wait/reap and error taxonomy are reused; output overflow fails closed.

## Guest check and limits

`go test -tags=shell_native_runtime ./internal/shellinstaller -run '^TestUserNativeInstallerRuntime$' -count=1 -v` uses the authenticated supplier at `/alan-source`. It checks missing pnpm, denied consent, stale selection, escaping private paths, pre-start cancellation/deadline, named writable-tree/Node preservation and caller HOME/cwd. The 1ms deadline does **not** prove after-start cancellation/settlement. No dependency installation or publication is proven.

The default npm worker, manifest, CLI positional ABI and both launchers are unchanged. Source admission, native bootstrap orchestration, progress, PNPM integrity/readbacks and ordinary TUI installed acceptance remain prerequisites for production routing.
