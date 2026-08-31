package repograph

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/asx8678/ultra/internal/fsext"
)

const (
	cacheFilePrefix      = "repograph-v2-"
	cacheFilePrefixStem  = "repograph-v"
	maxSnapshotCacheSize = 256 << 20
)

func snapshotCachePath(cacheDir, canonicalRoot string) string {
	if cacheDir == "" {
		return ""
	}
	return filepath.Join(cacheDir, cacheFilePrefix+stableID(canonicalRoot)+".json")
}

func loadSnapshot(path, canonicalRoot string) (*Snapshot, error) {
	if path == "" {
		return nil, nil
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open repository graph cache: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat repository graph cache: %w", err)
	}
	if info.Size() > maxSnapshotCacheSize {
		return nil, fmt.Errorf("repository graph cache exceeds %d bytes", maxSnapshotCacheSize)
	}

	decoder := json.NewDecoder(bufio.NewReader(file))
	var snapshot Snapshot
	if err := decoder.Decode(&snapshot); err != nil {
		return nil, fmt.Errorf("decode repository graph cache: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("decode repository graph cache: trailing JSON value")
		}
		return nil, fmt.Errorf("decode repository graph cache: %w", err)
	}
	if snapshot.Schema != SchemaVersion {
		return nil, fmt.Errorf("repository graph cache schema %d is not supported", snapshot.Schema)
	}
	if snapshot.Root != canonicalRoot {
		return nil, errors.New("repository graph cache belongs to a different root")
	}
	if err := validateCachedSnapshot(&snapshot); err != nil {
		return nil, fmt.Errorf("validate repository graph cache: %w", err)
	}
	sortNodes(snapshot.Nodes)
	sortEdges(snapshot.Edges)
	sort.Strings(snapshot.Coverage.Warnings)
	return cloneSnapshot(&snapshot), nil
}

func persistSnapshot(path string, snapshot *Snapshot) error {
	if path == "" || snapshot == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create repository graph cache directory: %w", err)
	}

	temporary, err := os.CreateTemp(filepath.Dir(path), ".repograph-*.tmp")
	if err != nil {
		return fmt.Errorf("create repository graph cache: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()

	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("set repository graph cache permissions: %w", err)
	}
	// Nodes, edges, and lookup indexes are derived data. Persist only source
	// facts and coverage, then rebuild after facts are verified against current
	// source bytes on the next process start.
	persisted := *snapshot
	persisted.Nodes = nil
	persisted.Edges = nil
	persisted.index = nil
	encoder := json.NewEncoder(temporary)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(&persisted); err != nil {
		return fmt.Errorf("encode repository graph cache: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync repository graph cache: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close repository graph cache: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace repository graph cache: %w", err)
	}
	committed = true

	if err := fsext.SyncDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sync repository graph cache directory: %w", err)
	}
	return nil
}

func validateCachedSnapshot(snapshot *Snapshot) error {
	if snapshot.Facts == nil {
		return errors.New("facts map is missing")
	}
	if snapshot.Generation == 0 && (len(snapshot.Facts) != 0 || len(snapshot.Nodes) != 0 || len(snapshot.Edges) != 0) {
		return errors.New("non-empty snapshot has generation zero")
	}
	if snapshot.Generation == ^uint64(0) {
		return errors.New("snapshot generation would overflow")
	}
	if len(snapshot.Facts) > maxIndexedFiles {
		return fmt.Errorf("facts exceed repository file limit: %d", len(snapshot.Facts))
	}
	var sourceBytes int64
	for path, facts := range snapshot.Facts {
		if !validRelativePath(path) || facts.Path != path {
			return fmt.Errorf("invalid fact path %q", path)
		}
		digest, err := hex.DecodeString(facts.Hash)
		if err != nil || len(digest) != 32 || facts.Language == "" || facts.Size < 0 {
			return fmt.Errorf("invalid facts for %q", path)
		}
		if facts.Size > maxIndexedSourceBytes-sourceBytes {
			return fmt.Errorf("facts exceed repository source byte limit: %d", maxIndexedSourceBytes)
		}
		sourceBytes += facts.Size
	}
	nodeIDs := make(map[string]struct{}, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		if node.ID == "" {
			return errors.New("node with empty ID")
		}
		if _, exists := nodeIDs[node.ID]; exists {
			return fmt.Errorf("duplicate node ID %q", node.ID)
		}
		nodeIDs[node.ID] = struct{}{}
		if node.Path != "" && !validRelativePath(node.Path) {
			return fmt.Errorf("invalid node path %q", node.Path)
		}
	}
	for _, edge := range snapshot.Edges {
		if _, exists := nodeIDs[edge.From]; !exists {
			return fmt.Errorf("edge source %q is missing", edge.From)
		}
		if _, exists := nodeIDs[edge.To]; !exists {
			return fmt.Errorf("edge target %q is missing", edge.To)
		}
		if edge.Path != "" && !validRelativePath(edge.Path) {
			return fmt.Errorf("invalid edge path %q", edge.Path)
		}
	}
	coverage := snapshot.Coverage
	if coverage.Discovered < 0 || coverage.Indexed < 0 || coverage.Reused < 0 || coverage.Unsupported < 0 || coverage.Generated < 0 || coverage.Oversized < 0 || coverage.Unreadable < 0 || coverage.Omitted < 0 {
		return errors.New("coverage contains a negative count")
	}
	return nil
}

func validRelativePath(path string) bool {
	if path == "" || filepath.IsAbs(path) || strings.ContainsRune(path, '\x00') {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	return clean == path && clean != ".." && !strings.HasPrefix(clean, "../")
}
