# SDK Main + stock provider: explicit internal admission

The locally authorized preview is a composition, not an intact Main installation.
Its internal preflight keeps authenticated stock metadata, installer and resolver
as the fixed preservation plan; native/dependency pins remain unchanged.

## Contract

- `assertMainBindings` and ordinary stock `verify` stay unchanged: full Main still
  fails when either supplier differs from the authenticated original.
- New `assertMainSDKPreviewBindings` requires exact stock package metadata and
  resolver bytes. Main's installer must remain bounded source DATA, not a supplier
  admitted for execution. The returned frozen plan preserves all three stock
  binding files; it never directs a caller to copy Main's changed installer.
- New modern Separate-only `verify-main-sdk-source` runs every original stock
  graph/source/supplier/native/settings/lock check before composition admission.
  Its Go wrapper compares complete caller-bound Main custody/source before and
  after the existing owned process. It neither loads source nor writes settings.

## Pertinent proof

The existing offline Guest uses retained official Main commit
`43541a0d30dfafacfcb2e99ef4135ab8b1693e99`, ZIP SHA
`cbc39d46b0d5416435c22498515e5bdfef8b4368e6a8e9d591f265f2de99362e`.
Original Gentle Pi 4.0.0 is authenticated against the committed npm SRI and
original supplier hashes. Actual Main/source/stock byte/stat readbacks stay exact.

The production comparator first rejected the authorized SDK composition at the
changed installer (behavioral RED), then accepted its fixed stock preservation
plan. Full Main, metadata/dependency/export/Pi/engine/hook/unknown changes,
resolver changes and malformed/empty/oversized supplier buffers remain refused.
Both Go entry points refuse unbound/tampered/canceled source before process setup.

Run `go test -tags=shell_main_sdk_admission -run '^TestUserMainSDKPreviewAdmission$' -count=1 -v ./internal/shellinstaller`
inside the existing bounded Guest, with `/frozen-main`, `/frozen-gentle-pi.tgz`
and `/source` mounted read-only. No new network or supplier code is executed.

## Still pending

No copy/activation, launcher, worker, selector or Ready composition label is
implemented by this unit. Future Ready must disclose SDK Main + stock provider.
The full owned stock-helper positive chain remains blocked by the existing
supervisor-custody prerequisite; comparator success is not that qualification.
Three-OS installation, health and advanced effects remain unqualified. No pin
exception, personal activation or publication authority is created.

Rollback removes only the SDK-composition function/action/wrapper and its tests
and document. Keep the original full-Main gate and all stock checks intact.
