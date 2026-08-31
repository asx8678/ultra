package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/asx8678/ultra/internal/lock"
)

type sharedRunDirLock struct {
	release func()
	refs    int
}

var runDirLocks = struct {
	sync.Mutex
	items map[string]*sharedRunDirLock
}{items: make(map[string]*sharedRunDirLock)}

// acquireAgentRunDirLock serializes mutable run state across processes. Calls
// within one process share a ref-counted lock, which keeps loader tests and
// coordinated in-process maintenance safe without opening a second OS lock.
func acquireAgentRunDirLock(dir string) (func(), error) {
	canonical, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(canonical, 0o700); err != nil {
		return nil, fmt.Errorf("create agent run directory: %w", err)
	}
	runDirLocks.Lock()
	defer runDirLocks.Unlock()
	if existing := runDirLocks.items[canonical]; existing != nil {
		existing.refs++
		return func() { releaseSharedRunDirLock(canonical) }, nil
	}
	release, err := lock.TryFile(filepath.Join(canonical, ".lock"))
	if err != nil {
		return nil, fmt.Errorf("lock agent run directory: %w", err)
	}
	runDirLocks.items[canonical] = &sharedRunDirLock{release: release, refs: 1}
	return func() { releaseSharedRunDirLock(canonical) }, nil
}

func acquireAgentRunMaintenanceLock(dir string) (func(), error) {
	canonical, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	runDirLocks.Lock()
	defer runDirLocks.Unlock()
	if runDirLocks.items[canonical] != nil {
		return nil, fmt.Errorf("lock agent run directory: %w", lock.ErrContended)
	}
	release, err := lock.TryFile(filepath.Join(canonical, ".lock"))
	if err != nil {
		return nil, fmt.Errorf("lock agent run directory: %w", err)
	}
	return release, nil
}

func releaseSharedRunDirLock(dir string) {
	runDirLocks.Lock()
	defer runDirLocks.Unlock()
	entry := runDirLocks.items[dir]
	if entry == nil {
		return
	}
	entry.refs--
	if entry.refs == 0 {
		entry.release()
		delete(runDirLocks.items, dir)
	}
}
