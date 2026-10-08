package cli

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestShellInstallEmbeddedOutcome(t *testing.T) {
	for _, channel := range []string{"stable", "main"} {
		t.Run("confirmed "+channel, func(t *testing.T) {
			model := NewShellInstallModel(nil).(shellInstallModel)
			model.req.Destination = `C:\owned\shell`
			model.req.Channel = channel
			model.req.Confirmation = "fixture-inspection-token"
			model.review = true // State-machine fixture, not physical consent evidence.
			final, command := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
			if command == nil {
				t.Fatal("confirmed embedding must return control to its parent")
			}
			if _, ok := command().(tea.QuitMsg); !ok {
				t.Fatal("embedded confirmation returned a backend command instead of Quit")
			}
			req, confirmed, finished, err := ShellInstallOutcome(final)
			if !confirmed || !finished || err != nil || req != model.req {
				t.Fatalf("unexpected outcome: %+v confirmed=%v finished=%v err=%v", req, confirmed, finished, err)
			}
			// A late completion cannot replace the confirmed selection or quit again.
			late, command := final.Update(shellInstallDone{err: errors.New("late")})
			if command != nil {
				t.Fatal("finished embedded model emitted another command")
			}
			_, confirmed, finished, err = ShellInstallOutcome(late)
			if !confirmed || !finished || err != nil {
				t.Fatalf("late event changed terminal outcome: %v %v %v", confirmed, finished, err)
			}
		})
	}
}

func TestShellInstallEmbeddedCancel(t *testing.T) {
	for _, key := range []tea.KeyType{tea.KeyEsc, tea.KeyCtrlC} {
		t.Run(tea.KeyMsg{Type: key}.String(), func(t *testing.T) {
			canceled := 0
			model := NewShellInstallModel(func() { canceled++ })
			final, command := model.Update(tea.KeyMsg{Type: key})
			if command == nil {
				t.Fatal("cancellation did not return control")
			}
			if _, ok := command().(tea.QuitMsg); !ok {
				t.Fatal("cancellation returned a non-Quit command")
			}
			_, confirmed, finished, err := ShellInstallOutcome(final)
			if canceled != 1 || confirmed || !finished || err != nil {
				t.Fatalf("invalid canceled outcome: callbacks=%d confirmed=%v finished=%v err=%v", canceled, confirmed, finished, err)
			}
			_, command = final.Update(tea.KeyMsg{Type: key})
			if canceled != 1 || command != nil {
				t.Fatal("cancellation callback repeated after completion")
			}
		})
	}
}

func TestShellInstallEmbeddedRejectsForeignModel(t *testing.T) {
	for _, model := range []tea.Model{nil, shellInstallModel{}} {
		_, confirmed, finished, err := ShellInstallOutcome(model)
		if confirmed || finished || err == nil {
			t.Fatalf("foreign model produced a usable outcome: %v %v %v", confirmed, finished, err)
		}
	}
}

func TestShellInstallEmbeddedIgnoresForeignCompletion(t *testing.T) {
	model := NewShellInstallModel(nil)
	final, command := model.Update(shellInstallDone{err: errors.New("not this model's worker")})
	if command != nil {
		t.Fatal("selection-only model accepted a backend completion")
	}
	_, confirmed, finished, err := ShellInstallOutcome(final)
	if confirmed || finished || err != nil {
		t.Fatalf("foreign completion changed selection state: %v %v %v", confirmed, finished, err)
	}
}

func TestShellInstallEmbeddedUnfinishedAndNilCancel(t *testing.T) {
	model := NewShellInstallModel(nil)
	if model.Init() != nil {
		t.Fatal("construction started an operation")
	}
	req, confirmed, finished, err := ShellInstallOutcome(model)
	if req.Mode != "separate" || req.Channel != "stable" || confirmed || finished || err != nil {
		t.Fatalf("invalid initial outcome: %+v %v %v %v", req, confirmed, finished, err)
	}
	final, command := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if command == nil {
		t.Fatal("nil cancellation callback prevented exit")
	}
	_, confirmed, finished, err = ShellInstallOutcome(final)
	if confirmed || !finished || err != nil {
		t.Fatalf("invalid nil-callback outcome: %v %v %v", confirmed, finished, err)
	}
}
