package shellinstaller

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
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

// Confirmation binds the human's approval to the inspected physical selection,
// not a boolean flag or an unvalidated path alias.
func userConfirmation(req UserInstallRequest, identity string) string {
	data, _ := json.Marshal([]string{req.Destination, req.Mode, req.SharedPrefix, req.SharedAgent, identity})
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func userGraphRepairError(root, mode string, err error) error {
	if mode == "separate" && err != nil {
		return fmt.Errorf("%w\nSeparate repair: preserve target %q and its agent data; install into a different empty target with separate mode. Run gentle-ai shell install --help for inspection and confirmation flags; do not delete or reuse the damaged target", err, root)
	}
	return err
}

func UserInstallFromEntry(args []string) (UserInstallRequest, error) {
	if len(args) != 5 {
		return UserInstallRequest{}, fmt.Errorf("invalid internal install arguments")
	}
	return UserInstallRequest{
		Destination: args[0], Mode: args[1], SharedPrefix: args[2], SharedAgent: args[3], Confirmation: args[4],
	}, nil
}
