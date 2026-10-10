# Shared GGA stays closed before launch

The environment/group fixture now uses production's `manifest.GGA` predicate for
post-exit readback, independently from the Separate-only PATH predicate. The real
`userLaunch` consumer also rejects Shared+GGA manifest DATA at manifest validation,
without stage mutation or CLI presentation. No supervisor/Node/Bash runtime or
successful installed state is fabricated to reach that refusal.

This is coverage strengthening, not a new behavioral RED or a production-policy
change. It addresses `R3-shared-readback-coverage` in a separate candidate; the
original approved receipt stands. Rollback removes only this fixture alignment
and the refusal test. Public GGA installation and installed acceptance stay closed.
