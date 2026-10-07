package shellinstaller

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
)

// Shared mode selects an existing owned global Pi installation and agent
// directory. Both command bindings use those same physical objects. Separate
// mode creates a private installation, HOME, agent, state and project.
type UserInstallRequest struct {
	Destination  string
	Mode         string
	SharedPrefix string
	SharedAgent  string
	Confirmation string
	Experience   *UserExperience
}

type UserInstallResult struct {
	Destination, Prefix, Agent, State string
}

type userManifest struct {
	Schema, Destination, Prefix, Agent, Mode string
	SupervisorSHA, NodeSHA                   string
	PrefixIdentity, AgentIdentity            string
}

const userSchema = "gentle-shell-user-install/v1"
const userCapabilityDrop = "/usr/bin/setpriv"

// Confirmation binds the human's approval to the inspected physical selection,
// not a boolean flag or an unvalidated path alias.
func userConfirmation(req UserInstallRequest, identity string) string {
	selection := []string{req.Destination, req.Mode, req.SharedPrefix, req.SharedAgent, identity}
	if req.Experience != nil {
		encoded, err := req.Experience.Encode()
		if err != nil {
			return "" // Inspection validates the profile before deriving authority.
		}
		selection = append(selection, encoded)
	}
	data, _ := json.Marshal(selection)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func userBinding(root, name string) string {
	root = strings.ReplaceAll(root, "'", "'\\''")
	binding := "#!/bin/sh\nexec '" + root + "/supervisor' shell launch '" + root + "' \"$@\"\n"
	if name == "gentle-shell" {
		binding = "#!/bin/sh\nif test \"${1-}\" = install; then shift; exec '" + root + "/supervisor' shell install \"$@\"; fi\n" + binding[len("#!/bin/sh\n"):]
	}
	return binding
}

func userGraphRepairError(root, mode string, err error) error {
	if mode == "separate" && err != nil {
		return fmt.Errorf("%w\nSeparate repair: preserve target %q and its agent data; install into a different empty target with separate mode. Run gentle-ai shell install --help for inspection and confirmation flags; do not delete or reuse the damaged target", err, root)
	}
	return err
}

func UserInstallFromEntry(args []string) (UserInstallRequest, error) {
	if len(args) != 5 && len(args) != 6 {
		return UserInstallRequest{}, fmt.Errorf("invalid internal install arguments")
	}
	req := UserInstallRequest{
		Destination: args[0], Mode: args[1], SharedPrefix: args[2], SharedAgent: args[3], Confirmation: args[4],
	}
	if len(args) == 6 {
		profile, err := DecodeUserExperience(args[5])
		if err != nil {
			return UserInstallRequest{}, err
		}
		req.Experience = &profile
	}
	return req, nil
}

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
