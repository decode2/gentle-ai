//go:build linux

package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/gentleman-programming/gentle-ai/v4/internal/cli"
	"github.com/gentleman-programming/gentle-ai/v4/internal/shellinstaller"
	"github.com/gentleman-programming/gentle-ai/v4/internal/system"
	"golang.org/x/sys/unix"
)

// Isolate terminal interaction from unrelated startup network commands.
// Update/View and the application's physical selection remain production code.
type shellPTYModel struct{ tea.Model }

func (shellPTYModel) Init() tea.Cmd { return nil }

func TestShellInstallParentPhysicalPTY(t *testing.T) {
	for _, scenario := range []string{"confirm", "cancel-target", "cancel-review"} {
		t.Run(scenario, func(t *testing.T) {
			master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = master.Close() })
			if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
				t.Fatal(err)
			}
			number, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
			if err != nil {
				t.Fatal(err)
			}
			slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|unix.O_NOCTTY, 0)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = slave.Close() })
			if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Col: 100, Row: 35}); err != nil {
				t.Fatal(err)
			}
			before, err := term.GetState(slave.Fd())
			if err != nil {
				t.Fatal(err)
			}
			home := t.TempDir()
			if err := os.Chmod(home, 0700); err != nil {
				t.Fatal(err)
			}
			setupMockHome(t, home)
			t.Setenv("GENTLE_AI_NO_ANIMATION", "1")
			target := filepath.Join(home, "qtarget")
			oldIn, oldOut := os.Stdin, os.Stdout
			oldOS, oldDetect, oldTUI, oldRun := ensureCurrentOSSupported, detectSystem, runTUI, runShellInstallSelection
			os.Stdin, os.Stdout = slave, slave // Real TTY boundary, not an isatty override.
			t.Cleanup(func() {
				os.Stdin, os.Stdout = oldIn, oldOut
				ensureCurrentOSSupported, detectSystem, runTUI, runShellInstallSelection = oldOS, oldDetect, oldTUI, oldRun
			})
			ensureCurrentOSSupported = func() error { return nil }
			detectSystem = func(context.Context) (system.DetectionResult, error) {
				return system.DetectionResult{System: system.SystemInfo{Supported: true}}, nil
			}
			programs, workers, returned := 0, 0, false
			created := make(chan *tea.Program, 1)
			runTUI = func(m tea.Model, opts ...tea.ProgramOption) (tea.Model, error) {
				programs++
				p := tea.NewProgram(shellPTYModel{m}, append(opts, tea.WithInput(slave), tea.WithOutput(slave))...)
				created <- p
				defer func() { returned = true }()
				return p.Run()
			}
			runShellInstallSelection = func(selection cli.ShellInstallSelection, stdout io.Writer) error {
				workers++
				after, err := term.GetState(slave.Fd())
				if err != nil || !returned || !reflect.DeepEqual(before, after) || stdout != slave {
					return fmt.Errorf("worker hook before terminal restoration: %v", err)
				}
				fresh, err := shellinstaller.InspectUserInstall(selection.Request)
				if err != nil || !selection.Confirmed || selection.Request.Destination != target || selection.Request.Confirmation != fresh {
					return fmt.Errorf("public interaction lost physical selection: %+v, %v", selection, err)
				}
				return nil // Inert boundary spy: no acquisition or installation authority.
			}
			var output bytes.Buffer
			var mu sync.Mutex
			go func() {
				buffer := make([]byte, 4096)
				for {
					n, err := master.Read(buffer)
					mu.Lock()
					if output.Len()+n < 256*1024 {
						_, _ = output.Write(buffer[:n])
					}
					mu.Unlock()
					if err != nil {
						return
					}
				}
			}()
			waitFor := func(text string) {
				t.Helper()
				deadline := time.Now().Add(8 * time.Second)
				for time.Now().Before(deadline) {
					mu.Lock()
					view := ansi.Strip(output.String())
					mu.Unlock()
					if strings.Contains(view, text) {
						return
					}
					time.Sleep(10 * time.Millisecond)
				}
				mu.Lock()
				view := ansi.Strip(output.String())
				mu.Unlock()
				t.Fatalf("PTY did not render %q; output=%q", text, view)
			}
			send := func(text string) {
				t.Helper()
				if _, err := master.Write([]byte(text)); err != nil {
					t.Fatal(err)
				}
			}
			done := make(chan error, 1)
			go func() { done <- RunArgs(nil, slave) }()
			t.Cleanup(func() {
				select {
				case p := <-created:
					p.Kill()
				default:
				}
			})
			waitFor("Install Gentle Shell")
			send(strings.Repeat("\x1b[B", 11) + "\r")
			waitFor("Target:")
			if scenario != "cancel-target" {
				send(target + "\r")
				waitFor("Confirm this physical selection")
			}
			if scenario == "confirm" {
				send("y")
			} else {
				mu.Lock()
				output.Reset()
				mu.Unlock()
				send("\x1b")
				waitFor("Install Gentle Shell")
				send("q")
			}
			select {
			case err := <-done:
				if err != nil || programs != 1 || (workers == 1) != (scenario == "confirm") {
					t.Fatalf("programs=%d workers=%d error=%v", programs, workers, err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("terminal owner did not finish")
			}
			after, err := term.GetState(slave.Fd())
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("termios not restored", err)
			}
			if _, err := os.Lstat(target); !os.IsNotExist(err) {
				t.Fatal("PTY fixture installed a target", err)
			}
		})
	}
}
