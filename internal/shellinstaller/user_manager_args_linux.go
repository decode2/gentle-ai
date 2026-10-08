//go:build linux

package shellinstaller

const userCapabilityDrop = "/usr/bin/setpriv"

func userServiceArgs(unit string, interactive bool, self, cwd string, args, env []string) []string {
	result := []string{"--user", "--quiet", "--wait", "--collect", "--service-type=exec", "--expand-environment=no", "--description=Gentle Shell owned runtime", "--unit=" + unit, "--working-directory=" + cwd,
		"--property=MemoryMax=3221225472", "--property=MemorySwapMax=0", "--property=CPUQuota=100%", "--property=CPUQuotaPeriodSec=100ms",
		"--property=TasksMax=64", "--property=NoNewPrivileges=yes", "--property=UMask=0077", "--property=KillMode=control-group", "--property=TimeoutStopSec=2s",
		"--property=UnsetEnvironment=LD_PRELOAD LD_LIBRARY_PATH LD_AUDIT NODE_OPTIONS NODE_PATH"}
	if interactive {
		result = append(result, "--pty")
	} else {
		result = append(result, "--pipe")
	}
	result = append(result, "/usr/bin/env", "-i")
	result = append(result, env...)
	return append(append(result, userCapabilityDrop, "--inh-caps=-all", "--ambient-caps=-all", "--no-new-privs", "--", self, "shell"), args...)
}
