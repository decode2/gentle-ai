# GGA selection without implicit activation

GGA is optional. This unit transports an explicit `gga: on|off` selection
and `gga_provider` in the existing experience/confirmation contract. It does
not install GGA, authenticate a provider, configure a project, or install hooks.
All public non-nil profiles and explicit GGA stages remain refused before writes.

## Selection contract

- Absence preserves historical bytes and makes no GGA selection.
- OFF carries no provider. ON requires an explicit bounded provider token;
  never derive Claude from the selected Pi runtime.
- Provider syntax follows [GGA v2.10.1 documentation](https://github.com/Gentleman-Programming/gentleman-guardian-angel/tree/v2.10.1):
  Ollama/GitHub require a model; Kiro does not accept an inline model.
- Tokens are at most 64 ASCII letters/digits or `_./:-`; the complete input
  keeps its existing 256-byte bound. This is not provider availability/model health.
- Confirmation binds both the opt-in and provider. JSON null, duplicate/escaped
  duplicate keys, shell syntax and unsupported Pi provider bindings are refused.

## Proof and next boundary

`go test -run '^TestUserGGA' ./internal/shellinstaller` covers canonical encoding,
confirmation differences, malformed input, historical bytes and unchanged
filesystems on unsupported Separate/Shared/generic stage requests.
Run Go only inside the existing offline credentialless verification Guest.
Rollback removes these optional fields, validation and tests together.

Next: qualify the canonical private configuration, authenticated GGA CLI and
actual readback before admitting installation. `gga init`/`gga install` require
explicit per-project consent; configuration readback is not an AI review receipt.
