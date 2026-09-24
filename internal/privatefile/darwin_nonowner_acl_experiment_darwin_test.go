//go:build darwin && cgo && privatefileowneraclexperiment

package privatefile

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

const (
	probeDenied = 41 + iota
	probeCanaryFailure
	probeStatFailure
	probeOpenSucceeded
	probeOtherError
	probeIdentityFailure
)

const (
	probeEnv    = "GENTLE_PRIVATEFILE_PROBE"
	probeCanary = "benign traversal control\n"
	probeMarker = "synthetic privatefile ACL probe\n"
)

// Invoked only in a copied test binary, before the Go test harness can print PASS.
// Codes distinguish an ACL denial from a broken control or an unrelated error.
func TestDarwinOwnerACLProbeChild(t *testing.T) {
	if os.Getenv(probeEnv) != "1" {
		return
	}
	args := flag.Args()
	if len(args) != 6 || args[0] != "probe" {
		os.Exit(probeOtherError)
	}
	openMode := 0
	switch args[1] {
	case "read":
		openMode = syscall.O_RDONLY
	case "write":
		openMode = syscall.O_WRONLY
	default:
		os.Exit(probeOtherError)
	}
	uid, err := strconv.Atoi(args[4])
	if err != nil || uid == 0 || os.Geteuid() != uid || os.Getuid() != uid {
		os.Exit(probeIdentityFailure)
	}
	control, err := os.ReadFile(args[2])
	if err != nil || !bytes.Equal(control, []byte(probeCanary)) {
		os.Exit(probeCanaryFailure)
	}
	info, err := os.Lstat(args[3])
	owner, ownerErr := strconv.ParseUint(args[5], 10, 32)
	if err != nil || ownerErr != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 ||
		info.Size() != int64(len(probeMarker)) || info.Sys().(*syscall.Stat_t).Uid != uint32(owner) {
		os.Exit(probeStatFailure)
	}
	fd, err := syscall.Open(args[3], openMode|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err == nil {
		syscall.Close(fd)
		os.Exit(probeOpenSucceeded)
	}
	if errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) {
		os.Exit(probeDenied)
	}
	os.Exit(probeOtherError)
}

