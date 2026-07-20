//go:build darwin

package reviewtransaction

import "golang.org/x/sys/unix"

func secureDirectoryOpenFlags() int {
	return unix.O_EVTONLY | unix.O_DIRECTORY | unix.O_CLOEXEC
}
