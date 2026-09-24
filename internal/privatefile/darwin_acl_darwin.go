//go:build darwin && cgo

package privatefile

/*
#include <errno.h>
#include <sys/types.h>
#include <sys/acl.h>

// Clear stale errno so only acl_get_fd_np can establish the no-ACL case.
static acl_t privateACLGetFD(int fd) {
	errno = 0;
	return acl_get_fd_np(fd, ACL_TYPE_EXTENDED);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// probeDarwinPrivateACL verifies an already-open regular file. The caller
// must keep fd open and prevent concurrent changes to its metadata/ACL.
func probeDarwinPrivateACL(fd int) error {
	var st syscall.Stat_t
	if fd < 0 {
		return errors.New("invalid private file descriptor")
	}
	if err := syscall.Fstat(fd, &st); err != nil {
		return fmt.Errorf("stat private file descriptor: %w", err)
	}
	if st.Mode&syscall.S_IFMT != syscall.S_IFREG || st.Uid != uint32(os.Geteuid()) || st.Mode&0o7077 != 0 {
		return errors.New("private file is not owner-only regular file")
	}

	acl, getErr := C.privateACLGetFD(C.int(fd))
	if acl == nil {
		// Apple's acl_get_fd_np reports ENOENT for a file with no extended
		// ACL. The successful fstat above rules out an invalid descriptor;
		// no other retrieval error is evidence of an empty ACL.
		if getErr == syscall.ENOENT {
			return nil
		}
		return fmt.Errorf("retrieve private file ACL: %w", getErr)
	}

	var entry C.acl_entry_t
	entryResult, entryErr := C.acl_get_entry(acl, C.ACL_FIRST_ENTRY, &entry)
	freeResult, freeErr := C.acl_free(unsafe.Pointer(acl))
	if entryResult != 0 {
		return fmt.Errorf("inspect private file ACL: %w", entryErr)
	}
	if freeResult != 0 {
		return fmt.Errorf("free private file ACL: %w", freeErr)
	}
	// Any entry, including a deny or an inherited entry, is rejected:
	// interpreting principal/permission interactions cannot prove privacy.
	return errors.New("private file has ACL entries")
}
