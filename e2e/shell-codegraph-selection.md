# CodeGraph selection boundary

`UserExperience` transports an optional `codegraph` choice: `on` or `off`.
Absence preserves historical JSON/defaults; empty/null/type errors and escaped
duplicate keys are refused. Full confirmation bytes bind ON versus OFF.
The existing 256-byte input and Unix/Windows entry argument ABIs are unchanged.

This unit does **not** admit CodeGraph installation. Both explicit choices
remain refused by the real POSIX preparer and generic/Windows stage validator
before destination creation; public nonnil profiles remain refused. Nothing
acquires a library, exposes a CLI, changes personal/project preferences or
indexes a project. ON/OFF runtime effects and Configure UI are later units.

Run `go test -run '^TestUserCodeGraph(Experience|Stage)' ./internal/shellinstaller`
in the existing offline Guest. Also run historical experience/confirmation,
Context7/Strict TDD/GGA, stage and CLI transport regression tests. Rollback is
this optional field/validation plus its tests/docs; no runtime cleanup needed.
