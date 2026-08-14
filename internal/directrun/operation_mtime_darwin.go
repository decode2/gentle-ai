//go:build darwin

package directrun

import "golang.org/x/sys/unix"

func operationMtime(st *unix.Stat_t) uint64 {
	return uint64(st.Mtim.Sec)<<32 | uint64(st.Mtim.Nsec)
}
