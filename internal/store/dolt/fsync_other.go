//go:build !darwin

package dolt

import "os"

// fullSync is fsync(2) everywhere but darwin, where the drive cache needs a
// separate request.
func fullSync(f *os.File) error { return f.Sync() }
