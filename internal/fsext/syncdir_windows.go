//go:build windows

package fsext

import "os"

// SyncDirectory is a no-op on Windows, where directory handles cannot be
// flushed with os.File.Sync.
func SyncDirectory(string) error {
	return nil
}

// ReplaceFile renames source over destination. os.Rename cannot replace an
// existing file on Windows, so the destination is removed first; a crash in
// between leaves no cache file, which callers tolerate by rebuilding.
func ReplaceFile(source, destination string) error {
	if err := os.Remove(destination); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(source, destination)
}
