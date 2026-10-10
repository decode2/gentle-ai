# Disabled GGA does not veto a selected profile

The real installer calls `userGGAProvision` after CodeGraph without a GGA guard. Its generic preference validation rejected the already admitted CodeGraph profile even when GGA was absent/OFF. Behavioral RED reproduces that producer-boundary refusal after canonical CodeGraph/Context7 preparation.

Validate the entire raw selection first, then return without effects for unselected GGA. GGA ON retains its existing stage, live-context, Separate, pinned acquisition and readback gates. No public admission, endpoint, credential, hook, provider or generic writer changes. GREEN checks byte/stat tree and request preservation, no private GGA exposure and malformed raw refusal with nil context (no acquisition). Prior producer/experience regressions remain applicable.

This fixes an observed principal-flow blocker, not a hardening campaign. It is component evidence only; cold whole installation, manager/userLaunch, live service and native three-OS acceptance remain pending. Rollback reverses only this guard order and its regression proof; historical authenticated producers remain.
