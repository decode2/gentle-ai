package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gentleman-programming/gentle-ai/v4/internal/shellinstaller"
)

const shellInstallHelp = `gentle-ai shell --help      print this help without starting a supervisor
gentle-ai shell install --target /owned/private-parent/shell --mode separate
  --mode shared --prefix /owned/selected-prefix --agent /owned/selected-agent
  --inspect                 print physical-selection confirmation without effects
  --confirm SHA256          approve that exact inspected selection
  --experience JSON         validated persona/background defaults for a new target
No flags: main Gentle AI TUI; choose Install Gentle-Shell, our own agent.
Commands live in TARGET/bin, outside npm's bin.
gentle-ai shell launch ROOT [PI_ARGS...]
  Launch the selected stock Pi; normal use is through TARGET/bin/pi or gentle-shell.
gentle-ai shell recover ROOT inspect
  Replace inspect with its printed confirmation to restore shared preimages.
Requires Linux amd64 and qualified cgroup limits or an existing delegated
systemd user manager >=254. No sudo, delegation creation or container fallback.
`

func parseShellInstall(args []string, stdout io.Writer) (shellinstaller.UserInstallRequest, bool, error) {
	var req shellinstaller.UserInstallRequest
	flags := flag.NewFlagSet("shell install", flag.ContinueOnError)
	flags.SetOutput(stdout)
	flags.StringVar(&req.Destination, "target", "", "owned installation target")
	flags.StringVar(&req.Mode, "mode", "separate", "separate or shared")
	flags.StringVar(&req.SharedPrefix, "prefix", "", "selected existing global Pi prefix")
	flags.StringVar(&req.SharedAgent, "agent", "", "selected existing Pi configuration")
	flags.StringVar(&req.Confirmation, "confirm", "", "physical selection SHA256")
	flags.Func("experience", "validated target-local persona/background JSON", func(raw string) error {
		p, err := shellinstaller.DecodeUserExperience(raw)
		if err == nil {
			req.Experience = &p
		}
		return err
	})
	inspect := flags.Bool("inspect", false, "inspect without installation")
	flags.Usage = func() { _, _ = io.WriteString(stdout, shellInstallHelp) }
	if err := flags.Parse(args); err != nil {
		return req, false, err
	}
	if flags.NArg() != 0 {
		return req, false, errors.New("unexpected shell install positional arguments; run gentle-ai shell install --help for supported flags")
	}
	return req, *inspect, nil
}

// Headless shell route, independent of generic setup. Interactive installation
// belongs to the main app TUI; this package provides its reusable selection model.
func RunShell(args []string, stdout io.Writer) (resultErr error) {
	defer func() {
		var failure *shellinstaller.PrivateRuntimeError
		if errors.As(resultErr, &failure) && (failure.Workspace != "" || failure.Destination != "") {
			resultErr = fmt.Errorf("%w\nPreserve evidence: workspace=%q destination/unit=%q\nFor shared installation recovery, inspect ROOT=workspace/installed or published destination with gentle-ai shell recover ROOT inspect", resultErr, failure.Workspace, failure.Destination)
		}
	}()
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		_, err := io.WriteString(stdout, shellInstallHelp)
		return err
	}
	switch args[0] {
	case "install", "launch", "recover", "internal-check", "internal-install", "internal-launch", "internal-recover":
		// The delegated supervisor re-enters here; internal selectors still
		// repeat kernel qualification and never grant execution authority.
	default:
		return fmt.Errorf("unknown shell command %q; run gentle-ai shell --help", args[0])
	}
	if len(args) == 1 && args[0] == "install" {
		return errors.New("interactive installation belongs to the main Gentle AI TUI; run gentle-ai and select Install Gentle-Shell, our own agent")
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
		preview, previewErr := shellinstaller.PreviewUserInstall(req, token)
		if previewErr != nil {
			return previewErr
		}
		_, err = fmt.Fprintf(stdout, "Confirmation: %s\nCommands: %s/bin/gentle-shell, %s/bin/pi\n%s", token, req.Destination, req.Destination, preview+shellExperienceDisclosure(req))
		return err
	}
	if req.Confirmation != token {
		return errors.New("inspect the physical selection first with --inspect, then pass its --confirm SHA256; run gentle-ai shell install --help for selection flags or gentle-ai shell install for interactive review")
	}
	values, err := shellinstaller.UserInstallEntryValues(req)
	if err != nil {
		return err
	}
	return shellinstaller.RunUserEntry(ctx, self, append([]string{"install"}, values...), os.Stdin, stdout, os.Stderr)
}

