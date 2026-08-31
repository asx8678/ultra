package repograph

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBoundedCoverageWarnings(t *testing.T) {
	t.Parallel()
	values := make([]string, 0, 200)
	for index := range 200 {
		values = append(values, itoa(index)+"-"+strings.Repeat("x", 600))
	}
	values = append(values, "!unsafe\x1b[31m\u202esequence")

	warnings := boundedCoverageWarnings(values)
	require.Len(t, warnings, maxCoverageWarnings)
	require.Contains(t, warnings[len(warnings)-1], "additional repository graph warnings omitted")
	for _, warning := range warnings {
		require.LessOrEqual(t, len(warning), maxCoverageWarningBytes)
		require.NotContains(t, warning, "\x1b")
		require.NotContains(t, warning, "\u202e")
	}
}

func TestBoundedCommandOutput(t *testing.T) {
	t.Parallel()

	t.Run("accepts bounded output", func(t *testing.T) {
		command := boundedOutputHelperCommand(t.Context(), "small")
		output, stderr, err := boundedCommandOutput(t.Context(), command, 16)
		require.NoError(t, err)
		require.Equal(t, "ok", string(output))
		require.Empty(t, stderr)
	})

	t.Run("rejects oversized output without waiting for the writer", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		command := boundedOutputHelperCommand(ctx, "large")
		output, _, err := boundedCommandOutput(ctx, command, 1024)
		require.ErrorContains(t, err, "command output exceeds 1024 bytes")
		require.Nil(t, output)
		require.NoError(t, ctx.Err())
	})

	t.Run("bounds stderr", func(t *testing.T) {
		command := boundedOutputHelperCommand(t.Context(), "stderr")
		_, stderr, err := boundedCommandOutput(t.Context(), command, 16)
		require.Error(t, err)
		require.Len(t, stderr, maxGitCommandStderrSize)
	})
}

func boundedOutputHelperCommand(ctx context.Context, mode string) *exec.Cmd {
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBoundedCommandOutputHelper$")
	command.Env = append(os.Environ(), "ULTRA_REPOGRAPH_OUTPUT_HELPER="+mode)
	return command
}

func TestBoundedCommandOutputHelper(t *testing.T) {
	mode := os.Getenv("ULTRA_REPOGRAPH_OUTPUT_HELPER")
	if mode == "" {
		return
	}
	switch mode {
	case "small":
		_, _ = os.Stdout.WriteString("ok")
	case "large":
		_, _ = os.Stdout.WriteString(strings.Repeat("x", 1<<20))
	case "stderr":
		_, _ = os.Stderr.WriteString(strings.Repeat("e", maxGitCommandStderrSize*2))
		os.Exit(2)
	default:
		os.Exit(3)
	}
	os.Exit(0)
}

func TestCanonicalRootFindsEnclosingGitWorktree(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
	nested := filepath.Join(root, "internal", "service")
	require.NoError(t, os.MkdirAll(nested, 0o755))

	canonical, err := CanonicalRoot(nested)
	require.NoError(t, err)
	require.Equal(t, root, canonical)
}

func TestManagerRefreshIsIncrementalAndSnapshotsAreImmutable(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cacheDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("package sample\nfunc Alpha() {}\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "b.py"), []byte("def beta():\n    return 1\n"), 0o644))

	manager, err := NewManager(root, cacheDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })

	first, report, err := manager.Refresh(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"a.go", "b.py"}, report.Parsed)
	require.Empty(t, report.Reused)
	require.Len(t, first.Facts, 2)

	// Mutating a caller-owned snapshot must not mutate the published state.
	fact := first.Facts["a.go"]
	fact.Hash = "caller mutation"
	first.Facts["a.go"] = fact
	first.Nodes = nil
	first.Coverage.Warnings = append(first.Coverage.Warnings, "caller mutation")

	second, report, err := manager.Refresh(context.Background())
	require.NoError(t, err)
	require.Empty(t, report.Parsed)
	require.Equal(t, []string{"a.go", "b.py"}, report.Reused)
	require.Equal(t, uint64(1), second.Generation)
	require.NotEqual(t, "caller mutation", second.Facts["a.go"].Hash)
	require.NotEmpty(t, second.Nodes)
	require.NotContains(t, second.Coverage.Warnings, "caller mutation")

	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("package sample\nfunc AlphaChanged() {}\n"), 0o644))
	require.NoError(t, os.Remove(filepath.Join(root, "b.py")))
	third, report, err := manager.Refresh(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"a.go"}, report.Parsed)
	require.Empty(t, report.Reused)
	require.Equal(t, uint64(2), third.Generation)
	require.Contains(t, third.Facts, "a.go")
	require.NotContains(t, third.Facts, "b.py")
}

