package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gentleman-programming/gentle-ai/v4/internal/shellinstaller"
)

func TestShellInstallFlags(t *testing.T) {
	for _, args := range [][]string{{"--unknown"}, {"extra"}, {"--target"}, {"--confirm"}} {
		if _, _, err := parseShellInstall(args, io.Discard); err == nil {
			t.Fatalf("accepted invalid flags %q", args)
		}
	}
	req, inspect, err := parseShellInstall([]string{"--target", "/owned/shell", "--mode", "shared", "--prefix", "/owned/pi", "--agent", "/owned/agent", "--inspect"}, io.Discard)
	if err != nil || !inspect || req.Mode != "shared" || req.Confirmation != "" {
		t.Fatalf("selection = %+v inspect=%v error=%v", req, inspect, err)
	}
	values, err := shellinstaller.UserInstallEntryValues(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := shellinstaller.UserInstallFromEntry(values); err != nil {
		t.Fatal(err)
	}
}

func TestShellInstallHelpHasNoEffects(t *testing.T) {
	var output bytes.Buffer
	if err := RunShell([]string{"install", "--help"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "--inspect") || !strings.Contains(output.String(), "existing delegated") {
		t.Fatalf("missing consent or prerequisites: %q", output.String())
	}
}

func TestShellInstallRefusalHelpIsRunnable(t *testing.T) {
	_, _, positionalErr := parseShellInstall([]string{"extra"}, io.Discard)
	if positionalErr == nil || !strings.Contains(positionalErr.Error(), "gentle-ai shell install --help") {
		t.Fatalf("refusal lacks the help continuation: %v", positionalErr)
	}
	var output bytes.Buffer
	if err := RunShell([]string{"install", "--help"}, &output); err != nil || !strings.Contains(output.String(), "--inspect") || !strings.Contains(output.String(), "--confirm") {
		t.Fatalf("named help is not runnable or lacks physical consent flags: %v %q", err, output.String())
	}
}

func TestShellInstallConfirmationRefusalHasNoEffects(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("physical user selection is Linux amd64 only")
	}
	parent := t.TempDir()
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, "shell")
	if _, err := shellinstaller.InspectUserInstall(shellinstaller.UserInstallRequest{Destination: target, Mode: "separate"}); err != nil {
		t.Fatalf("invalid physical selection fixture: %v", err)
	}
	err := RunShell([]string{"install", "--target", target, "--confirm", "not-confirmed"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "gentle-ai shell install --help") || !strings.Contains(err.Error(), "--inspect") || !strings.Contains(err.Error(), "--confirm") {
		t.Fatalf("unconfirmed selection lacks the safe continuation: %v", err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("unconfirmed selection created or published a target: %v", err)
	}
}

func TestShellInstallTUIEditAndCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := shellInstallModel{cancel: cancel, req: shellinstaller.UserInstallRequest{Mode: "separate"}}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/owned/shell")})
	m = next.(shellInstallModel)
	if cmd != nil || m.req.Destination != "/owned/shell" || m.review || m.confirmed {
		t.Fatal("typing performed effects or did not edit selection")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(shellInstallModel)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = next.(shellInstallModel)
	if m.req.Mode != "shared" || !strings.Contains(m.View(), "/bin/pi") {
		t.Fatal("shared selection or explicit command review absent")
	}
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil || ctx.Err() == nil || next.(shellInstallModel).confirmed {
		t.Fatal("idle cancellation did not settle without installation")
	}
}

func TestShellInstallSeparateRepairHelpHasNoEffects(t *testing.T) {
	// The documented repair continuation remains usable without a user manager.
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	var output bytes.Buffer
	if err := RunShell([]string{"install", "--help"}, &output); err != nil || !strings.Contains(output.String(), "--mode separate") {
		t.Fatalf("repair help is not runnable: %v %q", err, output.String())
	}
}

func TestShellInstallTopLevelCommandsHaveNoEffects(t *testing.T) {
	// An absent bus prevents a regression from reaching the real user manager.
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	for _, verb := range []string{"help", "--help", "typo", "check", "internal-typo", "internal-internal-install"} {
		t.Run(verb, func(t *testing.T) {
			var output bytes.Buffer
			err := RunShell([]string{verb}, &output)
			if verb == "help" || verb == "--help" {
				if err != nil || output.String() != shellInstallHelp {
					t.Fatalf("help reached runtime instead of printing usage: %v %q", err, output.String())
				}
			} else if err == nil || !strings.Contains(err.Error(), "unknown shell command") || !strings.Contains(err.Error(), "gentle-ai shell --help") {
				t.Fatalf("unknown verb reached runtime instead of naming help: %v", err)
			}
		})
	}
}

func TestShellInstallInternalCommandsRetainKernelChecks(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("GENTLE_SHELL_UNIT", "")
	t.Setenv("GENTLE_SHELL_UNIT_RECEIPT", "")
	for _, verb := range []string{"internal-check", "internal-install", "internal-launch", "internal-recover"} {
		t.Run(verb, func(t *testing.T) {
			// No operands: cannot install, launch or recover, even on a qualified host.
			want := shellinstaller.RunUserEntry(context.Background(), "", []string{verb}, nil, io.Discard, io.Discard)
			got := RunShell([]string{verb}, io.Discard)
			if (got == nil) != (want == nil) || (got != nil && got.Error() != want.Error()) {
				t.Fatalf("internal route bypassed or blocked kernel checks: got %v, want %v", got, want)
			}
		})
	}
}

func TestShellInstallTUIFieldNavigation(t *testing.T) {
	for _, mode := range []string{"separate", "shared"} {
		t.Run(mode, func(t *testing.T) {
			m := shellInstallModel{req: shellinstaller.UserInstallRequest{Mode: mode}}
			fields := 2
			if mode == "shared" {
				fields = 4
			}
			for step := 1; step <= fields*2; step++ {
				next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyTab})
				m = next.(shellInstallModel)
				if cmd != nil || m.field != step%fields {
					t.Fatalf("Tab step %d selected field %d; want %d", step, m.field, step%fields)
				}
			}
		})
	}
}

