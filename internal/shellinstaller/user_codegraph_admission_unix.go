//go:build linux || darwin

package shellinstaller

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"syscall"

	"golang.org/x/sys/unix"
)

// Reconstruct only the fixed retained sources, authenticate both whole SRIs,
// then admit their exact original members. No process, download or index.
func userCodeGraphReadback(ctx context.Context, root string) error {
	if ctx == nil {
		return privateError("source", errors.New("missing CodeGraph admission context"))
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return privateError("refused", errors.New("CodeGraph retained library requires Linux amd64"))
	}
	base := filepath.Join(root, "runtime/codegraph")
	archives := make([][]byte, len(userCodeGraphSources))
	for i, source := range userCodeGraphSources {
		for offset, part := int64(0), 0; offset < source.size; offset, part = offset+(32<<20), part+1 {
			path := filepath.Join(base, fmt.Sprintf("sources/%s.%d.tgz-part", source.name, part))
			data, err := userCodeGraphRetainedPart(ctx, path, min(32<<20, source.size-offset))
			if err != nil {
				return err
			}
			archives[i] = append(archives[i], data...)
		}
	}
	files, err := userCodeGraphMembers(ctx, archives)
	if err != nil {
		return err
	}
	files = append(files, userExperienceStageFile{"bin/codegraph", []byte(userCodeGraphCommand(root))})
	children := map[string]map[string]bool{"": {}}
	for _, file := range files {
		for name := file.name; name != "."; name = filepath.Dir(name) {
			parent := filepath.Dir(name)
			if parent == "." {
				parent = ""
			}
			if children[parent] == nil {
				children[parent] = make(map[string]bool)
			}
			children[parent][filepath.Base(name)] = true
		}
	}
	identities := make(map[string]os.FileInfo)
	for name, expected := range children {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(base, name)
		identity, err := privateDirectory(path)
		if err != nil {
			return err
		}
		directory, err := os.Open(path)
		if err != nil {
			return err
		}
		opened, statErr := directory.Stat()
		entries, readErr := directory.ReadDir(len(expected) + 1)
		if err := errors.Join(statErr, readErr, directory.Close()); err != nil || !os.SameFile(identity, opened) || len(entries) != len(expected) {
			return privateError("source", errors.Join(err, errors.New("CodeGraph retained layout differs")))
		}
		for _, entry := range entries {
			if !expected[entry.Name()] {
				return privateError("source", errors.New("CodeGraph unrequested member refused"))
			}
		}
		identities[path] = identity
	}
	for _, file := range files {
		path := filepath.Join(base, file.name)
		before, err := privatePhysical(path)
		if err != nil || before.Sys().(*syscall.Stat_t).Nlink != 1 {
			return privateError("source", errors.Join(err, errors.New("CodeGraph member alias refused")))
		}
		pin := fmt.Sprintf("%x", sha256.Sum256(file.data))
		mode := os.FileMode(0600)
		if file.name == "bin/codegraph" {
			mode = 0700
		}
		if _, err := privateNativeFile(ctx, path, mode, int64(len(file.data)), pin); err != nil {
			return err
		}
		after, err := privatePhysical(path)
		if err != nil || privateStamp(before) != privateStamp(after) {
			return privateError("source", errors.Join(err, errors.New("CodeGraph member identity changed")))
		}
	}
	for path, before := range identities {
		after, err := privateDirectory(path)
		if err != nil || !os.SameFile(before, after) {
			return privateError("preimage", err)
		}
	}
	return nil
}

func userCodeGraphRetainedPart(ctx context.Context, path string, size int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	before, err := privatePhysical(path)
	if err != nil || size < 0 || size > 32<<20 || before.Mode() != 0600 || before.Size() != size || before.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) || before.Sys().(*syscall.Stat_t).Nlink != 1 {
		return nil, privateError("source", errors.Join(err, errors.New("CodeGraph source part custody differs")))
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, errors.Join(err, file.Close(), errors.New("CodeGraph opened part differs"))
	}
	data, readErr := io.ReadAll(io.LimitReader(file, size+1))
	closeErr := file.Close()
	after, statErr := privatePhysical(path)
	if err := errors.Join(readErr, closeErr, statErr, ctx.Err()); err != nil || int64(len(data)) != size || privateStamp(before) != privateStamp(after) {
		return nil, privateError("source", errors.Join(err, errors.New("CodeGraph retained source changed")))
	}
	return data, nil
}
