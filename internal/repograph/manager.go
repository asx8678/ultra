package repograph

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/asx8678/ultra/internal/fsext"
)

var (
	errManagerClosed = errors.New("repository graph manager is closed")
	errNoUsableFiles = errors.New("repository graph contains no usable files")
	errFocusRequired = errors.New("repo_dwell requires a successful semantic repo_focus result")
)

// Manager owns one canonical repository root and its immutable graph
// snapshots. Refreshes are serialized; readers only retain snapshots that are
// never mutated after publication.
type Manager struct {
	root      string
	cacheDir  string
	cachePath string

	refreshGate     chan struct{}
	mu              sync.RWMutex
	closed          bool
	cacheUnverified bool
	snapshot        *Snapshot
	dwell           *dwellState
	warnings        []string
}

// NewManager creates a lazy manager for root. A valid root-scoped cache is
// loaded immediately, but the filesystem is strictly refreshed by the first
// operation before cached data is used.
func NewManager(root, cacheDir string) (*Manager, error) {
	canonicalRoot, err := canonicalDirectory(root)
	if err != nil {
		return nil, err
	}
	if cacheDir != "" {
		cacheDir, err = filepath.Abs(cacheDir)
		if err != nil {
			return nil, fmt.Errorf("resolve repository graph cache directory: %w", err)
		}
		cacheDir = filepath.Clean(cacheDir)
	}

	manager := &Manager{
		root:        canonicalRoot,
		cacheDir:    cacheDir,
		cachePath:   snapshotCachePath(cacheDir, canonicalRoot),
		refreshGate: make(chan struct{}, 1),
		dwell:       newDwellState(),
	}
	cached, err := loadSnapshot(manager.cachePath, canonicalRoot)
	if err != nil {
		// A cache is an optimization. Corruption, an old schema, or an
		// interrupted external write must not make the repository unusable.
		manager.warnings = []string{"Repository graph cache ignored: " + err.Error()}
	} else {
		if cached != nil {
			// Cache facts are re-extracted once before use, and the materialized
			// graph is always rebuilt from those verified facts.
			cached.Nodes = nil
			cached.Edges = nil
			cached.index = nil
		}
		manager.snapshot = cached
		manager.cacheUnverified = cached != nil
	}
	return manager, nil
}

// CanonicalRoot resolves path and returns the nearest enclosing Git worktree
// root, or the directory itself when path is outside Git.
func CanonicalRoot(path string) (string, error) {
	return canonicalDirectory(path)
}

func canonicalDirectory(path string) (string, error) {
	if path == "" {
		return "", errors.New("repository root is empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve repository root symlinks: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("stat repository root: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("repository root %q is not a directory", path)
	}
	canonical = filepath.Clean(canonical)
	for candidate := canonical; ; candidate = filepath.Dir(candidate) {
		if _, err := os.Stat(filepath.Join(candidate, ".git")); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			break
		}
	}
	return canonical, nil
}

// Root returns the canonical repository root owned by this manager.
func (m *Manager) Root() string {
	return m.root
}

// Close prevents future operations. Published snapshots remain valid values
// held by callers.
func (m *Manager) Close() error {
	m.refreshGate <- struct{}{}
	defer func() { <-m.refreshGate }()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	m.mu.Unlock()
	m.dwell.resetAll()
	return nil
}

// Refresh performs a strict content refresh and returns a caller-owned immutable
// snapshot copy. Model-facing operations use the same refresh path without
// copying the full generation before querying it.
func (m *Manager) Refresh(ctx context.Context) (*Snapshot, BuildReport, error) {
	snapshot, report, err := m.refresh(ctx)
	if err != nil {
		return nil, report, err
	}
	return cloneSnapshot(snapshot), report, nil
}

