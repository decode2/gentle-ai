# Closed global response replay

This is a Guest-only TLS transport qualification, not PNPM resolution,
installation, publisher verification, publication, or three-OS acceptance.
The caller must establish UID1000/caps0/NNP/private network and immutable RO custody.

Supply `/global-fixture/requests.json`: ≤512 unique routes, ≤256KiB inventory.
Each body must be regular, non-symlink, ≤64MiB and match its size/SHA256.
Unknown routes receive404 before lookup; rejected known bodies receive502.
There is no upstream. Four request workers join on close; owner stop interrupts
open connections, including incomplete handshakes/headers, before joining.

Reports keep32 ASCII-safe samples per bucket, ≤256 bytes each, complete counts
and explicit sample incompleteness; report size remains ≤32KiB.
`TestUserNativeGlobalMirror` uses authenticated Node and genuine TLS:40 original
responses, unknown404 including non-ASCII raw targets, alias502 and four pending
header connections at owner stop. It rechecks Node/project preservation and
retains failures. The seven-response persistence oracle remains separate.
Run `-tags=shell_native_runtime -run '^TestUserNativeGlobalMirror$'` in the
qualified Guest. Full original installation is the next independent unit.
