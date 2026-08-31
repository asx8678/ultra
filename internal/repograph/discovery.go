package repograph

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/asx8678/ultra/internal/fsext"
)

const (
	maxIndexedFileSize      int64 = 2 << 20
	maxIndexedSourceBytes   int64 = 128 << 20
	maxIndexedFiles               = 8_000
	binaryProbeSize               = 8 << 10
	discoveryHashBufferSize       = 64 << 10
)

var discoveryHashBufferPool = sync.Pool{
	New: func() any {
		buffer := make([]byte, discoveryHashBufferSize)
		return &buffer
	},
}

type discoveredFile struct {
	path     string
	language string
	hash     string
	size     int64
	data     []byte
}

type discoveryResult struct {
	files    []discoveredFile
	coverage Coverage
}

type discoveryCandidate struct {
	path         string
	rel          string
	language     string
	previousHash string
}

type discoveryOutcome struct {
	file        *discoveredFile
	unreadable  bool
	oversized   bool
	unsupported bool
	generated   bool
	warning     string
}

func indexedCandidateFits(count int, selectedBytes, nextSize int64) bool {
	if count >= maxIndexedFiles || selectedBytes < 0 || nextSize < 0 || selectedBytes > maxIndexedSourceBytes {
		return false
	}
	return nextSize <= maxIndexedSourceBytes-selectedBytes
}

// discoverFiles returns a deterministic, root-relative inventory. Directory
// traversal and policy filtering stay serial and deterministic; safe regular
// files are then read and hashed concurrently.
func discoverFiles(ctx context.Context, root string, excludedDirs ...string) (discoveryResult, error) {
	return discoverFilesWithFacts(ctx, root, nil, excludedDirs...)
}

// discoverFilesWithFacts avoids retaining source bytes when a strict content
// hash proves that the previous immutable facts are still current.
func discoverFilesWithFacts(
	ctx context.Context,
	root string,
	previous map[string]FileFacts,
	excludedDirs ...string,
) (discoveryResult, error) {
	result := discoveryResult{}
	walker := fsext.NewFastGlobWalker(root)
	excluded := make(map[string]struct{}, len(excludedDirs))
	for _, directory := range excludedDirs {
		if directory != "" {
			excluded[filepath.Clean(directory)] = struct{}{}
		}
	}

	var candidates []discoveryCandidate
	var candidateBytes int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			if path == root {
				return walkErr
			}
			result.coverage.Unreadable++
			result.coverage.Warnings = append(result.coverage.Warnings, warningPath(root, path, "unreadable"))
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if path != root {
				if repoGraphRuntimeDir(root, path) {
					return filepath.SkipDir
				}
				if _, skip := excluded[filepath.Clean(path)]; skip {
					return filepath.SkipDir
				}
				if walker.ShouldSkipDir(path) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if filepath.Clean(filepath.Dir(path)) == filepath.Clean(root) &&
			(strings.HasPrefix(entry.Name(), cacheFilePrefixStem) || strings.HasPrefix(entry.Name(), ".repograph-")) {
			return nil
		}
		if walker.ShouldSkip(path) {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			result.coverage.Discovered++
			result.coverage.Unreadable++
			result.coverage.Warnings = append(result.coverage.Warnings, warningPath(root, path, "unreadable"))
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}

		result.coverage.Discovered++
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			result.coverage.Unreadable++
			result.coverage.Warnings = append(result.coverage.Warnings, warningPath(root, path, "outside repository root"))
			return nil
		}
		rel = filepath.ToSlash(rel)
		language := languageForPath(rel)
		if language == "" {
			result.coverage.Unsupported++
			return nil
		}
		if info.Size() > maxIndexedFileSize {
			result.coverage.Oversized++
			return nil
		}
		if !indexedCandidateFits(len(candidates), candidateBytes, info.Size()) {
			result.coverage.Omitted++
			return nil
		}
		candidate := discoveryCandidate{path: path, rel: rel, language: language}
		if old, ok := previous[rel]; ok && old.Size == info.Size() && old.Language == language {
			candidate.previousHash = old.Hash
		}
		candidates = append(candidates, candidate)
		candidateBytes += info.Size()
		return nil
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return discoveryResult{}, ctx.Err()
		}
		return discoveryResult{}, fmt.Errorf("walk repository: %w", err)
	}

	outcomes := make([]discoveryOutcome, len(candidates))
	workers := min(runtime.GOMAXPROCS(0)*2, 32, len(candidates))
	var next atomic.Uint64
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for {
				index := int(next.Add(1) - 1)
				if index >= len(candidates) {
					return
				}
				if ctx.Err() != nil {
					return
				}
				outcomes[index] = inspectDiscoveryCandidate(ctx, root, candidates[index])
			}
		}()
	}
	wait.Wait()
	if err := ctx.Err(); err != nil {
		return discoveryResult{}, err
	}

	for _, outcome := range outcomes {
		switch {
		case outcome.file != nil:
			result.files = append(result.files, *outcome.file)
		case outcome.unreadable:
			result.coverage.Unreadable++
		case outcome.oversized:
			result.coverage.Oversized++
		case outcome.unsupported:
			result.coverage.Unsupported++
		case outcome.generated:
			result.coverage.Generated++
		}
		if outcome.warning != "" {
			result.coverage.Warnings = append(result.coverage.Warnings, outcome.warning)
		}
	}

	if result.coverage.Omitted > 0 {
		result.coverage.Warnings = append(result.coverage.Warnings,
			fmt.Sprintf("repository indexing limit reached: %d supported files omitted", result.coverage.Omitted))
	}
	sort.Slice(result.files, func(i, j int) bool { return result.files[i].path < result.files[j].path })
	sort.Strings(result.coverage.Warnings)
	return result, nil
}

