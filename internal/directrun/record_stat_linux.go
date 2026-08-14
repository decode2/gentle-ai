//go:build linux

package directrun

import "golang.org/x/sys/unix"

func sameRecordTimes(a, b *unix.Stat_t) bool {
	return a.Mtim == b.Mtim && a.Ctim == b.Ctim
}
