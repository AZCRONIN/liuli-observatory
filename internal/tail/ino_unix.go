//go:build !windows

package tail

import (
	"os"
	"syscall"
)

func fileID(fi os.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Ino)
	}
	return 0
}
