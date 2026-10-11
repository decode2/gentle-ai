# Main SDK selection reaches the owned worker

`sdk_source: "main-preview"` in existing experience JSON selects **SDK Main + stock provider**, not intact Main. Omitted or `stable` keeps stock and historical bytes. The 256-byte bound, positional ABI and physical confirmation remain authoritative.

## Review path

1. `user_experience.go`: precise enum, null/empty/duplicate rejection and confirmation-bound encoding.
2. `user_install_flow_unix.go` / `user_main_bindings_unix.go`: after original stock/native checks, acquire bounded Main DATA, retain archive/source, compose, place runtime and select the private package.
3. Optional `main_sdk` manifest metadata dispatches existing-install and launch readbacks to archive-bound selected verification; historical nil stays ordinary stock.

Linux amd64 fresh Separate only. Main optional combinations and unsupported RDD/TDD stay refused; generic preference writers cannot install Main. Existing source/stock/native pins and graph remain unchanged. Partial failure follows existing stage cleanup/uncertainty, not atomic rollback.

## Proof boundary

Run `go test -tags=shell_main_owned_preflight -run 'TestUserMainSDK(ExperienceWire|Worker)$' -count=1 -v ./internal/shellinstaller` **inside the qualified bounded Guest only**. The worker fixture uses retained authenticated inputs, actual production composition/selection, physical rename to different final paths, serialized metadata dispatch and file-backed SDK consumption with independent readbacks. It does not exercise a live acquisition or the complete installed supervisor/manager/launch pipeline.

Re-run original selected/unselected scenarios and stock regressions. No Main installer, native process, model, tool call, personal activation or new network is implied. Public Configure/Ready, actual persistent installation and genuine three-OS/human acceptance remain next work. Reverting this unit removes only the new worker choice/metadata dispatch and its tests; prior private SDK qualification stands.
