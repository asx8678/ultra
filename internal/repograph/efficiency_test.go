package repograph

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWarmFocusRepositoryEfficiencyGate(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	manager, err := NewManager(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })

	options := FocusOptions{
		SessionID: "efficiency-gate", Query: "buildToolsAt", Fresh: true, MaxTokens: 1024,
	}
	coldStart := time.Now()
	initial, err := manager.Focus(t.Context(), options)
	require.NoError(t, err)
	cold := time.Since(coldStart)
	indexed := initial.Meta.Coverage.Indexed
	coldLimit := max(8*time.Second, time.Duration(indexed)*10*time.Millisecond)
	require.Less(t, cold, coldLimit)

	warm := make([]time.Duration, 0, 5)
	for range 5 {
		started := time.Now()
		_, err = manager.Focus(t.Context(), options)
		require.NoError(t, err)
		warm = append(warm, time.Since(started))
	}
	warmP95 := percentile95(warm)
	warmLimit := max(time.Second, time.Duration(indexed)*time.Millisecond)
	require.Less(t, warmP95, warmLimit)

	var allocationErr error
	allocations := testing.AllocsPerRun(3, func() {
		_, allocationErr = manager.Focus(t.Context(), options)
	})
	require.NoError(t, allocationErr)
	allocationLimit := max(150_000.0, float64(indexed)*200)
	require.Less(t, allocations, allocationLimit)
	t.Logf(
		"Warm repository focus: files=%d cold=%s/%s p95=%s/%s allocations=%.0f/%.0f",
		indexed, cold, coldLimit, warmP95, warmLimit, allocations, allocationLimit,
	)
}

func BenchmarkWarmFocusRepository(b *testing.B) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		b.Fatal(err)
	}
	manager, err := NewManager(root, b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := manager.Close(); err != nil {
			b.Error(err)
		}
	})
	coldStart := time.Now()
	if _, err := manager.Focus(b.Context(), FocusOptions{
		SessionID: "benchmark", Query: "buildToolsAt", MaxTokens: 1024,
	}); err != nil {
		b.Fatal(err)
	}
	coldDuration := time.Since(coldStart)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := manager.Focus(b.Context(), FocusOptions{
			SessionID: "benchmark", Query: "buildToolsAt", MaxTokens: 1024,
		}); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(coldDuration.Nanoseconds()), "cold_ns")
}
