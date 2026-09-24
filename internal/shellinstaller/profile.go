package shellinstaller

import "fmt"

// Profile contains deterministic new-install channel and terminal choices.
type Profile struct {
	Channel            Channel
	TerminalEntryPoint TerminalEntryPoint
}

// NewInstallDefaults selects the stable channel and Pi terminal.
func NewInstallDefaults() Profile {
	return Profile{
		Channel:            DefaultChannel,
		TerminalEntryPoint: TerminalEntryPointPi,
	}
}

func (p Profile) Validate() error {
	if !p.Channel.Valid() {
		return fmt.Errorf("invalid Gentle-Shell channel %q (valid values: stable, main)", p.Channel)
	}
	// The zero value remains a legacy Pi target; syntactic recognition does not
	// authorize Gentle-Shell as an installation route.
	if p.TerminalEntryPoint != "" && p.TerminalEntryPoint != TerminalEntryPointPi {
		return fmt.Errorf("unsupported terminal entry point %q", p.TerminalEntryPoint)
	}
	return nil
}
