//go:build darwin && cgo && privatefileaclexperiment

package privatefile

import (
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

// Experiment only: observes a fresh inode, not the report writer or private data.
func TestDarwinInheritedACLDescriptorClearExperiment(t *testing.T) {
	parent := t.TempDir()
	child := filepath.Join(parent, "empty-child")
	t.Log("nonowner probe: unavailable; this test does not prove confidentiality against a different user")
	fd, preparation := darwinACLPrepare(parent, child)
	t.Logf("native fixture preparation:\n%s", preparation)
	if fd < 0 {
		t.Fatal("native ACL fixture creation failed")
	}
	defer syscall.Close(fd)
	// ls is supplementary evidence, never a substitute for same-fd ACL reads.
	listing, err := exec.Command("ls", "-le", parent, child).CombinedOutput()
	t.Logf("fixture ls -le before (err=%v):\n%s", err, listing)
	if err != nil {
		t.Errorf("fixture ls -le before unavailable: %v", err)
	}
	observation, ok := darwinACLProbe(fd, child)
	t.Logf("native descriptor observation:\n%s", observation)
	listing, err = exec.Command("ls", "-le", parent, child).CombinedOutput()
	t.Logf("fixture ls -le after (err=%v):\n%s", err, listing)
	if err != nil {
		t.Errorf("fixture ls -le after unavailable: %v", err)
	}
	if !ok {
		t.Fatal("native ACL fixture or same-fd clear/readback inconclusive or failed")
	}
}
