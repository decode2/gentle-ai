//go:build windows && shell_experience_windows_guest

package shellinstaller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

const experienceGuestRoot = "GENTLE_EXPERIENCE_GUEST_ROOT"

// This explicit test-only entry reuses the production suspended-start Job
// Object, never a copied SDK helper or an alternate installer frontend.
func TestMain(m *testing.M) {
	worker := os.Getenv(experienceGuestRoot) != ""
	if !experienceGuestInvocation(os.Args[1:], worker) {
		fmt.Fprintln(os.Stderr, "REFUSED: only the fixed experience guest invocation is supported")
		os.Exit(2)
	}
	if err := UserKernelCheck(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if worker {
		cwd, err := os.Getwd()
		if err != nil || cwd != os.Getenv(experienceGuestRoot) {
			fmt.Fprintln(os.Stderr, "REFUSED: private worker directory differs", err)
			os.Exit(2)
		}
		if _, err := userWindowsIdentity(cwd, true); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if err := userWindowsWorkerCheck(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Println("EXPERIENCE_JOB_PASS memory=3221225472 processes=64 cpu=1-logical suspended-before-binding=true")
		code := m.Run()
		if err := userWindowsJobQuiesce(2 * time.Second); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Println("EXPERIENCE_WORKER_QUIESCED sole-worker=true")
		os.Exit(code)
	}
	if err := experienceGuestSupervise(); err != nil {
		fmt.Fprintln(os.Stderr, "EXPERIENCE_GUEST_FAILED:", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func experienceGuestSupervise() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	image, err := userWindowsRead(self, 128<<20)
	if err != nil {
		return err // Copy into an owned local NTFS folder before execution.
	}
	root, err := os.MkdirTemp("", "gentle-experience-guest-")
	if err != nil {
		return err
	}
	fmt.Printf("EXPERIENCE_GUEST_ROOT=%s\nEXPERIENCE_IMAGE_SHA256=%s\n", root, userWindowsSHA(image))
	// Preserve the private directory and complete logs on success or failure.
	// Neither this runner nor its tests remove any preexisting installation.
	if err := userWindowsPrivate(root); err != nil {
		return err
	}
	for _, name := range []string{"tmp", "home", "appdata", "localappdata"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			return err
		}
	}
	logPath := filepath.Join(root, "result.txt")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, self, experienceGuestArguments()...)
	command.Dir = root
	command.Env = []string{
		"SystemRoot=" + os.Getenv("SystemRoot"), "WINDIR=" + os.Getenv("SystemRoot"),
		experienceGuestRoot + "=" + root,
		"TEMP=" + filepath.Join(root, "tmp"), "TMP=" + filepath.Join(root, "tmp"),
		"HOME=" + filepath.Join(root, "home"), "USERPROFILE=" + filepath.Join(root, "home"),
		"APPDATA=" + filepath.Join(root, "appdata"), "LOCALAPPDATA=" + filepath.Join(root, "localappdata"),
	}
	command.Stdout, command.Stderr = log, log
	release, startErr := userWindowsStart(command)
	if startErr != nil {
		return errors.Join(startErr, log.Sync(), log.Close())
	}
	waitErr := command.Wait()
	stopErr := release()
	fileErr := errors.Join(log.Sync(), log.Close())
	fmt.Printf("EXPERIENCE_RESULT_FILE=%s\n", logPath)
	data, readErr := userWindowsRead(logPath, 256<<10)
	if readErr == nil {
		fmt.Printf("%s", data)
	}
	return errors.Join(waitErr, stopErr, fileErr, readErr, ctx.Err())
}
