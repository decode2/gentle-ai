//go:build !windows

package orchestrator

import (
	"os/exec"
	"syscall"
)

// applyProcessPlatformTraits ties each worker to the daemon's life on Unix:
// if the daemon dies for any reason, the kernel SIGKILLs its workers
// (PARENT_DEATH_SIGNAL), so closing the app closes the fleet.
func applyProcessPlatformTraits(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
}