func (m *Manager) refresh(ctx context.Context) (*Snapshot, BuildReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case m.refreshGate <- struct{}{}:
		defer func() { <-m.refreshGate }()
	case <-ctx.Done():
		return nil, BuildReport{}, ctx.Err()
	}

	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return nil, BuildReport{}, errManagerClosed
	}
	previous := m.snapshot
	cacheUnverified := m.cacheUnverified
	m.mu.RUnlock()

	if err := ctx.Err(); err != nil {
		return nil, BuildReport{}, err
	}
	var previousFacts map[string]FileFacts
	if previous != nil && !cacheUnverified {
		previousFacts = previous.Facts
	}
	discovered, err := discoverFilesWithFacts(ctx, m.root, previousFacts, m.cacheDir)
	if err != nil {
		return nil, BuildReport{}, err
	}

	facts := make(map[string]FileFacts, len(discovered.files))
	report := BuildReport{}
	for _, file := range discovered.files {
		if err := ctx.Err(); err != nil {
			return nil, BuildReport{}, err
		}
		if previous != nil && !cacheUnverified {
			if old, ok := previous.Facts[file.path]; ok && old.Hash == file.hash && old.Size == file.size && old.Language == file.language {
				// File facts belong to the immutable previous generation. Reuse
				// their slices while comparing an unchanged inventory; a changed
				// generation clones the complete map before publication.
				facts[file.path] = old
				for _, warning := range old.Warnings {
					discovered.coverage.Warnings = append(
						discovered.coverage.Warnings,
						file.path+": "+warning,
					)
				}
				report.Reused = append(report.Reused, file.path)
				continue
			}
		}

		extracted := extractFile(filepath.Join(m.root, filepath.FromSlash(file.path)), file.path, file.data, file.hash)
		extracted.Path = file.path
		extracted.Language = file.language
		extracted.Hash = file.hash
		extracted.Size = file.size
		for _, warning := range extracted.Warnings {
			discovered.coverage.Warnings = append(
				discovered.coverage.Warnings,
				file.path+": "+warning,
			)
		}
		if extracted.Generated {
			discovered.coverage.Generated++
			continue
		}
		facts[file.path] = cloneFileFacts(extracted)
		report.Parsed = append(report.Parsed, file.path)
	}
	discovered.coverage.Indexed = len(facts)
	discovered.coverage.Reused = len(report.Reused)
	discovered.coverage.Warnings = boundedCoverageWarnings(discovered.coverage.Warnings)

	changed := previous == nil || !reflect.DeepEqual(previous.Facts, facts) ||
		!equivalentCoverage(previous.Coverage, discovered.coverage)
	if !changed {
		if cacheUnverified {
			if _, err := buildGraphContext(ctx, previous); err != nil {
				return nil, BuildReport{}, err
			}
		}
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return nil, BuildReport{}, errManagerClosed
		}
		m.cacheUnverified = false
		m.mu.Unlock()
		return previous, report, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, BuildReport{}, err
	}

	generation := uint64(1)
	if previous != nil {
		generation = previous.Generation + 1
	}
	next := &Snapshot{
		Schema:     SchemaVersion,
		Root:       m.root,
		Generation: generation,
		BuiltAt:    time.Now().UTC(),
		Facts:      cloneFacts(facts),
		Coverage:   cloneCoverage(discovered.coverage),
	}
	if _, err := buildGraphContext(ctx, next); err != nil {
		return nil, BuildReport{}, err
	}
	persistErr := persistSnapshot(m.cachePath, next)

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, BuildReport{}, errManagerClosed
	}
	m.snapshot = next
	m.cacheUnverified = false
	if persistErr != nil {
		m.warnings = stableStrings(append(
			m.warnings,
			"Repository graph cache unavailable: "+persistErr.Error(),
		))
	}
	m.mu.Unlock()
	return next, report, nil
}

// Sketch returns a deterministic, token-bounded repository silhouette.
func (m *Manager) Sketch(ctx context.Context, maxTokens int) (Result, error) {
	snapshot, _, err := m.refresh(ctx)
	if err != nil {
		return Result{}, err
	}
	if err := usableSnapshot(snapshot); err != nil {
		return Result{}, err
	}
	return m.decorateResult(sketchSnapshot(snapshot, maxTokens)), nil
}

