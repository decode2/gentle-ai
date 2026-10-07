package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestShellInstallInteractiveRequiresMainTUI(t *testing.T) {
	var output bytes.Buffer
	err := RunShell([]string{"install"}, &output)
	if err == nil || !strings.Contains(err.Error(), "main Gentle AI TUI") || output.Len() != 0 {
		t.Fatalf("headless route unexpectedly opened an installer: output=%q err=%v", output.String(), err)
	}
}
