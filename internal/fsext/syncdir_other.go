//go:build !windows

package fsext

import (
	"errors"
	"os"
)

// SyncDirectory durably records directory-entry changes where the platform
// exposes directory file descriptors.
func SyncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