// Focus starts or resets a session-scoped graph cursor around a query.
func (m *Manager) Focus(ctx context.Context, options FocusOptions) (Result, error) {
	snapshot, _, err := m.refresh(ctx)
	if err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if usableErr := usableSnapshot(snapshot); usableErr != nil {
		if !errors.Is(usableErr, errNoUsableFiles) {
			return Result{}, usableErr
		}
		fallback, fallbackErr := m.nativeFocusFallback(ctx, snapshot, options)
		if fallbackErr != nil {
			return Result{}, fallbackErr
		}
		m.dwell.reset(options.SessionID)
		if len(fallback.Hits) > 0 {
			return m.decorateResult(fallback), nil
		}
		result := renderGraphResult(
			"focus", snapshot, nil, nil, 0, normalizedTokenBudget(options.MaxTokens),
			[]string{"No supported files were available; bounded native search found no matches."},
		)
		result.Meta.Scope = options.Scope
		result.Meta.Degraded = true
		return m.decorateResult(result), nil
	}
	result, err := focusSnapshotContext(ctx, snapshot, options, m.dwell)
	if err != nil {
		return Result{}, err
	}
	if result.Meta.Status == "no_matches" {
		fallback, fallbackErr := m.nativeFocusFallback(ctx, snapshot, options)
		if fallbackErr != nil {
			return Result{}, fallbackErr
		}
		if len(fallback.Hits) > 0 {
			m.dwell.reset(options.SessionID)
			result = fallback
		}
	}
	return m.decorateResult(result), nil
}

const (
	maxNativeFocusBytes           = 32 << 20
	maxNativeFocusHits            = 64
	maxGitCommandOutputSize int64 = 8 << 20
	maxGitCommandStderrSize       = 64 << 10
)

func (m *Manager) nativeFocusFallback(ctx context.Context, snapshot *Snapshot, options FocusOptions) (Result, error) {
	query := strings.TrimSpace(options.Query)
	if query == "" || snapshot == nil {
		return Result{}, nil
	}
	kind := NodeSymbol
	for _, character := range query {
		if !unicode.IsLetter(character) && !unicode.IsDigit(character) && character != '_' && character != '.' {
			kind = NodeLiteral
			break
		}
	}
	if options.Scope.Kind != "" {
		kind = NodeKind(options.Scope.Kind)
	}
	needle := strings.ToLower(query)
	hits := make([]Hit, 0, maxNativeFocusHits)
	var scannedBytes int64
	limited := false
	hitLimited := false
	walker := fsext.NewFastGlobWalker(m.root)
	cacheDirectory := filepath.Clean(m.cacheDir)
	walkErr := filepath.WalkDir(m.root, func(absolute string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			if absolute == m.root {
				return walkErr
			}
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
			if absolute != m.root && (repoGraphRuntimeDir(m.root, absolute) ||
				filepath.Clean(absolute) == cacheDirectory || walker.ShouldSkipDir(absolute)) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Clean(filepath.Dir(absolute)) == filepath.Clean(m.root) &&
			(strings.HasPrefix(entry.Name(), cacheFilePrefixStem) || strings.HasPrefix(entry.Name(), ".repograph-")) {
			return nil
		}
		if walker.ShouldSkip(absolute) {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxIndexedFileSize {
			return nil
		}
		filePath, err := filepath.Rel(m.root, absolute)
		if err != nil || filePath == "." || filePath == ".." ||
			strings.HasPrefix(filePath, ".."+string(filepath.Separator)) {
			return nil
		}
		filePath = filepath.ToSlash(filePath)
		language := languageForPath(filePath)
		probe := Node{Kind: kind, Path: filePath, Language: language, Symbol: string(kind)}
		if !queryNodeInScope(probe, options.Scope) {
			return nil
		}
		if info.Size() > maxNativeFocusBytes-scannedBytes {
			limited = true
			return fs.SkipAll
		}
		current, err := os.Lstat(absolute)
		if err != nil || current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() {
			return nil
		}
		file, err := os.Open(absolute)
		if err != nil {
			return nil
		}
		opened, statErr := file.Stat()
		data, readErr := io.ReadAll(io.LimitReader(file, maxIndexedFileSize+1))
		closeErr := file.Close()
		if statErr != nil || readErr != nil || closeErr != nil || !opened.Mode().IsRegular() ||
			!os.SameFile(current, opened) || int64(len(data)) > maxIndexedFileSize {
			return nil
		}
		if int64(len(data)) > maxNativeFocusBytes-scannedBytes {
			limited = true
			return fs.SkipAll
		}
		scannedBytes += int64(len(data))
		if bytes.IndexByte(data, 0) >= 0 {
			return nil
		}
		for lineIndex, line := range strings.Split(string(data), "\n") {
			if !strings.Contains(strings.ToLower(line), needle) {
				continue
			}
			hits = append(hits, Hit{
				NodeID: stableID("native-focus", filePath, itoa(lineIndex+1), needle),
				Name:   query, Kind: kind, Path: filePath, Line: lineIndex + 1,
				EndLine: lineIndex + 1, Language: language,
				Score: 500_000 - int64(lineIndex),
			})
			if len(hits) == maxNativeFocusHits {
				hitLimited = true
				return fs.SkipAll
			}
		}
		return nil
	})
	if walkErr != nil {
		return Result{}, walkErr
	}
	if len(hits) == 0 {
		return Result{}, nil
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if hits[i].Path != hits[j].Path {
			return hits[i].Path < hits[j].Path
		}
		return hits[i].Line < hits[j].Line
	})
	maxTokens := normalizedTokenBudget(options.MaxTokens)
	visible := prefixHits(hits, resultHitLimit(maxTokens))
	warnings := []string{
		"Semantic graph miss; returned bounded native literal matches.",
		"Native fallback results cannot be expanded with repo_dwell; use the suggested reads.",
	}
	if limited {
		warnings = append(warnings, "Native fallback reached its 32 MiB scan limit.")
	}
	if hitLimited {
		warnings = append(warnings, "Native fallback reached its 64-hit limit.")
	}
	result := renderGraphResult(
		"focus", snapshot, visible, suggestedReadWindows(visible), 0, maxTokens, warnings,
	)
	result.Meta.Scope = options.Scope
	result.Meta.Degraded = true
	return result, nil
}

