//go:build linux

package directrun

import (
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func TestRetainedScriptInterpreterRejectsUnsafeHeaders(t *testing.T) {
	cases := []string{
		"", "#!/usr/bin/env node --inspect\nconsole.log(1)\n", "#!/usr/bin/env unknown\n", "#!/bin/sh\necho unsafe\n",
		"#!/usr/bin/env node\r\nconsole.log(1)\n",
	}
	for _, content := range cases {
		t.Run(content, func(t *testing.T) {
			file, err := os.CreateTemp(t.TempDir(), "script")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := file.WriteString(content); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			fd, err := unix.Open(file.Name(), unix.O_RDONLY|unix.O_CLOEXEC, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer unix.Close(fd)
			if _, err := retainedScriptInterpreter("npm", fd); err == nil {
				t.Fatal("accepted unsafe script")
			}
		})
	}
}

func TestRetainedOutputStopsAtBound(t *testing.T) {
	output := &retainedOutput{}
	if _, err := output.Write(make([]byte, retainedOutputLimit+1)); err != nil {
		t.Fatal(err)
	}
	if !output.overflow || len(output.bytes()) != retainedOutputLimit {
		t.Fatalf("overflow=%v size=%d", output.overflow, len(output.bytes()))
	}
}

func TestRetainedHelperRejectsInvalidDispatch(t *testing.T) {
	if !retainedProcFSAvailable() {
		t.Skip("procfd unavailable")
	}
	if _, err := retainedProgramFor("pnpm"); !errors.Is(err, ErrCommandTargetUnsupported) && err == nil {
		t.Fatal("pnpm target accepted")
	}
}
