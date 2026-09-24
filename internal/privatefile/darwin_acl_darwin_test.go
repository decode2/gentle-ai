//go:build darwin

package privatefile

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// probeDarwinPrivateACL is the future descriptor-only ACL gate: nil means
// verified no ACL; any ACL granting other principals or inspection failure
// must return an error. It must not reopen a path or write a report.
func TestDarwinPrivateACLNoACLPasses(t *testing.T) {
	if testing.Short() {
		t.Skip("native ACL fixture uses chmod")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "private")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	runDarwinACLFixtureCommand(t, "chmod", "-N", path)

	// The original path no longer names this descriptor. Path reopening is
	// not a valid substitute for inspecting the open file.
	if err := os.Rename(path, filepath.Join(dir, "renamed")); err != nil {
		t.Fatal(err)
	}
	if err := probeDarwinPrivateACL(int(file.Fd())); err != nil {
		t.Fatalf("no ACL on open descriptor: %v", err)
	}
}

func TestDarwinPrivateACLInheritedPermissiveFails(t *testing.T) {
	if testing.Short() {
		t.Skip("native ACL fixture uses chmod and ls")
	}
	dir := filepath.Join(t.TempDir(), "inheriting")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	runDarwinACLFixtureCommand(t, "chmod", "+a", "everyone allow read,file_inherit", dir)
	path := filepath.Join(dir, "child")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	// Ensure the fixture really inherited a permissive ACE, rather than
	// silently testing a mode-only file.
	listing := runDarwinACLFixtureCommand(t, "ls", "-le", path)
	if !strings.Contains(listing, "everyone") || !strings.Contains(listing, "inherited") {
		t.Fatalf("expected inherited everyone ACL on test fixture: %s", listing)
	}
	if err := probeDarwinPrivateACL(int(file.Fd())); err == nil {
		t.Fatal("inherited permissive ACL accepted despite owner-only mode")
	}
}

func TestDarwinPrivateACLInspectionFailureFailsClosed(t *testing.T) {
	file, err := os.OpenFile(filepath.Join(t.TempDir(), "private"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	closedFD := int(file.Fd())
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name string
		fd   int
	}{
		{name: "invalid descriptor", fd: -1},
		{name: "closed descriptor", fd: closedFD},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := probeDarwinPrivateACL(tt.fd); err == nil {
				t.Fatal("ACL inspection failure accepted")
			}
		})
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	if err := probeDarwinPrivateACL(int(reader.Fd())); err == nil {
		t.Fatal("ACL unavailable on pipe accepted")
	}
}

func runDarwinACLFixtureCommand(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("ACL fixture %s failed: %v: %s", name, err, out)
	}
	return string(out)
}