// Dwell advances the most recent focus cursor for one session.
func (m *Manager) Dwell(ctx context.Context, sessionID string, maxTokens int) (Result, error) {
	snapshot, _, err := m.refresh(ctx)
	if err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	cursor, ok := m.dwell.cursor(sessionID)
	if !ok {
		return Result{}, errFocusRequired
	}
	if err := usableSnapshot(snapshot); err != nil {
		return Result{}, err
	}
	if cursor.generation != snapshot.Generation {
		restarted, err := focusSnapshotContext(ctx, snapshot, FocusOptions{
			SessionID: sessionID,
			Query:     cursor.query,
			Scope:     cursor.scope,
			Fresh:     true,
			MaxTokens: maxTokens,
		}, m.dwell)
		if err != nil {
			return Result{}, err
		}
		warnings := append(restarted.Meta.Warnings, "Repository graph changed; the saved focus was refreshed.")
		result := renderGraphResult(
			"dwell", snapshot, restarted.Hits, restarted.SuggestedReads,
			restarted.Meta.Depth, maxTokens, warnings,
		)
		result.Meta.Scope = cursor.scope
		result.Meta.Degraded = true
		return m.decorateResult(result), nil
	}
	result, err := dwellSnapshotContext(ctx, snapshot, sessionID, maxTokens, m.dwell)
	if err != nil {
		return Result{}, err
	}
	return m.decorateResult(result), nil
}

