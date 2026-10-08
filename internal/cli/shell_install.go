package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gentleman-programming/gentle-ai/v4/internal/shellinstaller"
)

const shellInstallHelp = `gentle-ai shell install --target C:\owned\shell --mode separate
  --channel stable|main     default stable; Main freezes the canonical source snapshot
  --inspect                 print physical-selection confirmation without effects
  --confirm SHA256          approve that exact inspected selection
No flags: existing installer TUI. Owned commands live in TARGET\bin; personal PATH stays unchanged.
Requires Windows 11 x64, a normal unelevated account and a private local NTFS directory.
Separate only. No Shared, update, force, recovery, Windows Server or ARM64 qualification.
`

func parseShellInstall(args []string, stdout io.Writer) (shellinstaller.UserInstallRequest, bool, error) {
	var req shellinstaller.UserInstallRequest
	flags := flag.NewFlagSet("shell install", flag.ContinueOnError)
	flags.SetOutput(stdout)
	flags.StringVar(&req.Destination, "target", "", "owned installation target")
	flags.StringVar(&req.Mode, "mode", "separate", "separate or shared")
	flags.StringVar(&req.Channel, "channel", "stable", "stable or main")
	flags.StringVar(&req.SharedPrefix, "prefix", "", "selected existing global Pi prefix")
	flags.StringVar(&req.SharedAgent, "agent", "", "selected existing Pi configuration")
	flags.StringVar(&req.Confirmation, "confirm", "", "physical selection SHA256")
	inspect := flags.Bool("inspect", false, "inspect without installation")
	flags.Usage = func() { _, _ = io.WriteString(stdout, shellInstallHelp) }
	if err := flags.Parse(args); err != nil {
		return shellinstaller.UserInstallRequest{}, false, err
	}
	if flags.NArg() != 0 {
		return shellinstaller.UserInstallRequest{}, false, errors.New("unexpected shell install positional arguments; run gentle-ai shell install --help for supported flags")
	}
	channel, err := shellinstaller.UserInstallChannel(req.Channel)
	if err != nil || req.Channel == "" {
		return shellinstaller.UserInstallRequest{}, false, errors.New("invalid channel; use stable or main; run gentle-ai shell install --help for selection flags")
	}
	req.Channel = channel
	return req, *inspect, nil
}

// Dedicated early route, deliberately independent of the generic installer,
// profile detector, self-update and ordinary Gentle AI startup TUI.
func RunShell(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		_, err := io.WriteString(stdout, shellInstallHelp)
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if args[0] != "install" {
		return shellinstaller.RunUserEntry(ctx, self, args, os.Stdin, stdout, os.Stderr)
	}
	if len(args) == 1 {
		model := shellInstallModel{ctx: ctx, cancel: cancel, self: self, stdout: stdout, req: shellinstaller.UserInstallRequest{Mode: "separate", Channel: "stable"}}
		final, err := tea.NewProgram(model, tea.WithInput(os.Stdin), tea.WithOutput(stdout)).Run()
		if err != nil {
			return err
		}
		return final.(shellInstallModel).err
	}
	req, inspect, err := parseShellInstall(args[1:], stdout)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	token, err := shellinstaller.InspectUserInstall(req)
	if err != nil {
		return err
	}
	if inspect {
		_, err = fmt.Fprintf(stdout, "Channel: %s\nConfirmation: %s\nCommands: %s, %s\n", req.Channel, token, filepath.Join(req.Destination, "bin/gentle-shell.cmd"), filepath.Join(req.Destination, "bin/pi.cmd"))
		return err
	}
	// guard:population windows-separate-confirmation fail-closed: legitimate explicit installations carry the current token from the owned physical selection; missing or mismatched confirmations remain excluded without starting a worker
	if req.Confirmation != token {
		return errors.New("inspect the physical selection first with --inspect, then pass its --confirm SHA256; run gentle-ai shell install --help for selection flags or gentle-ai shell install for interactive review")
	}
	return shellinstaller.RunUserEntry(ctx, self, append([]string{"install"}, shellEntryValues(req)...), os.Stdin, stdout, os.Stderr)
}

func shellEntryValues(req shellinstaller.UserInstallRequest) []string {
	channel, _ := shellinstaller.UserInstallChannel(req.Channel) // Worker rejects invalid values.
	return []string{req.Destination, req.Mode, req.SharedPrefix, req.SharedAgent, req.Confirmation, "--channel", channel}
}

// NewShellInstallModel creates a selection-only child of the main TUI.
// It neither starts a program nor executes an installation command.
func NewShellInstallModel(cancel func()) tea.Model {
	return shellInstallModel{
		cancel: cancel, embedded: true,
		req: shellinstaller.UserInstallRequest{Mode: "separate", Channel: "stable"},
	}
}