func TestDarwinOwnerACLNonownerDenialExperiment(t *testing.T) {
	file, path, before := newOwnerACLFixture(t)
	// The child must still be empty when A checks its held-FD inode and ACL.
	proveOwnerACLReplacement(t, file, path, before)
	parent := filepath.Dir(path)

	canary := filepath.Join(parent, "control")
	if err := os.WriteFile(canary, []byte(probeCanary), 0644); err != nil {
		t.Fatalf("create benign canary: %v", err)
	}
	checkExperimentFile(t, canary, 0644, int64(len(probeCanary)), before.Uid)

	// sudo cannot traverse a 0700 fixture parent to run the original test binary.
	// Copy it into this disposable parent before lowering traversal to 0711.
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(executable)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	copyPath := filepath.Join(parent, "probe-binary")
	copyFile, err := os.OpenFile(copyPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		t.Fatal(err)
	}
	n, copyErr := io.Copy(copyFile, source)
	closeErr := copyFile.Close()
	if copyErr != nil || closeErr != nil || n <= 0 {
		t.Fatalf("copy test binary: bytes=%d copy=%v close=%v", n, copyErr, closeErr)
	}
	if err := os.Chmod(copyPath, 0755); err != nil {
		t.Fatal(err)
	}
	checkExperimentFile(t, copyPath, 0755, n, before.Uid)
	if err := os.Chmod(parent, 0711); err != nil {
		t.Fatal(err)
	}
	var parentStat syscall.Stat_t
	if err := syscall.Lstat(parent, &parentStat); err != nil || parentStat.Mode&syscall.S_IFMT != syscall.S_IFDIR ||
		parentStat.Mode&07777 != 0711 || parentStat.Uid != before.Uid {
		t.Fatalf("disposable parent mode/owner: %+v, %v", parentStat, err)
	}

	// Check the fixed account directly before sudo. Keep diagnostics private: id
	// may include system-specific account details on stderr.
	account := exec.Command("/usr/bin/id", "-u", "_nobody")
	accountOut, accountErr := account.Output()
	if accountErr != nil {
		class := "account-lookup-failed"
		if executableUnavailable(accountErr) {
			class = "executable-unavailable"
		}
		t.Fatalf("nonowner account precheck: class=%s exit=%d", class, commandExitCode(accountErr))
	}
	if _, parseErr := strconv.Atoi(strings.TrimSpace(string(accountOut))); parseErr != nil {
		t.Fatalf("nonowner account precheck: class=account-lookup-failed exit=0")
	}

	// Query the effective UID through precisely the passwordless sudo route the
	// probe will use. Keep only a safe failure class and numeric exit code.
	identity := exec.Command("/usr/bin/sudo", "-n", "-u", "_nobody", "/usr/bin/id", "-u")
	identityOut, err := identity.Output()
	if err != nil {
		class := "sudo-command-failed"
		if executableUnavailable(err) {
			class = "executable-unavailable"
		}
		t.Fatalf("passwordless nonowner identity: class=%s exit=%d", class, commandExitCode(err))
	}
	uid, err := strconv.Atoi(strings.TrimSpace(string(identityOut)))
	if err != nil || uid <= 0 || uint32(uid) == before.Uid {
		t.Fatalf("passwordless nonowner identity: class=sudo-command-failed exit=0")
	}

	written, err := file.Write([]byte(probeMarker))
	if err != nil || written != len(probeMarker) {
		t.Fatalf("held-FD marker write: bytes=%d err=%v", written, err)
	}
	if err := file.Sync(); err != nil {
		t.Fatalf("sync held-FD marker: %v", err)
	}
	checkExperimentFile(t, path, 0600, int64(len(probeMarker)), before.Uid)
	var linked syscall.Stat_t
	if err := syscall.Lstat(path, &linked); err != nil || linked.Dev != before.Dev || linked.Ino != before.Ino || linked.Nlink != 1 {
		t.Fatalf("child pathname no longer held inode: %+v, %v", linked, err)
	}
	ownerFD, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("owner pathname open: %v", err)
	}
	owner := os.NewFile(uintptr(ownerFD), path)
	ownerBytes, readErr := io.ReadAll(owner)
	closeErr = owner.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(ownerBytes, []byte(probeMarker)) {
		t.Fatalf("owner pathname read mismatch: read=%v close=%v equal=%t", readErr, closeErr, bytes.Equal(ownerBytes, []byte(probeMarker)))
	}

	// Each child checks actual UID, reads the same-parent control, and stats the
	// child pathname before its nofollow open. Neither mode reads or writes the FD.
	for _, mode := range []string{"read", "write"} {
		cmd := exec.Command("/usr/bin/sudo", "-n", "-u", "_nobody", "/usr/bin/env",
			probeEnv+"=1", copyPath, "-test.run=^TestDarwinOwnerACLProbeChild$", "--",
			"probe", mode, canary, path, strconv.Itoa(uid), strconv.FormatUint(uint64(before.Uid), 10))
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err = cmd.Run()
		status := commandExitCode(err)
		if err == nil {
			status = 0
		}
		if status != probeDenied {
			t.Fatalf("nonowner %s probe: expected_status=%d status=%d stdout_len=%d stderr_len=%d",
				mode, probeDenied, status, stdout.Len(), stderr.Len())
		}
		if stdout.Len() != 0 || stderr.Len() != 0 {
			t.Fatalf("nonowner %s probe output must be empty: status=%d stdout_len=%d stderr_len=%d",
				mode, status, stdout.Len(), stderr.Len())
		}
	}
}

func commandExitCode(err error) int {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return -1
}

func executableUnavailable(err error) bool {
	return errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrPermission)
}

func checkExperimentFile(t *testing.T, path string, mode os.FileMode, size int64, uid uint32) {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil || st.Mode&syscall.S_IFMT != syscall.S_IFREG ||
		st.Mode&07777 != uint16(mode) || st.Size != size || st.Uid != uid || st.Nlink != 1 {
		t.Fatalf("experiment file identity/mode/size %s: %+v, %v (expected mode=%s size=%d uid=%d)",
			path, st, err, fmt.Sprintf("%#o", mode), size, uid)
	}
}