// Impact ranks nodes causally connected to file and symbol seeds.
func (m *Manager) Impact(ctx context.Context, options ImpactOptions) (Result, error) {
	snapshot, _, err := m.refresh(ctx)
	if err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := usableSnapshot(snapshot); err != nil {
		return Result{}, err
	}
	options.Files = append([]string(nil), options.Files...)
	for index, path := range options.Files {
		if rel, ok := m.relativeInputPath(path); ok {
			options.Files[index] = rel
		}
	}
	options.Symbols = append([]string(nil), options.Symbols...)
	explicitSeeds := len(options.Files) > 0 || len(options.Symbols) > 0
	gitRequested := options.Uncommitted || strings.TrimSpace(options.Base) != ""
	if gitRequested {
		available, reason, gitErr := m.gitRepository(ctx)
		if gitErr != nil {
			return Result{}, gitErr
		}
		if !available {
			if !explicitSeeds {
				return Result{}, errors.New(reason)
			}
			options.warnings = append(options.warnings, reason+"; continuing with explicit impact seeds.")
			options.Uncommitted = false
			options.Base = ""
		}
	}
	gitFiles, err := m.impactGitFiles(ctx, options)
	if err != nil {
		return Result{}, err
	}
	options.Files = stablePaths(append(options.Files, gitFiles...))
	historySeeds := append([]string(nil), options.Files...)
	index := ensureGraph(snapshot)
	for _, symbol := range options.Symbols {
		seeds, resolveErr := resolveFocusContext(ctx, index, symbol, Scope{Kind: string(NodeSymbol)})
		if resolveErr != nil {
			return Result{}, resolveErr
		}
		for _, seed := range seeds {
			if seed.node.Path != "" {
				historySeeds = append(historySeeds, seed.node.Path)
			}
		}
	}
	if len(historySeeds) > 0 {
		options.cochanges, err = m.gitCochangeScores(ctx, stablePaths(historySeeds), snapshot)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return Result{}, ctxErr
			}
			options.warnings = append(options.warnings, "Git co-change history unavailable: "+err.Error())
		}
	}
	result, err := impactSnapshotContext(ctx, snapshot, options)
	if err != nil {
		return Result{}, err
	}
	if len(options.warnings) > 0 {
		result.Meta.Degraded = true
	}
	return m.decorateResult(result), nil
}

func (m *Manager) gitRepository(ctx context.Context) (bool, string, error) {
	command := exec.CommandContext(ctx, "git", "-C", m.root, "rev-parse", "--is-inside-work-tree")
	output, stderr, err := boundedCommandOutput(ctx, command, maxGitCommandOutputSize)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return false, "", ctxErr
		}
		reason := safeLine(stderr)
		if reason == "" {
			reason = err.Error()
		}
		return false, "Git repository unavailable: " + reason, nil
	}
	if strings.TrimSpace(string(output)) != "true" {
		return false, "Git repository unavailable", nil
	}
	return true, "", nil
}

func (m *Manager) impactGitFiles(ctx context.Context, options ImpactOptions) ([]string, error) {
	var files []string
	if options.Uncommitted {
		for _, args := range [][]string{
			{"diff", "--name-only", "-z", "--"},
			{"diff", "--cached", "--name-only", "-z", "--"},
			{"ls-files", "--others", "--exclude-standard", "-z", "--"},
		} {
			paths, err := m.gitPathList(ctx, args...)
			if err != nil {
				return nil, err
			}
			files = append(files, paths...)
		}
	}
	if base := strings.TrimSpace(options.Base); base != "" {
		if strings.HasPrefix(base, "-") {
			return nil, errors.New("git base revision must not start with a dash")
		}
		paths, err := m.gitPathList(ctx, "diff", "--name-only", "-z", base+"...HEAD", "--")
		if err != nil {
			return nil, err
		}
		files = append(files, paths...)
	}
	return stablePaths(files), nil
}

func (m *Manager) gitPathList(ctx context.Context, args ...string) ([]string, error) {
	commandArgs := append([]string{"-C", m.root}, args...)
	command := exec.CommandContext(ctx, "git", commandArgs...)
	output, stderr, err := boundedCommandOutput(ctx, command, maxGitCommandOutputSize)
	if err != nil {
		return nil, boundedCommandError("inspect Git changes", err, stderr)
	}
	parts := bytes.Split(output, []byte{0})
	paths := make([]string, 0, len(parts))
	for _, part := range parts {
		path := filepath.ToSlash(string(part))
		if path == "" {
			continue
		}
		if relative, ok := m.relativeInputPath(path); ok {
			paths = append(paths, relative)
		}
	}
	return stablePaths(paths), nil
}

