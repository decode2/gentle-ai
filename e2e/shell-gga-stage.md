# Private GGA configuration, not automatic activation

`userPrepareInstallStage` validates the full immutable profile/mode before any
writes. Explicit GGA is private Separate only; generic/Windows/Shared/public
admission remains refused. ON writes `TARGET/config/gga/config` with the explicitly
selected `PROVIDER`, 0700 directory/0600 file, LF and existing rooted exclusive
writer/sync/custody checks. OFF/absence creates no GGA directory or file.
No persona lookup for unmanaged; no project `.gga`, rules file or git hook write.

## Actual consumer proof

In the existing offline credentialless Guest:

```
go test -tags=shell_gga_canonical -run '^TestUserGGA' ./internal/shellinstaller
```

The frozen source archive for GGA v2.10.1 commit
`1124c3672f082c56b033c4e23a30e95d0e8cd593` is hash-checked before extracting
its four exact CLI/library members. Actual `gga config` reads native-prepared
ON/OFF/absent stages using the installer's private environment and leaves stage
preimages unchanged. Source reports `dev`, not an installed release receipt.
Configured text is not provider installation/authentication/model health or AI
review acceptance. No provider command, personal installation or hook activation.

Rollback removes this private-stage preparation and its tests; keep selection
transport/refusals. CLI provisioning and platform installation remain separate.
