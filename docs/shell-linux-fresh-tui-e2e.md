# Fresh TUI install and launch driver

Reusable standard-library driver for a **qualified disposable Linux guest**.
It starts `supervisor shell install` interactively with an absent target,
confirms the physical selection, checks both bindings and the manifest,
launches `gentle-shell` from an explicit project, observes local
`/gentle:status`, and compares a separate guest Pi fixture before/after.

## Integration command (inside the qualified guest only)

```sh
python3 /tests/shell_linux_fresh_tui.py --execute-in-guest \
  --supervisor /usr/local/bin/supervisor --supervisor-sha256 "$SUPERVISOR_SHA256" \
  --source-sha "$IMPLEMENTATION_SHA" --target /work/private-parent/fresh-shell \
  --project /work/project --pi-fixture /work/existing-pi > /work/fresh-tui.json
```

Supply a reviewed supervisor with the modern interactive route. The modern
implementation was uncommitted at authoring; no code is copied from it here.
The target must be absent, including dangling symlinks. Selected directories
must be physical, private, owned and disjoint. Provision a real existing Pi
fixture (executables, packages and configuration) without credentials, not
just a sentinel. Inventory records metadata, hashes and symlink text without
following links. The caller SHA is metadata, not build provenance attestation.

## Execution boundary and evidence

The caller must qualify isolation, acquisition transport, cgroup limits or
an existing delegated manager, clean HOME/TMPDIR, and fixture identity first.
Only HOME/TMPDIR/XDG_RUNTIME_DIR/DBUS_SESSION_BUS_ADDRESS are inherited;
no token, proxy or package-manager settings are forwarded.
The consent flag does **not** establish OS isolation. Do not run on real WSL/Pi.
Captured output is bounded and withheld; JSON records hashes, exits and steps.
Failure preserves artifacts for investigation; it does not claim rollback or
absence of effects. Stop/reap covers owned and foreground process groups;
guest cgroup qualification remains responsible for escaped descendants.

CI runs fake-supervisor harness controls only, without network, credentials
or host mounts. It does not execute Gentle Shell, prove a complete upgrade,
qualify the manager, or declare Ready. Real integration must publish its exact
source/binary identities and retain this driver's JSON and guest evidence.
Rollback of this contribution removes only the driver, tests, image, workflow
and this document; no installer code or prior report needs reverting.
