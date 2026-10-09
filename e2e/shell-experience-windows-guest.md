# Run the private Windows experience tests

This is a source-test package, **not an installer or a qualified release**.
No Go or Node installation is required in the Windows guest.

## Scope and prerequisites

Use the selected existing Windows 11 x64 VM, a normal unelevated account,
and a fresh private local NTFS folder. Do not run on personal Windows.
The ordinary source tests and every public nonnil profile refusal remain unchanged.

The package contains `experience.exe` and its externally recorded SHA-256.
Copy it into the private NTFS folder and verify that hash before execution.
Never bypass Defender, SmartScreen, execution policy, ownership or ACL refusals.
Transfer requires an existing authenticated channel or separately approved media
attachment. Do not enable services, retrieve historical credentials, or replay
VM provisioning/boot markers. Physical network disconnection requires approval.

## Execute and retain evidence

Run `experience.exe` with **no arguments** from normal PowerShell in the VM.
Its fixed worker runs only the three `TestUserWindowsStageExperience` tests:
canonical files, preimage preservation and before-write refusals.
It rejects custom selectors, list-only runs and extra flags.

The existing native supervisor binds the suspended child to a verified Job
Object before resuming it: 3 GiB committed memory, 64 processes, one logical
CPU share. The tests have a 45-second timeout; the supervisor has 60 seconds.
The worker uses fresh private HOME/TEMP/AppData paths and a minimal environment.
These bounds do not disable the Windows pagefile or physically isolate networking.
No network acquisition is part of these filesystem tests.

Record the image hash, guest OS/architecture/token, complete test output, exit
code and `EXPERIENCE_WORKER_QUIESCED`. The private result directory is preserved.
A compiled binary or an absent failure is not a native Windows PASS. Installed
SDK consumers, lifecycle, background scheduling and three-OS acceptance remain open.
