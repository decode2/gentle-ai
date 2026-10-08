package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/gentleman-programming/gentle-ai/v4/internal/cli"
	"github.com/gentleman-programming/gentle-ai/v4/internal/shellinstaller"
	"github.com/gentleman-programming/gentle-ai/v4/internal/system"
)

func shellInstallIdleUpdate(t *testing.T, m *Model, msg tea.Msg) {
	t.Helper()
	next, cmd := m.Update(msg)
	*m = next.(Model)
	if cmd != nil {
		t.Fatalf("%T scheduled effects or requested parent termination", msg)
	}
}

func TestMainShellInstallMenu(t *testing.T) {
	for _, tt := range []struct {
		name   string
		optIn  bool
		cursor int
		want   Screen
	}{
		{"legacy installation", false, 0, ScreenDetection},
		{"primary shell installer", true, 0, ScreenShellInstall},
		{"ordinary installation shifted", true, 1, ScreenDetection},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := NewModel(system.DetectionResult{}, "test")
			if tt.optIn {
				m = m.WithShellInstaller()
			}
			shellInstallIdleUpdate(t, &m, tea.WindowSizeMsg{Width: 120, Height: 50})
			view := ansi.Strip(m.View())
			title := strings.Index(view, "Install Gentle-Shell, our own agent")
			subtitle := strings.Index(view, "The complete Pi experience.")
			menu := strings.Index(view, "Menu")
			if tt.optIn && (title < 0 || subtitle <= title || menu <= subtitle) {
				t.Fatalf("original hero is missing or follows Menu:\n%s", view)
			}
			if !tt.optIn && title >= 0 {
				t.Fatal("legacy menu unexpectedly opted into the shell installer")
			}
			if menu < 0 || strings.Index(view, "Start installation") <= menu || m.Cursor != 0 {
				t.Fatal("ordinary menu or initial primary cursor changed")
			}
			for step := 0; step < tt.cursor; step++ {
				shellInstallIdleUpdate(t, &m, tea.KeyMsg{Type: tea.KeyDown})
			}
			shellInstallIdleUpdate(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
			if _, confirmed := m.ShellInstallSelection(); confirmed || m.Screen != tt.want || m.OperationRunning || m.InstallFlowActive != (tt.want == ScreenDetection) {
				t.Fatalf("menu selected screen %v, want %v, without shell consent or effects", m.Screen, tt.want)
			}
		})
	}
}

