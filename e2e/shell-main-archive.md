# Shared Main source archive preflight

The existing Windows Main worker now uses the portable ZIP scanner and source
inventory. The scanner retains digest, path/type/duplicate, 32768-entry,
32MiB-member and 1GiB aggregate bounds; existing Node/Go recipes retain their
128MiB-member exception. Main requires the canonical 32MiB source descriptor.
Implicit directories are included; file parents and case aliases are refused
before the worker creates its stage. Native/dependency roots remain excluded.

Offline Guest `TestUserMainSourceArchiveInventory`: synthetic ZIP DATA,
including package CRC failure, canceled context, missing package, path/type,
native/dependency custody, descriptor/digest and impossible hierarchy controls.
Three hierarchy/descriptor controls failed before correction; pertinent suite
passes (35 top-level tests, zero SKIP). Guest formatting is clean and all three
required targets compile; Windows/macOS tests were not executed natively.

No publisher/TLS, source API/dependency compatibility, retained POSIX custody,
overlay, launch or installed acceptance is claimed. POSIX Main is not offered.
Runtime installation is N/A for this parser boundary. Rollback: restore Windows
preflight/inventory and remove the shared parser, tests and this record together.