type boundedStderr struct {
	buffer    bytes.Buffer
	remaining int
}

func (writer *boundedStderr) Write(data []byte) (int, error) {
	length := len(data)
	if writer.remaining > 0 {
		kept := min(writer.remaining, length)
		_, _ = writer.buffer.Write(data[:kept])
		writer.remaining -= kept
	}
	return length, nil
}

func (writer *boundedStderr) String() string {
	return writer.buffer.String()
}

func boundedCommandOutput(ctx context.Context, command *exec.Cmd, maxBytes int64) ([]byte, string, error) {
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, "", fmt.Errorf("open command output: %w", err)
	}
	stderr := &boundedStderr{remaining: maxGitCommandStderrSize}
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		return nil, stderr.String(), fmt.Errorf("start command: %w", err)
	}
	output, readErr := io.ReadAll(io.LimitReader(stdout, maxBytes+1))
	oversized := int64(len(output)) > maxBytes
	if readErr != nil || oversized {
		_ = command.Process.Kill()
	}
	waitErr := command.Wait()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, stderr.String(), ctxErr
	}
	if readErr != nil {
		return nil, stderr.String(), fmt.Errorf("read command output: %w", readErr)
	}
	if oversized {
		return nil, stderr.String(), fmt.Errorf("command output exceeds %d bytes", maxBytes)
	}
	if waitErr != nil {
		return nil, stderr.String(), waitErr
	}
	return output, stderr.String(), nil
}

func boundedCommandError(operation string, err error, stderr string) error {
	stderr = safeLine(stderr)
	if stderr == "" {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return fmt.Errorf("%s: %w: %s", operation, err, stderr)
}

func (m *Manager) gitCochangeScores(ctx context.Context, seeds []string, snapshot *Snapshot) (map[string]int64, error) {
	available, reason, err := m.gitRepository(ctx)
	if err != nil {
		return nil, err
	}
	if !available {
		if reason == "" {
			reason = "Git repository unavailable"
		}
		return nil, errors.New(reason)
	}
	command := exec.CommandContext(
		ctx,
		"git", "-C", m.root, "log", "-n", "200", "--format=%x1e", "--name-only", "-z", "--",
	)
	output, stderr, err := boundedCommandOutput(ctx, command, maxGitCommandOutputSize)
	if err != nil {
		return nil, boundedCommandError("inspect Git history", err, stderr)
	}
	seedSet := make(map[string]struct{}, len(seeds))
	for _, seed := range seeds {
		seedSet[cleanGraphPath(seed)] = struct{}{}
	}
	scores := make(map[string]int64)
	commits := bytes.Split(output, []byte{0x1e})
	commitIndex := 0
	for _, commit := range commits {
		if len(commit) == 0 {
			continue
		}
		commit = bytes.TrimPrefix(commit, []byte{0})
		commit = bytes.TrimPrefix(commit, []byte{'\n'})
		parts := bytes.Split(commit, []byte{0})
		paths := make([]string, 0, len(parts))
		touchesSeed := false
		for _, part := range parts {
			if len(part) == 0 {
				continue
			}
			filePath := filepath.ToSlash(string(part))
			relative, ok := m.relativeInputPath(filePath)
			if !ok {
				continue
			}
			paths = append(paths, relative)
			_, touches := seedSet[relative]
			touchesSeed = touchesSeed || touches
		}
		if !touchesSeed || len(paths) > 100 {
			commitIndex++
			continue
		}
		weight := int64(100_000 / (1 + commitIndex/10))
		for _, filePath := range paths {
			if _, seed := seedSet[filePath]; seed {
				continue
			}
			if snapshot == nil {
				continue
			}
			if _, indexed := snapshot.Facts[filePath]; indexed {
				scores[filePath] += weight
			}
		}
		commitIndex++
	}

	type scoredPath struct {
		path  string
		score int64
	}
	ranked := make([]scoredPath, 0, len(scores))
	for filePath, score := range scores {
		ranked = append(ranked, scoredPath{path: filePath, score: score})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].path < ranked[j].path
	})
	if len(ranked) > 64 {
		ranked = ranked[:64]
	}
	bounded := make(map[string]int64, len(ranked))
	for _, candidate := range ranked {
		bounded[candidate.path] = candidate.score
	}
	return bounded, nil
}

