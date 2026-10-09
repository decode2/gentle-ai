//go:build windows

package shellinstaller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// userStageExperience writes only an unpublished installer stage. The caller
// retains cleanup/uncertainty ownership; no public profile acceptance is added.
// Native SID/DACL, NTFS, reparse and hard-link custody remain mandatory.
func userStageExperience(ctx context.Context, root string, profile *UserExperience) error {
	if profile == nil {
		return nil
	}
	files, err := userStageExperienceFiles(profile)
	if err != nil {
		return err
	}
	if ctx == nil {
		return context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	rootIdentity, err := userWindowsIdentity(root, true)
	if err != nil {
		return err
	}
	config := filepath.Join(root, "config")
	identity, err := userWindowsIdentity(config, true)
	if err != nil {
		return err
	}
	before, err := os.Stat(config)
	if err != nil {
		return err
	}
	directory, err := os.OpenRoot(config)
	if err != nil {
		return err
	}
	defer directory.Close()
	opened, err := directory.Stat(".")
	if err != nil || !os.SameFile(before, opened) {
		return errors.Join(err, errors.New("Windows stage configuration pin differs"))
	}
	// Check all intended preimages before any write. Unmanaged has no persona
	// entry, so neither its historical bytes nor its metadata are inspected.
	for _, file := range files {
		if _, err := directory.Lstat(file.name); !os.IsNotExist(err) {
			return errors.Join(err, errors.New("Windows staged preference already exists"))
		}
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if fresh, err := userWindowsIdentity(config, true); err != nil || fresh != identity {
			return errors.Join(err, errors.New("Windows stage configuration identity differs"))
		}
		// Relative exclusive creation stays pinned to the verified directory.
		output, err := directory.OpenFile(file.name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, writeErr := output.Write(file.data)
		syncErr := output.Sync()
		closeErr := output.Close()
		if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
			return err
		}
		actual, err := userWindowsRead(filepath.Join(config, file.name), int64(len(file.data)))
		if err != nil || string(actual) != string(file.data) {
			return errors.Join(err, errors.New("Windows staged preference custody/readback differs"))
		}
	}
	for path, expected := range map[string]string{root: rootIdentity, config: identity} {
		if fresh, err := userWindowsIdentity(path, true); err != nil || fresh != expected {
			return errors.Join(err, errors.New("Windows stage final identity differs"))
		}
	}
	return ctx.Err()
}