func TestManagerOperationsStrictlyRefreshAndAreConcurrencySafe(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc Before() {}\n"), 0o644))

	manager, err := NewManager(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })

	before, err := manager.Sketch(context.Background(), 256)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc After() {}\n"), 0o644))
	after, err := manager.Focus(context.Background(), FocusOptions{SessionID: "one", Query: "After", MaxTokens: 256})
	require.NoError(t, err)
	require.Greater(t, after.Meta.Generation, before.Meta.Generation)

	_, err = manager.Focus(context.Background(), FocusOptions{SessionID: "two", Query: "main", MaxTokens: 256})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc After() {}\nfunc Later() { After() }\n"), 0o644))
	restarted, err := manager.Dwell(context.Background(), "one", 256)
	require.NoError(t, err)
	require.True(t, restarted.Meta.Degraded)
	require.Contains(t, restarted.Meta.Warnings, "Repository graph changed; the saved focus was refreshed.")
	_, err = manager.Dwell(context.Background(), "missing", 256)
	require.ErrorIs(t, err, errFocusRequired)

	var wait sync.WaitGroup
	errors := make(chan error, 8)
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, sketchErr := manager.Sketch(context.Background(), 128)
			errors <- sketchErr
		}()
	}
	wait.Wait()
	close(errors)
	for sketchErr := range errors {
		require.NoError(t, sketchErr)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = manager.Refresh(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

func TestManagerStrictHashDetectsMetadataPreservingChange(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\nfunc Alpha() {}\n"), 0o644))
	original, err := os.Stat(path)
	require.NoError(t, err)

	manager, err := NewManager(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	before, err := manager.Sketch(t.Context(), 256)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(path, []byte("package main\nfunc Bravo() {}\n"), 0o644))
	require.NoError(t, os.Chtimes(path, original.ModTime(), original.ModTime()))
	result, err := manager.Focus(t.Context(), FocusOptions{
		SessionID: "strict-hash", Query: "Bravo", Fresh: true, MaxTokens: 256,
	})
	require.NoError(t, err)
	require.Greater(t, result.Meta.Generation, before.Meta.Generation)
	require.True(t, hasHitWhere(result, func(hit Hit) bool { return hit.Name == "Bravo" }))
}

func TestManagerRefreshWaitHonorsCancellation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644))
	manager, err := NewManager(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })

	manager.refreshGate <- struct{}{}
	defer func() { <-manager.refreshGate }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, _, err = manager.Refresh(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(started), 500*time.Millisecond)
}

func TestManagerDiscoveryCoverageAndIgnoreRules(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored.go\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "included.go"), []byte("package sample\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "ignored.go"), []byte("package ignored\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "binary.py"), []byte{'x', 0, 'y'}, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "generated.go"), []byte("// Code generated by a test. DO NOT EDIT.\npackage sample\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "large.js"), make([]byte, maxIndexedFileSize+1), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "unsupported.dat"), []byte("data"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".pi", "fabric"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".pi", "fabric", "state.json"), []byte(`{"runtime":true}`), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "autoresearch", "run"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "autoresearch", "run", "experiment.go"), []byte("package experiment\n"), 0o644))

	outside := filepath.Join(t.TempDir(), "outside.go")
	require.NoError(t, os.WriteFile(outside, []byte("package outside\n"), 0o644))
	if err := os.Symlink(outside, filepath.Join(root, "linked.go")); err != nil {
		t.Skipf("Symlinks unavailable: %v", err)
	}

	manager, err := NewManager(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	snapshot, _, err := manager.Refresh(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, snapshot.Coverage.Indexed)
	require.Contains(t, snapshot.Facts, "included.go")
	require.NotContains(t, snapshot.Facts, "ignored.go")
	require.NotContains(t, snapshot.Facts, "linked.go")
	require.NotContains(t, snapshot.Facts, ".pi/fabric/state.json")
	require.NotContains(t, snapshot.Facts, "autoresearch/run/experiment.go")
	require.Equal(t, 1, snapshot.Coverage.Generated)
	require.Equal(t, 1, snapshot.Coverage.Oversized)
	require.GreaterOrEqual(t, snapshot.Coverage.Unsupported, 2)
}

func TestManagerFocusFallsBackToBoundedNativeSearch(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "main.go"),
		[]byte("package sample\n// needle only in prose\nfunc Main() {}\n"),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "notes.txt"),
		[]byte("unsupported format has a second exact needle\n"),
		0o644,
	))
	manager, err := NewManager(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })

	result, err := manager.Focus(t.Context(), FocusOptions{
		SessionID: "fallback", Query: "needle only in prose", Fresh: true, MaxTokens: 512,
	})
	require.NoError(t, err)
	require.NotEmpty(t, result.Hits)
	require.Equal(t, "main.go", result.Hits[0].Path)
	require.Equal(t, 2, result.Hits[0].Line)
	require.True(t, result.Meta.Degraded)
	require.Contains(t, result.Meta.Warnings, "Semantic graph miss; returned bounded native literal matches.")

	unsupported, err := manager.Focus(t.Context(), FocusOptions{
		SessionID: "fallback-unsupported", Query: "second exact needle", Fresh: true, MaxTokens: 512,
	})
	require.NoError(t, err)
	require.NotEmpty(t, unsupported.Hits)
	require.Equal(t, "notes.txt", unsupported.Hits[0].Path)
	require.Empty(t, unsupported.Hits[0].Language)
	require.True(t, unsupported.Meta.Degraded)

	unsupportedRoot := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(unsupportedRoot, "README.txt"),
		[]byte("native search works without an indexed language\n"),
		0o644,
	))
	unsupportedManager, err := NewManager(unsupportedRoot, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, unsupportedManager.Close()) })
	onlyNative, err := unsupportedManager.Focus(t.Context(), FocusOptions{
		SessionID: "only-native", Query: "without an indexed language", Fresh: true, MaxTokens: 512,
	})
	require.NoError(t, err)
	require.NotEmpty(t, onlyNative.Hits)
	require.Equal(t, "README.txt", onlyNative.Hits[0].Path)
	require.True(t, onlyNative.Meta.Degraded)
	_, err = unsupportedManager.Dwell(t.Context(), "only-native", 512)
	require.ErrorIs(t, err, errFocusRequired)

	missing, err := unsupportedManager.Focus(t.Context(), FocusOptions{
		SessionID: "only-native-missing", Query: "not present 91f6", Fresh: true, MaxTokens: 512,
	})
	require.NoError(t, err)
	require.Empty(t, missing.Hits)
	require.Equal(t, "no_matches", missing.Meta.Status)
	require.True(t, missing.Meta.Degraded)
}

