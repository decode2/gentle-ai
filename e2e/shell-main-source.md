# Exact private Main source materialization

The internal POSIX consumer reads the caller-bound retained Main archive and
writes `runtime/main-source` through the existing exclusive/synced rooted writer.
Files remain non-executable private DATA (0600); directories are private 0700.
Existing source is refused; the stage owner retains cleanup responsibility.

Readback walks the complete tree against the ZIP inventory, including implicit
and empty directories, then checks exact bytes, CRC/size, single-link custody,
parent/source identity and cancellation. Extraction holds one member at a time,
not the whole inflated archive. Runtime stock/prefix objects are not replaced.

Offline Guest: extra file/directory controls were RED, then GREEN. The pertinent
suite has 40 top-level PASS, zero SKIP. Formatting is clean; Linux amd64,
Darwin arm64 and Windows amd64 packages compile only. Real filesystem fixtures
prove read-only admission, relocation and untouched protected sibling DATA.

No ZIP member is executed. Fixtures establish neither TLS/publisher provenance
nor authenticated distribution preservation/health. Public Main channel,
worker integration, package/API/dependency qualification and stock activation
remain closed. Rollback removes this consumer and its tests/document together.
