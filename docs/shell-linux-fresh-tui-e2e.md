# Fresh TUI install and launch driver

Standard-library driver for a **qualified disposable Linux guest**: fresh
`supervisor shell install` via PTY, manifest/bindings checks, project launch,
local `/gentle:status`, and separate guest Pi fixture preservation.

## Integration command (inside the qualified guest only)

```sh
python3 /tests/shell_linux_fresh_tui.py --execute-in-guest \
  --supervisor /usr/local/bin/supervisor --supervisor-sha256 "$SUPERVISOR_SHA256" \
  --source-sha "$IMPLEMENTATION_SHA" --target /work/private-parent/fresh-shell \
  --project /work/project --pi-fixture /work/existing-pi > /work/fresh-tui.json
```

Supply a reviewed, committed modern supervisor; no installer WIP is copied.
The target must be absent, including dangling links; directories must be
physical, private, owned and disjoint. Use a real credentialless Pi fixture
(executables, packages, configuration), not a sentinel. Inventory covers metadata,
hashes and symlink text without following links. Caller SHA is not build provenance.

## Execution boundary and evidence

The caller must qualify isolation, acquisition transport, cgroup limits or
an existing delegated manager, clean HOME/TMPDIR, and fixture identity first.
Only HOME/TMPDIR/XDG_RUNTIME_DIR/DBUS_SESSION_BUS_ADDRESS are inherited;
no token, proxy or package-manager settings are forwarded.
The consent flag does **not** establish OS isolation. Do not run on real WSL/Pi.
JSON records hashes, exits, steps and contract failure reasons; raw output and external exception text stay withheld.
Failure preserves artifacts for investigation; it does not claim rollback or
absence of effects. Stop/reap covers owned and foreground process groups;
guest cgroup qualification remains responsible for escaped descendants.

CI runs fake-supervisor harness controls only, without network, credentials
or host mounts, with private HOME/TMPDIR and executable guest tmpfs for fakes.
It does not execute Gentle Shell, prove upgrade, qualify the manager or declare Ready.
Real integration must retain source/binary identities, driver JSON and guest evidence.
Rollback removes only this driver, tests, image, workflow and document.
