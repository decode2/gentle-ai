//go:build windows

package orchestrator

import "os/exec"

// applyProcessPlatformTraits on Windows: workers inherit the daemon's job
// object, and the desktop shell creates that job with KILL_ON_JOB_CLOSE, so
// exiting the app terminates daemon and workers together. Nothing to set
// here — the default (no breakaway) is the correct behavior.
func applyProcessPlatformTraits(*exec.Cmd) {}