func inspectDiscoveryCandidate(ctx context.Context, root string, candidate discoveryCandidate) discoveryOutcome {
	if candidate.previousHash != "" {
		outcome, unchanged := inspectUnchangedDiscoveryCandidate(ctx, root, candidate)
		if unchanged || outcome.unreadable || outcome.oversized {
			return outcome
		}
	}
	return inspectDiscoveryCandidateData(ctx, root, candidate)
}

func inspectUnchangedDiscoveryCandidate(
	ctx context.Context,
	root string,
	candidate discoveryCandidate,
) (discoveryOutcome, bool) {
	current, err := os.Lstat(candidate.path)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() {
		return discoveryOutcome{unreadable: true, warning: warningPath(root, candidate.path, "unreadable")}, false
	}
	if current.Size() > maxIndexedFileSize {
		return discoveryOutcome{oversized: true}, false
	}
	file, err := os.Open(candidate.path)
	if err != nil {
		return discoveryOutcome{unreadable: true, warning: warningPath(root, candidate.path, "unreadable")}, false
	}
	openedInfo, statErr := file.Stat()
	hasher := sha256.New()
	buffer := discoveryHashBufferPool.Get().(*[]byte)
	readBytes, readErr := io.CopyBuffer(hasher, io.LimitReader(file, maxIndexedFileSize+1), *buffer)
	discoveryHashBufferPool.Put(buffer)
	closeErr := file.Close()
	if statErr != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(current, openedInfo) || readErr != nil || closeErr != nil {
		return discoveryOutcome{unreadable: true, warning: warningPath(root, candidate.path, "unreadable")}, false
	}
	if ctx.Err() != nil {
		return discoveryOutcome{}, false
	}
	if readBytes > maxIndexedFileSize {
		return discoveryOutcome{oversized: true}, false
	}
	digest := hex.EncodeToString(hasher.Sum(nil))
	if digest != candidate.previousHash {
		return discoveryOutcome{}, false
	}
	return discoveryOutcome{file: &discoveredFile{
		path: candidate.rel, language: candidate.language,
		hash: digest, size: readBytes,
	}}, true
}

