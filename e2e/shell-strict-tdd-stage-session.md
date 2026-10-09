# Canonical Strict TDD stage → frozen SDK callback

This characterization reuses the existing authenticated archive/extraction
harness (its historical Go name is `TestUserContext7FrozenRegistration`,
tag `shell_context7_registration`). Explicit
`GENTLE_EXPERIENCE_STRICT_TDD_PROBE=1` selects only this Strict TDD proof;
ordinary Context7 controls remain unchanged.

The Go preparer creates actual Separate stages for ON, OFF and absent, with
unmanaged persona and background OFF. It does not fabricate policy JSON.
The real frozen Pi 1.0.0 resource loader then loads the **local source**
`/sdk/extensions/strict-tdd.ts`. Public runner dispatch reads canonical bytes
and returns the additional policy only for ON; baseline, empty history and
callback error readbacks remain explicit.

The scoped SDK options create one private `config/auth.json` (0600, two bytes).
`modelsPath:null` uses an in-memory catalog, not a models-store file.
The first observer incorrectly expected both files and failed **before the
policy callback**. The frozen implementation confirmed that assumption wrong;
the corrected observer admits exactly the actual auth file and config-directory
mtime, preserving every other preimage. This is observer RED, not product RED.
Each callback leaves bytes/mode/inode/mtime unchanged; access-time is not covered.

No synthetic assistant, model/provider request, tool-permission grant, remote
Context7 health, full-package activation or installation is claimed.
A source extension plus cross-builds is not an authentic compatible distribution
or three-OS installed acceptance. Public profiles/Ready remain refused.
