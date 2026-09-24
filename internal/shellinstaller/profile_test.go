package shellinstaller

import "testing"

func TestProfile(t *testing.T) {
	t.Run("new install defaults to stable Pi", func(t *testing.T) {
		profile := NewInstallDefaults()
		if profile.Channel != ChannelStable {
			t.Errorf("Channel = %q, want %q", profile.Channel, ChannelStable)
		}
		if profile.TerminalEntryPoint != TerminalEntryPointPi {
			t.Errorf("TerminalEntryPoint = %q, want %q", profile.TerminalEntryPoint, TerminalEntryPointPi)
		}
		if err := profile.Validate(); err != nil {
			t.Fatalf("default profile should validate: %v", err)
		}
	})

	t.Run("accepts legacy zero and explicit Pi targets", func(t *testing.T) {
		for _, target := range []TerminalEntryPoint{"", TerminalEntryPointPi} {
			profile := Profile{Channel: ChannelStable, TerminalEntryPoint: target}
			if err := profile.Validate(); err != nil {
				t.Errorf("target %q should validate: %v", target, err)
			}
		}
	})

	t.Run("rejects unauthorized or forged targets", func(t *testing.T) {
		for _, target := range []TerminalEntryPoint{TerminalEntryPointGentleShell, "other-terminal"} {
			profile := Profile{Channel: ChannelStable, TerminalEntryPoint: target}
			if err := profile.Validate(); err == nil {
				t.Errorf("target %q should be rejected", target)
			}
		}
	})

	t.Run("rejects invalid channels", func(t *testing.T) {
		for _, channel := range []Channel{"", "beta"} {
			profile := Profile{Channel: channel, TerminalEntryPoint: TerminalEntryPointPi}
			if err := profile.Validate(); err == nil {
				t.Errorf("channel %q should be rejected", channel)
			}
		}
	})
}
