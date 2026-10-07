package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gentleman-programming/gentle-ai/v4/internal/shellinstaller"
)

func TestEmbeddedShellInstallConfiguration(t *testing.T) {
	for _, tt := range []struct {
		name    string
		keys    []tea.KeyMsg
		want    shellinstaller.UserInstallRequest
		wantErr bool
	}{
		{"initial separate target", nil, shellinstaller.UserInstallRequest{Mode: "separate"}, false},
		{"typing and backspace", []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune("shellx")}, {Type: tea.KeyBackspace}}, shellinstaller.UserInstallRequest{Destination: "shell", Mode: "separate"}, false},
		{"shared mode editing", []tea.KeyMsg{{Type: tea.KeyTab}, {Type: tea.KeyRight}}, shellinstaller.UserInstallRequest{Mode: "shared"}, false},
		{"confirmation before review", []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune("y")}}, shellinstaller.UserInstallRequest{Destination: "y", Mode: "separate"}, false},
		{"invalid review cannot confirm", []tea.KeyMsg{{Type: tea.KeyEnter}, {Type: tea.KeyRunes, Runes: []rune("y")}}, shellinstaller.UserInstallRequest{Destination: "y", Mode: "separate"}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			m := NewShellInstallModel(func() { calls++ })
			if m == nil || m.Init() != nil {
				t.Fatal("constructor scheduled work instead of returning an idle model")
			}
			for _, key := range tt.keys {
				var cmd tea.Cmd
				m, cmd = m.Update(key)
				if cmd != nil {
					t.Fatal("editing scheduled effects or ended the installer")
				}
			}
			got, confirmed, finished, err := ShellInstallOutcome(m)
			if got != tt.want || confirmed || finished || (err != nil) != tt.wantErr || calls != 0 {
				t.Fatalf("configuration = %+v confirmed=%v finished=%v err=%v cancel calls=%d", got, confirmed, finished, err, calls)
			}
		})
	}
}

func TestEmbeddedShellInstallReviewAndCompletion(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("physical user selection is Linux amd64 only")
	}
	for _, tt := range []struct {
		name      string
		review    bool
		key       tea.KeyMsg
		nilCancel bool
		confirmed bool
		finished  bool
		calls     int
	}{
		{"review confirms", true, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")}, false, true, true, 0},
		{"review declines to edit", true, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}, false, false, false, 0},
		{"idle escape", false, tea.KeyMsg{Type: tea.KeyEsc}, false, false, true, 1},
		{"idle ctrl c", false, tea.KeyMsg{Type: tea.KeyCtrlC}, false, false, true, 1},
		{"idle escape nil cancel", false, tea.KeyMsg{Type: tea.KeyEsc}, true, false, true, 0},
		{"idle ctrl c nil cancel", false, tea.KeyMsg{Type: tea.KeyCtrlC}, true, false, true, 0},
		{"review escape", true, tea.KeyMsg{Type: tea.KeyEsc}, false, false, true, 1},
		{"review ctrl c", true, tea.KeyMsg{Type: tea.KeyCtrlC}, false, false, true, 1},
		{"review escape nil cancel", true, tea.KeyMsg{Type: tea.KeyEsc}, true, false, true, 0},
		{"review ctrl c nil cancel", true, tea.KeyMsg{Type: tea.KeyCtrlC}, true, false, true, 0},
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
			want := shellinstaller.UserInstallRequest{Destination: filepath.Join(parent, "shell"), Mode: "separate"}
			t.Cleanup(func() {
				if data, err := os.ReadFile(sentinel); err != nil || string(data) != "inert owned fixture" {
					t.Errorf("owned sentinel changed: %q, %v", data, err)
				}
				if _, err := os.Lstat(want.Destination); !os.IsNotExist(err) {
					t.Errorf("selection published or installed a target: %v", err)
				}
			})
			calls := 0
			var cancel func()
			if !tt.nilCancel {
				cancel = func() { calls++ }
			}
			m := NewShellInstallModel(cancel)
			if m == nil || m.Init() != nil {
				t.Fatal("constructor scheduled effects")
			}
			next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(want.Destination)})
			m = next
			got, confirmed, finished, err := ShellInstallOutcome(m)
			if cmd != nil || got != want || confirmed || finished || err != nil || calls != 0 {
				t.Fatal("target editing did not remain an incomplete separate selection")
			}
			if tt.review {
				token, err := shellinstaller.InspectUserInstall(want)
				if err != nil {
					t.Fatalf("invalid owned physical fixture: %v", err)
				}
				want.Confirmation = token
				m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
				got, confirmed, finished, err = ShellInstallOutcome(m)
				if cmd != nil || got != want || confirmed || finished || err != nil || calls != 0 || !strings.Contains(m.View(), token) {
					t.Fatal("review did not disclose the inspected selection without granting authority")
				}
			}
			m, cmd = m.Update(tt.key)
			if tt.finished {
				if cmd == nil {
					t.Fatal("completed selection did not request terminal release")
				}
				if _, ok := cmd().(tea.QuitMsg); !ok {
					t.Fatal("completed selection returned a non-quit command")
				}
			} else if cmd != nil {
				t.Fatal("returning to editing requested effects or terminal release")
			}
			got, confirmed, finished, err = ShellInstallOutcome(m)
			if got != want || confirmed != tt.confirmed || finished != tt.finished || err != nil || calls != tt.calls {
				t.Fatalf("outcome = %+v confirmed=%v finished=%v err=%v cancel calls=%d", got, confirmed, finished, err, calls)
			}
			if confirmed {
				selection := got
				selection.Confirmation = ""
				fresh, err := shellinstaller.InspectUserInstall(selection)
				if err != nil || got.Confirmation != fresh {
					t.Fatalf("confirmation does not match the fresh physical selection: %v", err)
				}
				foreign := struct{ tea.Model }{m}
				req, granted, done, err := ShellInstallOutcome(foreign)
				if req != (shellinstaller.UserInstallRequest{}) || granted || done || err != nil {
					t.Fatal("a foreign model wrapping a confirmed selection gained authority")
				}
			}
		})
	}
}

func TestEmbeddedShellInstallUnrelatedOutcome(t *testing.T) {
	for _, tt := range []struct {
		name  string
		model tea.Model
	}{
		{"nil model", nil},
		{"unrelated model", struct{ tea.Model }{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req, confirmed, finished, err := ShellInstallOutcome(tt.model)
			if req != (shellinstaller.UserInstallRequest{}) || confirmed || finished || err != nil {
				t.Fatalf("foreign outcome = %+v confirmed=%v finished=%v err=%v", req, confirmed, finished, err)
			}
		})
	}
}
