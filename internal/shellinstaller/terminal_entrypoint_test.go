package shellinstaller

import "testing"

func TestTerminalEntryPoint(t *testing.T) {
	cases := []struct {
		name  string
		value TerminalEntryPoint
		valid bool
	}{
		{name: "Pi", value: TerminalEntryPointPi, valid: true},
		{name: "Gentle Shell syntax", value: TerminalEntryPointGentleShell, valid: true},
		{name: "empty", value: "", valid: false},
		{name: "forged", value: "other-terminal", valid: false},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.value.Valid(); got != tt.valid {
				t.Errorf("Valid() = %t, want %t", got, tt.valid)
			}
		})
	}
}
