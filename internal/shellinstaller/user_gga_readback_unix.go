//go:build linux || darwin

package shellinstaller

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Read-only retained custody; neither invokes GGA nor activates its PATH.
func userGGAReadback(ctx context.Context, root string) error {
	if ctx == nil {
		return privateError("source", errors.New("missing GGA integrity context"))
	}
	base := filepath.Join(root, "runtime/gga")
	identities := make(map[string]os.FileInfo)
	for _, expected := range []struct {
		name  string
		count int
	}{{"", 3}, {"bin", 1}, {"lib", 3}} {
		directory := filepath.Join(base, expected.name)
		identity, err := privateDirectory(directory)
		if err != nil {
			return err
		}
		identities[directory] = identity
		entries, err := privateNativeEntries(directory)
		if err != nil || len(entries) != expected.count {
			return privateError("source", errors.Join(err, errors.New("GGA retained layout differs")))
		}
	}
	path := filepath.Join(base, "source.tgz")
	archive, err := userGGAReadArchive(ctx, path, userGGAOpenArchive)
	if err != nil {
		return err
	}
	files, err := userGGAMembers(ctx, archive)
	if err != nil {
		return err
	}
	files = append(files, userExperienceStageFile{"source.tgz", archive})
	for _, file := range files {
		path := filepath.Join(base, file.name)
		info, err := privatePhysical(path)
		if err != nil || info.Sys().(*syscall.Stat_t).Nlink != 1 {
			return privateError("source", errors.Join(err, errors.New("GGA member alias refused")))
		}
		mode := os.FileMode(0700)
		if file.name == "source.tgz" {
			mode = 0600
		}
		pin := fmt.Sprintf("%x", sha256.Sum256(file.data))
		if _, err := privateNativeFile(ctx, path, mode, int64(len(file.data)), pin); err != nil {
			return err
		}
		fresh, err := privatePhysical(path)
		if err != nil || !os.SameFile(info, fresh) || fresh.Sys().(*syscall.Stat_t).Nlink != 1 {
			return privateError("source", errors.Join(err, errors.New("GGA member identity changed")))
		}
	}
	for directory, identity := range identities {
		fresh, err := privateDirectory(directory)
		if err != nil || !os.SameFile(identity, fresh) {
			return privateError("preimage", err)
		}
	}
	return nil
}