// NewShellInstallModel provides the existing selection flow without starting a
// Bubble Tea program. The containing program owns terminal release and execution.
func NewShellInstallModel(cancel func()) tea.Model {
	return shellInstallModel{cancel: cancel, req: shellinstaller.UserInstallRequest{Mode: "separate"}}
}

// ShellInstallOutcome reads a selection without executing it. Only a finished,
// confirmed selection is eligible for handoff; the backend still revalidates it.
func ShellInstallOutcome(model tea.Model) (shellinstaller.UserInstallRequest, bool, bool, error) {
	if selection, ok := model.(shellInstallModel); ok {
		return selection.req, selection.confirmed, selection.finished, selection.err
	}
	return shellinstaller.UserInstallRequest{}, false, false, nil
}

type shellInstallModel struct {
	cancel    context.CancelFunc
	req       shellinstaller.UserInstallRequest
	field     int
	review    bool
	confirmed bool
	finished  bool
	preview   string
	width     int
	height    int
	scroll    int
	err       error
}

func (m shellInstallModel) Init() tea.Cmd { return nil }

func (m shellInstallModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = max(0, size.Width), max(0, size.Height)
		m.scroll = min(m.scroll, m.lastReviewOffset())
		return m, nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if key.String() == "ctrl+c" || key.String() == "esc" {
		if m.cancel != nil {
			m.cancel()
		}
		m.confirmed, m.finished = false, true
		return m, tea.Quit
	}
	if m.review {
		if m.scrollReview(key.String()) {
			return m, nil
		}
		if key.String() != "y" {
			m.review, m.scroll = false, 0
			return m, nil
		}
		m.confirmed, m.finished = true, true
		return m, tea.Quit
	}
	switch key.String() {
	case "tab":
		fields := 2
		if m.req.Mode == "shared" {
			fields = 4
		}
		m.field = (m.field + 1) % fields
	case "enter":
		token, err := shellinstaller.InspectUserInstall(m.req)
		m.preview = ""
		if err == nil {
			m.preview, err = shellinstaller.PreviewUserInstall(m.req, token)
		}
		m.err = err
		if err == nil {
			m.req.Confirmation, m.review, m.scroll = token, true, 0
		}
	case "left", "right":
		if m.field == 1 {
			if m.req.Mode == "separate" {
				m.req.Mode = "shared"
			} else {
				m.req.Mode, m.req.SharedPrefix, m.req.SharedAgent = "separate", "", ""
			}
		}
	default:
		fields := []*string{&m.req.Destination, nil, &m.req.SharedPrefix, &m.req.SharedAgent}
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

func (m shellInstallModel) content() string {
	rows := []string{"Gentle Shell Linux user installer", "Target: " + m.req.Destination, "Mode: " + m.req.Mode, "Shared prefix: " + m.req.SharedPrefix, "Shared agent: " + m.req.SharedAgent,
		"Commands: " + m.req.Destination + "/bin/gentle-shell and " + m.req.Destination + "/bin/pi", "Tab selects field; arrows change mode; Enter reviews; Escape cancels."}
	rows[m.field+1] = "> " + rows[m.field+1]
	if disclosure := shellExperienceDisclosure(m.req); disclosure != "" {
		rows = append(rows, disclosure)
	}
	if m.review {
		if m.preview != "" {
			rows = append(rows, m.preview)
		}
		rows = append(rows, "Confirm this physical selection and both command bindings? y installs; non-scroll keys edit.", m.req.Confirmation)
	}
	if m.err != nil {
		rows = append(rows, m.err.Error())
	}
	return strings.Join(rows, "\n")
}