func TestShellInstallTUIPathsWithSpaces(t *testing.T) {
	for _, field := range []int{0, 2, 3} {
		m := shellInstallModel{field: field, req: shellinstaller.UserInstallRequest{Mode: "shared"}}
		for _, text := range []string{"/owned/my", " ", "shell"} {
			next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)})
			m = next.(shellInstallModel)
			if cmd != nil {
				t.Fatal("editing performed effects")
			}
		}
		paths := []string{m.req.Destination, "", m.req.SharedPrefix, m.req.SharedAgent}
		if paths[field] != "/owned/my shell" || m.req.Mode != "shared" {
			t.Fatalf("space was consumed instead of editing field %d: %+v", field, m.req)
		}
	}
}

func TestShellInstallTUIConfirmationReleasesTerminal(t *testing.T) {
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := shellInstallModel{cancel: cancel, review: true,
		req: shellinstaller.UserInstallRequest{Destination: "/owned/shell", Mode: "separate", Confirmation: "reviewed"}}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if cmd == nil {
		t.Fatal("confirmation did not release the TUI")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("confirmation ran the installer before releasing the TUI")
	}
	if !next.(shellInstallModel).confirmed {
		t.Fatal("confirmation was not retained for the post-TUI handoff")
	}
	if got := next.(shellInstallModel).req; got != m.req {
		t.Fatalf("confirmed selection changed: %+v", got)
	}
}

func TestShellInstallTUIReviewCancelDoesNotInstall(t *testing.T) {
	for _, key := range []tea.KeyType{tea.KeyCtrlC, tea.KeyEsc} {
		ctx, cancel := context.WithCancel(context.Background())
		m := shellInstallModel{cancel: cancel, review: true}
		next, cmd := m.Update(tea.KeyMsg{Type: key})
		cancel()
		if cmd == nil || ctx.Err() == nil || next.(shellInstallModel).confirmed {
			t.Fatal("review cancellation authorized an installation or failed to quit")
		}
	}
}

func TestShellInstallTUINonConfirmationReturnsToEdit(t *testing.T) {
	m := shellInstallModel{review: true}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m = next.(shellInstallModel)
	if cmd != nil || m.review || m.confirmed {
		t.Fatal("declining review did not return to editing without effects")
	}
}
