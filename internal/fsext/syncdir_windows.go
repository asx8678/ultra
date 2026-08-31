//go:build windows

package fsext

// SyncDirectory is a no-op on Windows, where directory handles cannot be
// flushed with os.File.Sync.
func SyncDirectory(string) error {
	return nil
}
