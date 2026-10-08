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
	"github.com/gentleman-programming/gentle-ai/v4/internal/shellinstaller"
	"github.com/gentleman-programming/gentle-ai/v4/internal/system"
	"golang.org/x/sys/unix"
)

func TestMainShellInstallPhysicalPTY(t *testing.T) {
	for _, scenario := range []string{"confirm", "cancel configuration", "cancel review"} {
		t.Run(scenario, func(t *testing.T) {
			master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
			if err != nil {
				t.Fatalf("existing Guest PTY unavailable: %v", err)
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
			before, err := term.GetState(slave.Fd())
			if err != nil {
				t.Fatal(err)
			}
			home := t.TempDir()
			if err := os.Chmod(home, 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
			t.Setenv("GENTLE_AI_NO_ANIMATION", "1")
			target := filepath.Join(home, "qshell")
			oldIn, oldOut := os.Stdin, os.Stdout
			oldDetect, oldEnsure, oldTUI, oldShell := detectSystem, ensureCurrentOSSupported, runTUI, runShellEntry
			os.Stdin, os.Stdout = slave, slave
			t.Cleanup(func() {
				os.Stdin, os.Stdout = oldIn, oldOut
				detectSystem, ensureCurrentOSSupported, runTUI, runShellEntry = oldDetect, oldEnsure, oldTUI, oldShell
			})
			ensureCurrentOSSupported = func() error { return nil }
			detectSystem = func(context.Context) (system.DetectionResult, error) {
				return system.DetectionResult{System: system.SystemInfo{Supported: true}}, nil
			}
			programCalls, backendCalls, returned := 0, 0, false
			var program *tea.Program
			runTUI = func(m tea.Model, opts ...tea.ProgramOption) (tea.Model, error) {
				programCalls++
				program = tea.NewProgram(m, append(opts, tea.WithInput(slave), tea.WithOutput(slave))...)
				defer func() { returned = true }()
				return program.Run()
			}
			runShellEntry = func(args []string, _ io.Writer) error {
				backendCalls++
				after, err := term.GetState(slave.Fd())
				if !returned || err != nil || !reflect.DeepEqual(before, after) {
					return fmt.Errorf("handoff before physical terminal restoration: %v", err)
				}
				p := shellinstaller.DefaultUserExperience()
				want, err := p.Encode()
				fresh, inspectErr := shellinstaller.InspectUserInstall(shellinstaller.UserInstallRequest{Destination: target, Mode: "separate", Experience: &p})
				if err != nil || inspectErr != nil || len(args) != 13 || args[2] != target || args[11] != "--experience" || args[12] != want || args[10] != fresh {
					return fmt.Errorf("physical selection/profile lost: %q", args)
				}
				return nil // inert spy: never invoke the installer
			}
			var output bytes.Buffer
			var outputMu sync.Mutex
			go func() {
				buffer := make([]byte, 4096)
				for {
					n, err := master.Read(buffer)
					outputMu.Lock()
					if output.Len()+n < 256*1024 {
						_, _ = output.Write(buffer[:n])
					}
					outputMu.Unlock()
					if err != nil {
						return
					}
				}
			}()
			waitFor := func(text string) {
				t.Helper()
				deadline := time.Now().Add(8 * time.Second)
				for time.Now().Before(deadline) {
					outputMu.Lock()
					view := ansi.Strip(output.String())
					outputMu.Unlock()
					if strings.Contains(view, text) {
						return
					}
					time.Sleep(10 * time.Millisecond)
				}
				t.Fatalf("real PTY never rendered %q", text)
			}
			send := func(text string) {
				t.Helper()
				if _, err := master.Write([]byte(text)); err != nil {
					t.Fatal(err)
				}
			}
			resize := func(cols, rows uint16) {
				t.Helper()
				if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Col: cols, Row: rows}); err != nil {
					t.Fatal(err)
				}
				width, height, err := term.GetSize(slave.Fd())
				if err != nil || width != int(cols) || height != int(rows) {
					t.Fatal("physical PTY winsize differs", err)
				}
				if err := unix.Kill(os.Getpid(), unix.SIGWINCH); err != nil {
					t.Fatal(err)
				}
			}
			resize(120, 50)
			done := make(chan error, 1)
			go func() { done <- RunArgs(nil, slave) }()
			t.Cleanup(func() {
				if program != nil {
					program.Kill()
				}
			})
			waitFor("Install Gentle-Shell, our own agent")
			send("\r")
			waitFor("Configure Gentle-Shell experience")
			if scenario != "cancel configuration" {
				send(strings.Repeat("\x1b[B", 4) + "\r")
				waitFor("Choose installation destination")
				send(target + "\r")
				waitFor("Ready to install Gentle-Shell")
				resize(80, 24)
				waitFor("pgup/pgdn")
				resize(120, 50)
			}
			if scenario == "confirm" {
				send("\r")
			} else {
				outputMu.Lock()
				output.Reset()
				outputMu.Unlock()
				send("\x1b")
				waitFor("Install Gentle-Shell, our own agent")
				send("q")
			}
			select {
			case err := <-done:
				if err != nil || programCalls != 1 || (backendCalls == 1) != (scenario == "confirm") {
					t.Fatalf("PTY handoff: programs=%d backend=%d error=%v", programCalls, backendCalls, err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("parent program did not restore and return")
			}
			after, err := term.GetState(slave.Fd())
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("physical termios not restored", err)
			}
			if _, err := os.Lstat(target); !os.IsNotExist(err) {
				t.Fatal("PTY test installed a target")
			}
		})
	}
}