func stablePaths(values []string) []string {
	unique := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = filepath.ToSlash(value)
		if value == "" {
			continue
		}
		if _, exists := unique[value]; exists {
			continue
		}
		unique[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func (m *Manager) relativeInputPath(path string) (string, bool) {
	if path == "" {
		return "", false
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(m.root, filepath.FromSlash(path))
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(m.root, absolute)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func usableSnapshot(snapshot *Snapshot) error {
	if snapshot == nil || len(snapshot.Facts) == 0 {
		return errNoUsableFiles
	}
	return nil
}

func (m *Manager) decorateResult(result Result) Result {
	m.mu.RLock()
	warnings := append([]string(nil), m.warnings...)
	m.mu.RUnlock()
	if len(warnings) == 0 {
		return result
	}
	result.Meta.Warnings = stableStrings(append(result.Meta.Warnings, warnings...))
	result.Meta.Degraded = true
	return result
}

const (
	maxCoverageWarnings     = 128
	maxCoverageWarningBytes = 512
)

func boundedCoverageWarnings(values []string) []string {
	unique := make(map[string]struct{}, min(len(values), maxCoverageWarnings))
	warnings := make([]string, 0, min(len(values), maxCoverageWarnings))
	for _, value := range values {
		value = truncateUTF8Bytes(safeLine(value), maxCoverageWarningBytes)
		if value == "" {
			continue
		}
		if _, exists := unique[value]; exists {
			continue
		}
		unique[value] = struct{}{}
		warnings = append(warnings, value)
	}
	if len(warnings) == 0 {
		return nil
	}
	sort.Strings(warnings)
	if len(warnings) <= maxCoverageWarnings {
		return warnings
	}
	omitted := len(warnings) - (maxCoverageWarnings - 1)
	warnings = append(
		warnings[:maxCoverageWarnings-1],
		fmt.Sprintf("%d additional repository graph warnings omitted", omitted),
	)
	return warnings
}

func equivalentCoverage(left, right Coverage) bool {
	left.Reused = 0
	right.Reused = 0
	return reflect.DeepEqual(left, right)
}

func cloneSnapshot(snapshot *Snapshot) *Snapshot {
	if snapshot == nil {
		return nil
	}
	clone := *snapshot
	clone.index = nil
	clone.Facts = cloneFacts(snapshot.Facts)
	clone.Nodes = append([]Node(nil), snapshot.Nodes...)
	clone.Edges = append([]Edge(nil), snapshot.Edges...)
	clone.Coverage = cloneCoverage(snapshot.Coverage)
	return &clone
}

func cloneFacts(facts map[string]FileFacts) map[string]FileFacts {
	cloned := make(map[string]FileFacts, len(facts))
	for path, fact := range facts {
		cloned[path] = cloneFileFacts(fact)
	}
	return cloned
}

func cloneFileFacts(facts FileFacts) FileFacts {
	facts.Symbols = append([]SymbolFact(nil), facts.Symbols...)
	facts.Imports = append([]ImportFact(nil), facts.Imports...)
	facts.Calls = append([]CallFact(nil), facts.Calls...)
	facts.Literals = append([]LiteralFact(nil), facts.Literals...)
	facts.Routes = append([]RouteFact(nil), facts.Routes...)
	facts.Inheritance = append([]InheritanceFact(nil), facts.Inheritance...)
	facts.Warnings = append([]string(nil), facts.Warnings...)
	return facts
}

func cloneCoverage(coverage Coverage) Coverage {
	coverage.Warnings = append([]string(nil), coverage.Warnings...)
	return coverage
}