func TestMainShellInstallChildTransitions(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("physical user selection is Linux amd64 only")
	}
	for _, tt := range []struct {
		name   string
		review bool
		key    tea.KeyMsg
	}{
		{"editing escape", false, tea.KeyMsg{Type: tea.KeyEsc}},
		{"editing ctrl c", false, tea.KeyMsg{Type: tea.KeyCtrlC}},
		{"review escape", true, tea.KeyMsg{Type: tea.KeyEsc}},
		{"review ctrl c", true, tea.KeyMsg{Type: tea.KeyCtrlC}},
		{"review declines", true, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}},
		{"review confirms", true, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			parent := t.TempDir()
			if err := os.Chmod(parent, 0700); err != nil {
				t.Fatal(err)
			}
			sentinel := filepath.Join(parent, "sentinel")
			if err := os.WriteFile(sentinel, []byte("inert owned fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			p := shellinstaller.DefaultUserExperience()
			want := shellinstaller.UserInstallRequest{Destination: filepath.Join(parent, "qshell"), Mode: "separate", Experience: &p}
			t.Cleanup(func() {
				if data, err := os.ReadFile(sentinel); err != nil || string(data) != "inert owned fixture" {
					t.Errorf("owned sentinel changed: %q, %v", data, err)
				}
				if _, err := os.Lstat(want.Destination); !os.IsNotExist(err) {
					t.Errorf("parent or child installed/published a target: %v", err)
				}
			})
			m := NewModel(system.DetectionResult{}, "test").WithShellInstaller()
			shellInstallIdleUpdate(t, &m, tea.WindowSizeMsg{Width: 120, Height: 50})
			shellInstallIdleUpdate(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
			shellExperienceContinue(t, &m)
			for _, msg := range []tea.Msg{
				tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(parent + string(os.PathSeparator))},
				tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")},
				tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("shell")},
				UpdateCheckResultMsg{}, AdvisoryMsg{},
			} {
				shellInstallIdleUpdate(t, &m, msg)
			}
			edited, ok := cli.ShellInstallPresentationOf(m.shellInstall.child)
			if _, confirmed := m.ShellInstallSelection(); confirmed || !ok || edited.Request.Destination != want.Destination || m.Screen != ScreenShellInstall || !strings.Contains(ansi.Strip(m.View()), "Target") {
				t.Fatal("q or background messages stole the child route or granted consent")
			}
			if tt.review {
				token, err := shellinstaller.InspectUserInstall(want)
				if err != nil {
					t.Fatalf("invalid owned physical fixture: %v", err)
				}
				want.Confirmation = token
				shellInstallIdleUpdate(t, &m, tea.KeyMsg{Type: tea.KeyEnter})
				for _, size := range []tea.WindowSizeMsg{{Width: 80, Height: 8}, {Width: 120, Height: 50}} {
					shellInstallIdleUpdate(t, &m, size)
					view := m.View()
					if m.Width != size.Width || m.Height != size.Height || len(strings.Split(view, "\n")) > size.Height {
						t.Fatal("review resize lost parent dimensions or exceeds physical height")
					}
					for _, line := range strings.Split(view, "\n") {
						if ansi.StringWidth(line) > size.Width {
							t.Fatal("child review exceeds physical terminal width")
						}
					}
				}
				if _, confirmed := m.ShellInstallSelection(); confirmed || !strings.Contains(shellReviewVisibleData(m.View()), token) {
					t.Fatal("review did not disclose real SHA without granting consent")
				}
			}
			for _, msg := range []tea.Msg{UpdateCheckResultMsg{}, AdvisoryMsg{}} {
				shellInstallIdleUpdate(t, &m, msg)
			}
			if tt.key.String() == "y" {
				next, cmd := m.Update(tt.key)
				m = next.(Model)
				got, confirmed := m.ShellInstallSelection()
				if !confirmed || !reflect.DeepEqual(got, want) || cmd == nil {
					t.Fatalf("confirmed handoff = %+v, confirmed=%v, quit command=%v", got, confirmed, cmd != nil)
				}
				if _, ok := cmd().(tea.QuitMsg); !ok {
					t.Fatal("parent confirmation did not request terminal restoration only")
				}
				selection := got
				selection.Confirmation = ""
				fresh, err := shellinstaller.InspectUserInstall(selection)
				if err != nil || fresh != got.Confirmation {
					t.Fatalf("handoff SHA differs from fresh physical inspection: %v", err)
				}
				for _, msg := range []tea.Msg{UpdateCheckResultMsg{}, AdvisoryMsg{}} {
					shellInstallIdleUpdate(t, &m, msg)
					if retained, ok := m.ShellInstallSelection(); !ok || retained != got {
						t.Fatal("late background message erased confirmed handoff")
					}
				}
				return
			}
			shellInstallIdleUpdate(t, &m, tt.key)
			if _, confirmed := m.ShellInstallSelection(); confirmed {
				t.Fatal("cancellation or declining review retained selection authority")
			}
			if tt.key.String() == "n" {
				shellInstallIdleUpdate(t, &m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
				edited, ok := cli.ShellInstallPresentationOf(m.shellInstall.child)
				if _, confirmed := m.ShellInstallSelection(); confirmed || !ok || edited.Request.Destination != want.Destination+"q" || m.Screen != ScreenShellInstall {
					t.Fatal("declining review did not return to child path editing")
				}
			} else if m.Screen != ScreenWelcome || m.Cursor != 0 {
				t.Fatal("child cancellation did not return to the primary welcome action")
			}
		})
	}
}
