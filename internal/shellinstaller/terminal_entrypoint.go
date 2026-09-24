package shellinstaller

// TerminalEntryPoint identifies a syntactically recognized terminal target.
type TerminalEntryPoint string

const (
	TerminalEntryPointPi          TerminalEntryPoint = "pi"
	TerminalEntryPointGentleShell TerminalEntryPoint = "gentle-shell"
)

func (entryPoint TerminalEntryPoint) Valid() bool {
	return entryPoint == TerminalEntryPointPi || entryPoint == TerminalEntryPointGentleShell
}
