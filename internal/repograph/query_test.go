package repograph

import (
	"context"
	"fmt"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestQueryTermsAreBounded(t *testing.T) {
	t.Parallel()
	query := ""
	for index := range maxFocusQueryTerms * 2 {
		query += fmt.Sprintf("term%d ", index)
	}
	terms := queryTerms(query)
	require.Len(t, terms, maxFocusQueryTerms)
	require.Equal(t, "term0", terms[0])
	require.Equal(t, "term15", terms[len(terms)-1])
}

func TestFocusResolutionExpansionAndDwell(t *testing.T) {
	t.Parallel()

	snapshot := queryTestSnapshot()
	state := newDwellState()
	for _, query := range []string{"ProcessOrder", "process_order", "Process", "ProcesOrder"} {
		result := focusSnapshot(snapshot, FocusOptions{SessionID: query, Query: query, Fresh: true, MaxTokens: 240}, state)
		require.NotEmpty(t, result.Hits, query)
		require.Equal(t, "ProcessOrder", result.Hits[0].Name, query)
	}
	route := focusSnapshot(snapshot, FocusOptions{SessionID: "route", Query: "POST /orders/{orderID}", Fresh: true, MaxTokens: 240}, state)
	require.NotEmpty(t, route.Hits)
	require.Equal(t, "POST /orders/{*}", route.Hits[0].Name)

	// A small page makes progressive disclosure observable and deterministic.
	first := focusSnapshot(snapshot, FocusOptions{SessionID: "progressive", Query: "ProcessOrder", Fresh: true, MaxTokens: 64}, state)
	require.NotEmpty(t, first.Hits)
	second := dwellSnapshot(snapshot, "progressive", 64, state)
	require.NotEmpty(t, second.Hits)
	disclosed := make(map[string]struct{})
	for _, hit := range first.Hits {
		disclosed[hit.NodeID] = struct{}{}
	}
	for _, hit := range second.Hits {
		require.NotContains(t, disclosed, hit.NodeID)
		disclosed[hit.NodeID] = struct{}{}
	}
	third := dwellSnapshot(snapshot, "progressive", 64, state)
	for _, hit := range third.Hits {
		require.NotContains(t, disclosed, hit.NodeID)
	}

	state.reset("progressive")
	missing := dwellSnapshot(snapshot, "progressive", 64, state)
	require.Empty(t, missing.Hits)
	require.NotEmpty(t, missing.Meta.Warnings)
}

func TestFocusFreshControlsProgressiveDisclosure(t *testing.T) {
	t.Parallel()
	snapshot := queryTestSnapshot()
	state := newDwellState()
	options := FocusOptions{SessionID: "fresh", Query: "ProcessOrder", Fresh: true, MaxTokens: 64}
	first := focusSnapshot(snapshot, options, state)
	require.NotEmpty(t, first.Hits)

	options.Fresh = false
	repeated := focusSnapshot(snapshot, options, state)
	for _, left := range first.Hits {
		for _, right := range repeated.Hits {
			require.NotEqual(t, left.NodeID, right.NodeID)
		}
	}

	options.Fresh = true
	reset := focusSnapshot(snapshot, options, state)
	require.NotEmpty(t, reset.Hits)
	require.Equal(t, first.Hits[0].NodeID, reset.Hits[0].NodeID)
}

func TestDwellWidensSemanticNeighborhood(t *testing.T) {
	t.Parallel()
	nodes := make([]Node, 0, 6)
	edges := make([]Edge, 0, 5)
	for index, name := range []string{"A", "B", "C", "D", "E", "F"} {
		id := fmt.Sprintf("node-%d", index)
		nodes = append(nodes, Node{ID: id, Kind: NodeSymbol, Name: name, Path: "chain.go", Line: index + 1})
		if index > 0 {
			edges = append(edges, Edge{From: fmt.Sprintf("node-%d", index-1), To: id, Kind: EdgeCalls, Weight: weightCalls})
		}
	}
	snapshot := &Snapshot{Root: "/repo", Generation: 1, Facts: map[string]FileFacts{"chain.go": {Path: "chain.go"}}, Nodes: nodes, Edges: edges}
	snapshot.index = newGraphIndex(snapshot)
	state := newDwellState()
	focus := focusSnapshot(snapshot, FocusOptions{SessionID: "wide", Query: "A", Fresh: true, MaxTokens: 2000}, state)
	require.NotEmpty(t, focus.Hits)
	dwell := dwellSnapshot(snapshot, "wide", 2000, state)
	require.NotEmpty(t, dwell.Hits)
	require.Equal(t, maxFocusDepth+1, dwell.Meta.Depth)
	require.Contains(t, []string{"E", "F"}, dwell.Hits[0].Name)
}

func TestDwellStateIsBoundedExpiresAndClearsFailedFocus(t *testing.T) {
	t.Parallel()
	snapshot := queryTestSnapshot()
	state := newDwellState()
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	state.now = func() time.Time { return now }

	for index := range maxDwellSessions + 8 {
		focusSnapshot(snapshot, FocusOptions{
			SessionID: fmt.Sprintf("session-%03d", index),
			Query:     "ProcessOrder",
			MaxTokens: 256,
		}, state)
	}
	state.mu.Lock()
	require.Len(t, state.sessions, maxDwellSessions)
	state.mu.Unlock()
	_, oldestExists := state.cursor("session-000")
	require.False(t, oldestExists)
	_, newestExists := state.cursor(fmt.Sprintf("session-%03d", maxDwellSessions+7))
	require.True(t, newestExists)

	now = now.Add(dwellSessionTTL)
	_, newestExists = state.cursor(fmt.Sprintf("session-%03d", maxDwellSessions+7))
	require.False(t, newestExists)

	focusSnapshot(snapshot, FocusOptions{SessionID: "cleared", Query: "ProcessOrder", MaxTokens: 256}, state)
	_, exists := state.cursor("cleared")
	require.True(t, exists)
	focusSnapshot(snapshot, FocusOptions{SessionID: "cleared", Query: "definitely-missing", MaxTokens: 256}, state)
	_, exists = state.cursor("cleared")
	require.False(t, exists)
}

func TestGraphExpansionHonorsCancellation(t *testing.T) {
	t.Parallel()

	snapshot := queryTestSnapshot()
	index := ensureGraph(snapshot)
	seeds := resolveFocus(index, "ProcessOrder", Scope{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := resolveFocusContext(ctx, index, "ProcessOrder", Scope{})
	require.ErrorIs(t, err, context.Canceled)
	_, err = resolveImpactSeedsContext(ctx, index, nil, []string{"ProcessOrder"}, nil)
	require.ErrorIs(t, err, context.Canceled)
	_, err = expandFocusContext(ctx, index, seeds, Scope{}, maxFocusDepth)
	require.ErrorIs(t, err, context.Canceled)
	_, err = expandImpactContext(ctx, index, seeds, maxImpactDepth)
	require.ErrorIs(t, err, context.Canceled)
}

func TestImpactRanksCallersTestsAndSharedConfiguration(t *testing.T) {
	t.Parallel()

	snapshot := queryTestSnapshot()
	result := impactSnapshot(snapshot, ImpactOptions{Symbols: []string{"ProcessOrder"}, MaxTokens: 500})
	require.NotEmpty(t, result.Hits)

	positions := make(map[string]int)
	for index, hit := range result.Hits {
		positions[hit.Name] = index
	}
	require.Contains(t, positions, "HandleOrder")
	require.Contains(t, positions, "TestProcessOrder")
	require.Contains(t, positions, "API_TOKEN")
	require.Less(t, positions["TestProcessOrder"], positions["API_TOKEN"])

	reads := suggestedReadWindows(result.Hits)
	require.NotEmpty(t, reads)
	for _, window := range reads {
		require.Equal(t, window.StartLine-1, window.Offset)
		require.Equal(t, window.EndLine-window.StartLine+1, window.Limit)
		require.LessOrEqual(t, window.Limit, 200)
	}
}

func TestImpactExcludesEveryNodeInChangedFile(t *testing.T) {
	t.Parallel()

	snapshot := queryTestSnapshot()
	result := impactSnapshot(snapshot, ImpactOptions{Files: []string{"domain/order.go"}, MaxTokens: 500})
	require.NotEmpty(t, result.Hits)
	for _, hit := range result.Hits {
		require.NotEqual(t, "domain/order.go", hit.Path)
	}
}

func TestScopeUsesPathBoundaries(t *testing.T) {
	t.Parallel()

	require.True(t, queryNodeInScope(Node{Path: "api/handler.go"}, Scope{Path: "api"}))
	require.False(t, queryNodeInScope(Node{Path: "notapi/leak.go"}, Scope{Path: "api"}))
}

func TestRenderedResultsExposeOnlyVisibleHits(t *testing.T) {
	t.Parallel()

	hits := make([]Hit, 0, 20)
	for index := range 20 {
		hits = append(hits, Hit{
			NodeID: fmt.Sprintf("node-%d", index),
			Name:   fmt.Sprintf("VeryLongSemanticResultName%03d", index),
			Kind:   NodeSymbol,
			Path:   "some/deep/repository/path/with/context.go",
			Line:   index + 1,
			Score:  int64(1000 - index),
		})
	}
	result := renderGraphResult("focus", queryTestSnapshot(), hits, suggestedReadWindows(hits), 3, 128, nil)
	require.True(t, result.Meta.Truncated)
	require.Less(t, len(result.Hits), len(hits))
	for _, hit := range result.Hits {
		require.Contains(t, result.Text, hit.Name)
	}
	visiblePaths := make(map[string]struct{})
	for _, hit := range result.Hits {
		visiblePaths[hit.Path] = struct{}{}
	}
	for _, read := range result.SuggestedReads {
		require.Contains(t, result.Text, read.Path)
		require.Contains(t, visiblePaths, read.Path)
	}
}

func TestSketchAlwaysRanksProductionBeforeTests(t *testing.T) {
	t.Parallel()

	snapshot := &Snapshot{
		Root: "/repo", Generation: 1,
		Facts: map[string]FileFacts{
			"aaa_test.go": {Path: "aaa_test.go"},
			"zzz.go":      {Path: "zzz.go"},
		},
		Nodes: []Node{
			{ID: "test", Kind: NodeFile, Name: "aaa_test.go", Path: "aaa_test.go", Test: true},
			{ID: "production", Kind: NodeFile, Name: "zzz.go", Path: "zzz.go"},
		},
	}
	snapshot.index = newGraphIndex(snapshot)
	result := snapshotSketch(snapshot, 256)
	require.Len(t, result.Hits, 2)
	require.Equal(t, "production", result.Hits[0].NodeID)
}

func TestSketchAndRenderingAreDeterministicAndStrictlyBounded(t *testing.T) {
	t.Parallel()

	snapshot := queryTestSnapshot()
	first := snapshotSketch(snapshot, 80)
	second := snapshotSketch(snapshot, 80)
	require.Equal(t, first, second)
	require.LessOrEqual(t, len(first.Text), 80*4)
	require.LessOrEqual(t, first.Meta.UsedTokens, 80)

	hits := []Hit{{NodeID: "unicode", Name: "処理サービス🙂", Kind: NodeSymbol, Path: "日本語/処理.go", Line: 10, Score: 1}}
	for tokens := 1; tokens <= 32; tokens++ {
		result := renderGraphResult("focus", snapshot, hits, nil, 1, tokens, nil)
		require.LessOrEqual(t, len(result.Text), tokens*4)
		require.True(t, utf8.ValidString(result.Text))
		require.LessOrEqual(t, result.Meta.UsedTokens, tokens)
	}
}

func TestRenderingSanitizesTerminalControls(t *testing.T) {
	t.Parallel()

	snapshot := queryTestSnapshot()
	hits := []Hit{{
		NodeID: "control", Name: "Reset\x1b[31m\u202eName", Kind: NodeSymbol,
		Path: "src/evil\x1bfile.go", Line: 1, Score: 1,
	}}
	result := renderGraphResult(
		"focus", snapshot, hits, suggestedReadWindows(hits), 1, 256,
		[]string{"warning\x1b[2J\u202etext"},
	)

	require.NotContains(t, result.Text, "\x1b")
	require.NotContains(t, result.Text, "\u202e")
	require.Contains(t, result.Text, "Reset")
	require.Contains(t, result.Text, "evil")
	require.Contains(t, result.Text, "warning")
}

func queryTestSnapshot() *Snapshot {
	snapshot := &Snapshot{
		Root:       "/repo",
		Generation: 7,
		Facts: map[string]FileFacts{
			"api/handler.go": {
				Path: "api/handler.go", Language: "go",
				Symbols: []SymbolFact{{Name: "HandleOrder", Qualified: "HandleOrder", Kind: "function", StartLine: 10, EndLine: 18}},
				Imports: []ImportFact{{Target: "../domain/order", Line: 3}},
				Calls:   []CallFact{{Caller: "HandleOrder", Callee: "ProcessOrder", Line: 14}},
				Routes:  []RouteFact{{Method: "POST", Path: "/orders/:id", Owner: "HandleOrder", Line: 11}},
			},
			"domain/order.go": {
				Path: "domain/order.go", Language: "go",
				Symbols: []SymbolFact{
					{Name: "OrderService", Qualified: "OrderService", Kind: "struct", StartLine: 4, EndLine: 30},
					{Name: "ProcessOrder", Qualified: "OrderService.ProcessOrder", Kind: "method", StartLine: 8, EndLine: 16},
				},
				Literals: []LiteralFact{{Value: "API_TOKEN", Kind: "env", Line: 12}},
			},
			"domain/order_test.go": {
				Path: "domain/order_test.go", Language: "go",
				Symbols: []SymbolFact{{Name: "TestProcessOrder", Qualified: "TestProcessOrder", Kind: "function", StartLine: 6, EndLine: 14}},
				Imports: []ImportFact{{Target: "./order", Line: 3}},
				Calls:   []CallFact{{Caller: "TestProcessOrder", Callee: "ProcessOrder", Line: 9}},
			},
			"web/client.ts": {
				Path: "web/client.ts", Language: "typescript",
				Symbols:  []SymbolFact{{Name: "submitOrder", Qualified: "submitOrder", Kind: "function", StartLine: 3, EndLine: 9}},
				Literals: []LiteralFact{{Value: "/orders/42", Kind: "path", Line: 5}, {Value: "API_TOKEN", Kind: "env", Line: 6}},
			},
		},
	}
	buildGraph(snapshot)
	return snapshot
}