func TestManagerImpactDegradesUnavailableGitWithExplicitSeeds(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "main.go"), []byte("package sample\nfunc Alpha() {}\n"), 0o644))
	manager, err := NewManager(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })

	result, err := manager.Impact(t.Context(), ImpactOptions{
		Files: []string{"main.go"}, Uncommitted: true, MaxTokens: 256,
	})
	require.NoError(t, err)
	require.NotEmpty(t, result.Meta.Warnings)
	_, err = manager.Impact(t.Context(), ImpactOptions{Uncommitted: true, MaxTokens: 256})
	require.Error(t, err)
}

func TestManagerImpactReportsUnavailableCochangeHistory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "service.go"),
		[]byte("package service\nfunc Handle() {}\n"),
		0o644,
	))
	manager, err := NewManager(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })

	result, err := manager.Impact(t.Context(), ImpactOptions{Files: []string{"service.go"}, MaxTokens: 256})
	require.NoError(t, err)
	require.True(t, result.Meta.Degraded)
	require.Contains(t, strings.Join(result.Meta.Warnings, "\n"), "Git co-change history unavailable")
}

func TestManagerImpactUsesBoundedGitCochangeHistory(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is unavailable")
	}
	root := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		output, err := command.CombinedOutput()
		require.NoError(t, err, string(output))
	}
	runGit("init", "-q")
	require.NoError(t, os.WriteFile(filepath.Join(root, "seed.go"), []byte("package sample\nfunc Seed() {}\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "historical.md"), []byte("# Historical partner\n"), 0o644))
	runGit("add", ".")
	runGit("-c", "user.name=Ultra Test", "-c", "user.email=ultra@example.invalid", "commit", "-qm", "cochange")

	manager, err := NewManager(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	result, err := manager.Impact(t.Context(), ImpactOptions{Files: []string{"seed.go"}, MaxTokens: 512})
	require.NoError(t, err)
	found := false
	for _, hit := range result.Hits {
		if hit.Path == "historical.md" && hit.Relation == EdgeCoChanges {
			require.Equal(t, "historical", hit.Direction)
			found = true
		}
	}
	require.True(t, found)
}

func TestManagerImpactDiscoversUncommittedGitFiles(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is unavailable")
	}
	root := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		output, err := command.CombinedOutput()
		require.NoError(t, err, string(output))
	}
	runGit("init", "-q")
	require.NoError(t, os.WriteFile(filepath.Join(root, "tracked.go"), []byte("package sample\nfunc Alpha() int { return 1 }\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "consumer.go"), []byte("package sample\nfunc Beta() int { return Alpha() }\n"), 0o644))
	runGit("add", ".")
	runGit("-c", "user.name=Ultra Test", "-c", "user.email=ultra@example.invalid", "commit", "-qm", "initial")

	manager, err := NewManager(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	_, _, err = manager.Refresh(t.Context())
	require.NoError(t, err)
	_, err = manager.Impact(t.Context(), ImpactOptions{Files: []string{"tracked.go"}, Base: "missing-revision", MaxTokens: 256})
	require.Error(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "tracked.go"), []byte("package sample\nfunc Alpha() int { return 2 }\n"), 0o644))

	result, err := manager.Impact(t.Context(), ImpactOptions{Uncommitted: true, MaxTokens: 256})
	require.NoError(t, err)
	require.Equal(t, "impact", result.Meta.Operation)
	foundConsumer := false
	for _, hit := range result.Hits {
		foundConsumer = foundConsumer || hit.Path == "consumer.go"
	}
	require.True(t, foundConsumer)
}
