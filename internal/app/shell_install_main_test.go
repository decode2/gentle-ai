package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gentleman-programming/gentle-ai/v4/internal/shellinstaller"
	"github.com/gentleman-programming/gentle-ai/v4/internal/system"
	"github.com/gentleman-programming/gentle-ai/v4/internal/tui"
)

func TestMainShellInstallTerminalHandoff(t *testing.T) {
	for _, tt := range []struct {
		name       string
		args       []string
		confirm    bool
		programErr bool
	}{
		{"main confirms", nil, true, false},
		{"alias confirms", []string{"shell", "install"}, true, false},
		{"main cancels", nil, false, false},
		{"alias cancels", []string{"shell", "install"}, false, false},
		{"parent failure refuses handoff", nil, true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assumeInteractiveTTY(t)
			home := t.TempDir()
			if err := os.Chmod(home, 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
			oldDetect, oldEnsure, oldTUI, oldShell := detectSystem, ensureCurrentOSSupported, runTUI, runShellEntry
			t.Cleanup(func() {
				detectSystem, ensureCurrentOSSupported, runTUI, runShellEntry = oldDetect, oldEnsure, oldTUI, oldShell
			})
			ensureCurrentOSSupported = func() error { return nil }
			detectSystem = func(context.Context) (system.DetectionResult, error) {
				return system.DetectionResult{System: system.SystemInfo{Supported: true}}, nil
			}
			p := shellinstaller.UserExperience{Persona: "neutral", BackgroundSubagents: "off"}
			want := shellinstaller.UserInstallRequest{Destination: filepath.Join(home, "shell"), Mode: "separate", Experience: &p}
			stopped := false
			programCalls, backendCalls := 0, 0
			programFailure := errors.New("parent program failed")
			backendResult := errors.New("inert backend spy")
			runTUI = func(model tea.Model, _ ...tea.ProgramOption) (tea.Model, error) {
				programCalls++
				defer func() { stopped = true }()
				for _, msg := range []tea.Msg{
					tea.WindowSizeMsg{Width: 120, Height: 50},
					tea.KeyMsg{Type: tea.KeyEnter},
					tea.KeyMsg{Type: tea.KeyDown}, tea.KeyMsg{Type: tea.KeyEnter},
					tea.KeyMsg{Type: tea.KeyDown}, tea.KeyMsg{Type: tea.KeyDown},
					tea.KeyMsg{Type: tea.KeyEnter}, tea.KeyMsg{Type: tea.KeyDown},
					tea.KeyMsg{Type: tea.KeyEnter},
					tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(want.Destination)},
					tea.KeyMsg{Type: tea.KeyEnter},
				} {
					next, cmd := model.Update(msg)
					model = next
					if cmd != nil || backendCalls != 0 {
						t.Fatal("parent UI executed effects before confirmation")
					}
				}
				key := tea.KeyMsg{Type: tea.KeyEsc}
				if tt.confirm {
					token, err := shellinstaller.InspectUserInstall(want)
					if err != nil {
						t.Fatal(err)
					}
					want.Confirmation = token
					key = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")}
				}
				next, cmd := model.Update(key)
				if tt.confirm {
					if cmd == nil {
						t.Fatal("confirmed parent did not request terminal release")
					}
					if _, ok := cmd().(tea.QuitMsg); !ok {
						t.Fatal("confirmation scheduled something other than parent quit")
					}
				} else if cmd != nil || next.(tui.Model).Screen != tui.ScreenWelcome {
					t.Fatal("cancel quit the parent instead of returning to the menu")
				}
				if tt.programErr {
					return next, programFailure
				}
				return next, nil
			}
			runShellEntry = func(args []string, _ io.Writer) error {
				backendCalls++
				if !stopped {
					t.Fatal("backend invoked before parent Run returned")
				}
				expected := []string{"install", "--target", want.Destination, "--mode", want.Mode,
					"--prefix", "", "--agent", "", "--confirm", want.Confirmation}
				raw, err := p.Encode()
				if err != nil {
					t.Fatal(err)
				}
				expected = append(expected, "--experience", raw)
				if !reflect.DeepEqual(args, expected) {
					t.Fatalf("handoff args = %q, want %q", args, expected)
				}
				return backendResult
			}
			var output bytes.Buffer
			err := RunArgs(tt.args, &output)
			calls := 0
			var expectedErr error
			if tt.confirm && !tt.programErr {
				calls, expectedErr = 1, backendResult
			} else if tt.programErr {
				expectedErr = programFailure
			}
			if programCalls != 1 || backendCalls != calls || err != expectedErr {
				t.Fatalf("program=%d backend=%d err=%v, want 1/%d/%v", programCalls, backendCalls, err, calls, expectedErr)
			}
			if _, err := os.Lstat(want.Destination); !os.IsNotExist(err) {
				t.Fatalf("UI or inert spy installed a target: %v", err)
			}
		})
	}
}
