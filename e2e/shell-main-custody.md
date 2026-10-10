# Private POSIX Main archive custody

The internal stage retains `runtime/main/main.json` and `main.zip` without
applying source or provisioning dependencies. Preflight precedes creation;
existing custody is never replaced. Failures leave cleanup to the stage owner.

Readback is bound to the caller's frozen descriptor, not to mutable retained
JSON. Both files reuse CodeGraph's fixed-size 32MiB custody reader: private
0600, same physical inode/metadata, owned single link, nofollow/nonblock open,
bounded read and settled close. The Main ZIP digest/inventory is revalidated;
parent identities and the two-member private directory are checked.

Offline Guest: two hard-link controls were behavioral RED, then GREEN. The
38-test pertinent suite and authenticated CodeGraph retained-library regression
pass without SKIP; Guest formatting is clean. All three target packages compile
only. A real fixture stage survives relocation/readback without byte/stat drift.
Synthetic ZIP DATA is never executed and establishes no publisher/TLS trust.

No public POSIX Main channel, worker integration, compatibility qualification,
overlay or installed launch is offered. Next: apply only compatible source while
preserving frozen dependencies/native custody. Rollback removes this stage and
its tests/docs and restores the CodeGraph-private reader together.