func inspectDiscoveryCandidateData(ctx context.Context, root string, candidate discoveryCandidate) discoveryOutcome {
	current, err := os.Lstat(candidate.path)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() {
		return discoveryOutcome{unreadable: true, warning: warningPath(root, candidate.path, "unreadable")}
	}
	if current.Size() > maxIndexedFileSize {
		return discoveryOutcome{oversized: true}
	}
	file, err := os.Open(candidate.path)
	if err != nil {
		return discoveryOutcome{unreadable: true, warning: warningPath(root, candidate.path, "unreadable")}
	}
	openedInfo, statErr := file.Stat()
	data, readErr := io.ReadAll(io.LimitReader(file, maxIndexedFileSize+1))
	closeErr := file.Close()
	if statErr != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(current, openedInfo) || readErr != nil || closeErr != nil {
		return discoveryOutcome{unreadable: true, warning: warningPath(root, candidate.path, "unreadable")}
	}
	if ctx.Err() != nil {
		return discoveryOutcome{}
	}
	if int64(len(data)) > maxIndexedFileSize {
		return discoveryOutcome{oversized: true}
	}
	if isBinary(data) {
		return discoveryOutcome{unsupported: true, warning: candidate.rel + ": binary file skipped"}
	}
	if isGenerated(data) {
		return discoveryOutcome{generated: true}
	}

	digest := sha256.Sum256(data)
	return discoveryOutcome{file: &discoveredFile{
		path: candidate.rel, language: candidate.language,
		hash: hex.EncodeToString(digest[:]), size: int64(len(data)), data: data,
	}}
}

func isBinary(data []byte) bool {
	if len(data) > binaryProbeSize {
		data = data[:binaryProbeSize]
	}
	return bytes.IndexByte(data, 0) >= 0
}

func isGenerated(data []byte) bool {
	if len(data) > 8<<10 {
		data = data[:8<<10]
	}
	comments := generatedHeaderComments(data)
	if strings.Contains(comments, "code generated") && strings.Contains(comments, "do not edit") {
		return true
	}
	markers := [...]string{
		"@generated", "automatically generated", "auto-generated",
		"autogenerated file", "generated by ",
	}
	for _, marker := range markers {
		if strings.Contains(comments, marker) {
			return true
		}
	}
	return false
}

// generatedHeaderComments returns comment text only. Generated markers in
// string literals or ordinary source code must not suppress a legitimate file.
func generatedHeaderComments(data []byte) string {
	lines := bytes.Split(data, []byte{'\n'})
	if len(lines) > 80 {
		lines = lines[:80]
	}
	var builder strings.Builder
	inBlock := false
	for _, raw := range lines {
		line := strings.TrimSpace(string(raw))
		for line != "" {
			if inBlock {
				end := strings.Index(line, "*/")
				if end < 0 {
					builder.WriteString(strings.ToLower(line))
					builder.WriteByte('\n')
					break
				}
				builder.WriteString(strings.ToLower(line[:end]))
				builder.WriteByte('\n')
				line = strings.TrimSpace(line[end+2:])
				inBlock = false
				continue
			}
			switch {
			case strings.HasPrefix(line, "//"):
				builder.WriteString(strings.ToLower(strings.TrimSpace(line[2:])))
				builder.WriteByte('\n')
			case strings.HasPrefix(line, "#"):
				builder.WriteString(strings.ToLower(strings.TrimSpace(line[1:])))
				builder.WriteByte('\n')
			case strings.HasPrefix(line, ";"):
				builder.WriteString(strings.ToLower(strings.TrimSpace(line[1:])))
				builder.WriteByte('\n')
			case strings.HasPrefix(line, "--"):
				builder.WriteString(strings.ToLower(strings.TrimSpace(line[2:])))
				builder.WriteByte('\n')
			case strings.HasPrefix(line, "<!--"):
				end := strings.Index(line[4:], "-->")
				if end < 0 {
					builder.WriteString(strings.ToLower(line[4:]))
				} else {
					builder.WriteString(strings.ToLower(line[4 : 4+end]))
				}
				builder.WriteByte('\n')
			case strings.HasPrefix(line, "/*"):
				line = strings.TrimSpace(line[2:])
				inBlock = true
				continue
			default:
				// Generated notices are header metadata. Once real source starts,
				// later comments describing generated output are ordinary content.
				return builder.String()
			}
			break
		}
	}
	return builder.String()
}

func repoGraphRuntimeDir(root, directory string) bool {
	relative, err := filepath.Rel(root, directory)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false
	}
	first := strings.SplitN(filepath.ToSlash(relative), "/", 2)[0]
	switch first {
	case ".pi", ".ultra", ".crush", "autoresearch":
		return true
	default:
		return false
	}
}

func warningPath(root, path, reason string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		rel = filepath.Base(path)
	}
	return filepath.ToSlash(rel) + ": " + reason
}