// ShellInstallOutcome copies the terminal selection for execution after the
// parent restores its terminal. An unfinished model is not authorization.
func ShellInstallOutcome(model tea.Model) (shellinstaller.UserInstallRequest, bool, bool, error) {
	m, ok := model.(shellInstallModel)
	if !ok || !m.embedded {
		// refusal:by-design world-action: the caller must construct an embedded model before reading its outcome; a CLI command cannot correct this API misuse.
		return shellinstaller.UserInstallRequest{}, false, false, errors.New("expected an embedded shell install model; use cli.NewShellInstallModel before reading its outcome")
	}
	return m.req, m.confirmed, m.finished, m.err
}

type shellInstallDone struct{ err error }

type shellInstallModel struct {
	ctx       context.Context
	cancel    context.CancelFunc
	self      string
	stdout    io.Writer
	req       shellinstaller.UserInstallRequest
	field     int
	review    bool
	busy      bool
	err       error
	embedded  bool
	confirmed bool
	finished  bool
}

func (m shellInstallModel) Init() tea.Cmd { return nil }

func (m shellInstallModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.embedded && m.finished {
		return m, nil
	}
	if done, ok := msg.(shellInstallDone); ok {
		if m.embedded {
			return m, nil // Selection-only children own no backend completion.
		}
		m.busy, m.err = false, done.err
		return m, tea.Quit
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if key.String() == "ctrl+c" || key.String() == "esc" {
		if m.cancel != nil {
			m.cancel()
		}
		if m.embedded {
			m.finished = true
			return m, tea.Quit
		}
		if m.busy {
			return m, nil // Await actual stop/reap; never abandon the install goroutine.
		}
		return m, tea.Quit
	}
	if m.busy {
		return m, nil
	}
	if key.Type == tea.KeyRunes && strings.IndexFunc(string(key.Runes), unicode.IsControl) >= 0 {
		// Windows console modifier records can carry a NUL character instead of text.
		// Never turn those records into path bytes or let them dismiss physical review.
		if key.Paste || len(key.Runes) > 1 {
			m.err = errors.New("destination text contains control characters; input refused; enter a path without control characters or run gentle-ai shell install --help")
		}
		return m, nil
	}
	if m.review {
		if key.String() != "y" {
			m.review = false
			return m, nil
		}
		if m.embedded {
			m.confirmed, m.finished = true, true
			return m, tea.Quit
		}
		m.busy = true
		return m, func() tea.Msg {
			err := shellinstaller.RunUserEntry(m.ctx, m.self, append([]string{"install"}, shellEntryValues(m.req)...), os.Stdin, m.stdout, os.Stderr)
			return shellInstallDone{err}
		}
	}
	switch key.String() {
	case "tab":
		m.field = (m.field + 1) % 3
	case "enter":
		token, err := shellinstaller.InspectUserInstall(m.req)
		m.err = err
		if err == nil {
			m.req.Confirmation, m.review = token, true
		}
	case "left", "right":
		// Separate remains fixed; only the channel field toggles.
		if m.field == 2 {
			if m.req.Channel == "main" {
				m.req.Channel = "stable"
			} else {
				m.req.Channel = "main"
			}
		}
	case " ":
		if m.field == 0 {
			m.req.Destination += " "
		}
	default:
		fields := []*string{&m.req.Destination, nil, nil}
		if field := fields[m.field]; field != nil {
			if key.Type == tea.KeyBackspace && len(*field) > 0 {
				value := []rune(*field)
				*field = string(value[:len(value)-1])
			} else if key.Type == tea.KeyRunes {
				*field += string(key.Runes)
			}
		}
	}
	return m, nil
}

func (m shellInstallModel) View() string {
	if m.busy {
		return "Installing selected Gentle Shell. Ctrl-C cancels; waiting for stop/reap.\n"
	}
	rows := []string{"Gentle Shell Windows 11 x64 user installer", "Target: " + m.req.Destination, "Mode: Separate (private Node/Go/Pi; personal installation preserved)",
		"Channel: " + m.req.Channel + " (Left/Right selects; Main resolves once after confirmation)",
		"Commands: " + filepath.Join(m.req.Destination, "bin/gentle-shell.cmd") + " and " + filepath.Join(m.req.Destination, "bin/pi.cmd"),
		"Tab selects field; Enter reviews; Escape cancels. Personal PATH and configuration are not changed."}
	rows[m.field+1] = "> " + rows[m.field+1]
	if m.review {
		rows = append(rows, "Confirm this physical selection and both command bindings? y installs; any other key edits.", m.req.Confirmation)
	}
	if m.err != nil {
		rows = append(rows, m.err.Error())
	}
	return strings.Join(rows, "\n") + "\n"
}
