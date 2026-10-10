//go:build linux || darwin

package shellinstaller

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The same authenticated Shell Node executes the pinned platform CLI directly;
// the omitted package/node is neither needed nor acquired by this command.
func userCodeGraphCommand(root string) string {
	root = strings.ReplaceAll(root, "'", "'\\''")
	return "#!/bin/sh\nexec '" + root + "/runtime/node/bin/node' '" + root + "/runtime/codegraph/node_modules/@colbymchenry/codegraph-linux-x64/lib/dist/bin/codegraph.js' \"$@\"\n"
}

func userStageCodeGraphExecutable(ctx context.Context, stage, destination string) error {
	if ctx == nil || !privateHierarchyPath(destination) {
		return privateError("refused", errors.New("CodeGraph command requires an absolute selected destination"))
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	base := filepath.Join(stage, "runtime/codegraph")
	identity, err := privateDirectory(base)
	if err != nil {
		return err
	}
	directory, err := os.OpenRoot(base)
	if err != nil {
		return err
	}
	defer directory.Close()
	opened, err := directory.Stat(".")
	if err != nil || !os.SameFile(identity, opened) {
		return privateError("preimage", err)
	}
	if err := directory.Mkdir("bin", 0700); err != nil {
		return err
	}
	data := []byte(userCodeGraphCommand(destination))
	if err := userWriteWithSync("bin/codegraph", data, 0700, func(path string, flags int, mode os.FileMode) (userSyncedFile, error) {
		return directory.OpenFile(path, flags, mode)
	}); err != nil {
		return err
	}
	path := filepath.Join(base, "bin/codegraph")
	if _, err := privateNativeFile(ctx, path, 0700, int64(len(data)), fmt.Sprintf("%x", sha256.Sum256(data))); err != nil {
		return err
	}
	after, err := privateDirectory(base)
	if err != nil || !os.SameFile(identity, after) {
		return privateError("preimage", err)
	}
	return nil
}
