//go:build darwin

package dolt

import (
	"os"
	"syscall"
)

// fullSync forces the drive to flush its own write cache. On darwin fsync(2)
// hands the data to the drive and returns, so it survives a process crash but
// not a power loss; F_FULLFSYNC is the call that does. A filesystem that does
// not implement it falls back to fsync(2), which is still better than nothing.
func fullSync(f *os.File) error {
	_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), syscall.F_FULLFSYNC, 0)
	if errno == 0 {
		return nil
	}
	return f.Sync()
}
