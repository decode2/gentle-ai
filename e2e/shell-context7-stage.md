# Internal POSIX Separate canonical MCP stage

`userStageContext7` produces `agent/mcp.json` for a private, unpublished stage.
On/off use the fixed official endpoint `https://mcp.context7.com/mcp`, explicit
`enabled` and `codemode` exposure. An absent choice is an effect-free no-op.

The writer requires physical private stage/agent directories and an empty agent
home. It refuses Shared, existing files/links and unsafe identities/modes. It
reuses rooted exclusive writes, file/directory synchronization, native checksum
custody and directory identity readback. It does not inspect config/persona or
projects. The caller owns cleanup; this is not hostile same-UID atomicity.

## Evidence boundary

- Exact official on/off bytes and mode0600 are tested without network access.
- Frozen Pi1.0.0 CLI consumes the producer's **unchanged off file** and reports
  the official URL, source, global scope, disabled state, codemode and zero tools.
- Other CLI transport fixtures use the existing exclusive-write primitive with
  a local URL. They are not fixed-endpoint Context7 producer output, and they do
  not prove actual Context7 health. No endpoint override or interception exists.

Public nonnil profiles and the lower-level experience validator remain refused.
The writer is not wired into installation, a Windows implementation, or a
release-ready choice. Physical confirmation, producer integration, authentic
whole-product binding, installed target matrices and Ready remain pending.
Trusted project config can override global entries; file-only `mcp list` health
is not an active session's permission-qualified tool execution.
