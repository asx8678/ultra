package repograph

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

type qualityGold struct {
	name     string
	path     string
	kind     NodeKind
	relation EdgeKind
	line     int
}

type qualityScore struct {
	points float64
}

var semanticSymbolGold = []qualityGold{
	{name: "GetOrder", path: "api/server.go", kind: NodeSymbol, line: 21},
	{name: "OrderController", path: "web/router.ts", kind: NodeSymbol, line: 4},
	{name: "OrderService", path: "python/service.py", kind: NodeSymbol, line: 7},
	{name: "OrderRepository", path: "rust/src/lib.rs", kind: NodeSymbol, line: 5},
}

func TestAutoresearchQuality(t *testing.T) {
	ctx := context.Background()
	var (
		score      qualityScore
		latencies  []time.Duration
		maxTokens  int
		metricDone bool
	)
	defer func() {
		if metricDone {
			return
		}
		metricDone = true
		quality := min(100, max(0, int(math.Round(score.points))))
		fmt.Printf("    METRIC semantic_quality=%d\n", quality)
		fmt.Printf("    METRIC p95_ms=%.3f\n", percentile95(latencies).Seconds()*1000)
		fmt.Printf("    METRIC max_tokens=%d\n", maxTokens)
	}()

	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS("testdata/semantic-mini")); err != nil {
		t.Fatalf("Copy semantic fixture: %v", err)
	}
	manager, err := NewManager(root, t.TempDir())
	if err != nil {
		t.Fatalf("Create graph manager: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := manager.Close(); closeErr != nil {
			t.Errorf("Close graph manager: %v", closeErr)
		}
	})

	timedResult := func(label string, call func() (Result, error)) Result {
		started := time.Now()
		result, callErr := call()
		latencies = append(latencies, time.Since(started))
		maxTokens = max(maxTokens, result.Meta.UsedTokens)
		if callErr != nil {
			t.Logf("%s: %v", label, callErr)
		}
		return result
	}

	started := time.Now()
	initial, initialReport, refreshErr := manager.Refresh(ctx)
	latencies = append(latencies, time.Since(started))
	if refreshErr != nil {
		t.Fatalf("Refresh semantic fixture: %v", refreshErr)
	}

	sketch := timedResult("sketch", func() (Result, error) {
		return manager.Sketch(ctx, 800)
	})

	symbolResults := make([]Result, 0, len(semanticSymbolGold))
	for index, gold := range semanticSymbolGold {
		gold := gold
		symbolResults = append(symbolResults, timedResult("focus symbol "+gold.name, func() (Result, error) {
			return manager.Focus(ctx, FocusOptions{
				SessionID: fmt.Sprintf("symbol-%d", index),
				Query:     gold.name,
				Scope:     Scope{Path: filepath.ToSlash(filepath.Dir(gold.path))},
				Fresh:     true,
				MaxTokens: 800,
			})
		}))
	}
	symbolChecks := make([]bool, len(semanticSymbolGold))
	for index, gold := range semanticSymbolGold {
		symbolChecks[index] = hasQualityHit(symbolResults[index], gold)
	}
	score.category(t, "symbols", 12, symbolChecks...)

	route := timedResult("focus route", func() (Result, error) {
		return manager.Focus(ctx, FocusOptions{
			SessionID: "route",
			Query:     "GET /orders/{*}",
			Fresh:     true,
			MaxTokens: 800,
		})
	})
	routeChecks := []bool{
		hasHitWhere(route, func(hit Hit) bool {
			return hit.Kind == NodeRoute && strings.Contains(hit.Name, "/orders/{*}")
		}),
		hasQualityHit(route, qualityGold{path: "api/server.go", relation: EdgeRoutes}),
		hasQualityHit(route, qualityGold{path: "web/router.ts", relation: EdgeRoutes}),
		hasQualityHit(route, qualityGold{path: "python/service.py", relation: EdgeRoutes}),
		hasQualityHit(route, qualityGold{path: "openapi.yaml", relation: EdgeRoutes}),
	}
	score.category(t, "routes", 12, routeChecks...)

	dwell := timedResult("dwell route", func() (Result, error) {
		return manager.Dwell(ctx, "route", 800)
	})

	config := timedResult("focus config literal", func() (Result, error) {
		return manager.Focus(ctx, FocusOptions{SessionID: "config", Query: "ORDER_AUDIT_TOPIC", Fresh: true, MaxTokens: 800})
	})
	score.category(t, "config", 10,
		hasQualityHit(config, qualityGold{name: "ORDER_AUDIT_TOPIC", kind: NodeLiteral}),
		hasQualityHit(config, qualityGold{path: "config/service.yaml", relation: EdgeShares}),
		hasQualityHit(config, qualityGold{path: "domain/order.go", relation: EdgeShares}),
		hasQualityHit(config, qualityGold{path: "web/settings.ts", relation: EdgeShares}),
	)

	goImport := focusPath(timedResult, manager, ctx, "go-import", "api/server.go", 800)
	tsImport := focusPath(timedResult, manager, ctx, "ts-import", "web/router.ts", 800)
	pythonImport := focusPath(timedResult, manager, ctx, "python-import", "python/service.py", 800)
	rustImport := focusPath(timedResult, manager, ctx, "rust-import", "rust/src/lib.rs", 800)
	score.category(t, "imports", 10,
		hasQualityHit(goImport, qualityGold{path: "domain/order.go", relation: EdgeImports}),
		hasQualityHit(tsImport, qualityGold{path: "web/settings.ts", relation: EdgeImports}),
		hasQualityHit(pythonImport, qualityGold{path: "python/repository.py", relation: EdgeImports}),
		hasQualityHit(rustImport, qualityGold{path: "rust/src/repository.rs", relation: EdgeImports}),
	)

	goCall := focusSymbol(timedResult, manager, ctx, "go-call", "GetOrder", "api", 800)
	tsCall := focusSymbol(timedResult, manager, ctx, "ts-call", "getOrder", "web", 800)
	pythonCall := focusSymbol(timedResult, manager, ctx, "python-call", "get_order", "python", 800)
	rustCall := focusSymbol(timedResult, manager, ctx, "rust-call", "get_order", "rust", 800)
	score.category(t, "calls", 10,
		hasQualityHit(goCall, qualityGold{name: "LoadOrder", path: "domain/order.go", relation: EdgeCalls}),
		hasQualityHit(tsCall, qualityGold{name: "publishAudit", path: "web/router.ts", relation: EdgeCalls}),
		hasQualityHit(pythonCall, qualityGold{name: "fetch", path: "python/service.py", relation: EdgeCalls}),
		hasQualityHit(rustCall, qualityGold{name: "load_order", path: "rust/src/repository.rs", relation: EdgeCalls}),
	)

	score.category(t, "tests", 10,
		hasQualityHit(goCall, qualityGold{path: "api/server_test.go", relation: EdgeTests}),
		hasQualityHit(tsCall, qualityGold{path: "web/router.test.ts", relation: EdgeTests}),
		hasQualityHit(pythonCall, qualityGold{path: "python/test_service.py", relation: EdgeTests}),
	)

	fuzzy := timedResult("focus fuzzy", func() (Result, error) {
		return manager.Focus(ctx, FocusOptions{SessionID: "fuzzy", Query: "GetOrdr", Fresh: true, MaxTokens: 300})
	})
	fuzzyFirst := len(fuzzy.Hits) > 0 && fuzzy.Hits[0].Name == "GetOrder" && fuzzy.Hits[0].Path == "api/server.go"
	score.category(t, "fuzzy", 8,
		hasQualityHit(fuzzy, qualityGold{name: "GetOrder", path: "api/server.go", kind: NodeSymbol}),
		fuzzyFirst,
	)

	impact := timedResult("impact", func() (Result, error) {
		return manager.Impact(ctx, ImpactOptions{Files: []string{"domain/order.go"}, Symbols: []string{"LoadOrder"}, MaxTokens: 800})
	})

	budgetSketch := timedResult("budget sketch", func() (Result, error) {
		return manager.Sketch(ctx, 32)
	})
	budgetFocus := timedResult("budget focus", func() (Result, error) {
		return manager.Focus(ctx, FocusOptions{SessionID: "budget", Query: "order", Fresh: true, MaxTokens: 48})
	})
	budgetImpact := timedResult("budget impact", func() (Result, error) {
		return manager.Impact(ctx, ImpactOptions{Files: []string{"api/server.go"}, MaxTokens: 64})
	})
	score.category(t, "budgets", 10,
		withinBudget(budgetSketch, 32),
		withinBudget(budgetFocus, 48),
		withinBudget(budgetImpact, 64),
		budgetSketch.Meta.Truncated,
		budgetFocus.Meta.Truncated,
		budgetImpact.Meta.Truncated,
	)

	unchanged, unchangedReport, unchangedErr := manager.Refresh(ctx)
	if unchangedErr != nil {
		t.Logf("unchanged refresh: %v", unchangedErr)
	}
	serverPath := filepath.Join(root, "api", "server.go")
	serverSource, readErr := os.ReadFile(serverPath)
	if readErr != nil {
		t.Fatalf("Read incremental fixture: %v", readErr)
	}
	serverSource = append(serverSource, []byte("\nfunc (s *Server) CancelOrder() {}\n")...)
	if writeErr := os.WriteFile(serverPath, serverSource, 0o644); writeErr != nil {
		t.Fatalf("Mutate incremental fixture: %v", writeErr)
	}
	started = time.Now()
	changed, changedReport, changedErr := manager.Refresh(ctx)
	latencies = append(latencies, time.Since(started))
	if changedErr != nil {
		t.Logf("changed refresh: %v", changedErr)
	}
	cancel := timedResult("focus invalidated symbol", func() (Result, error) {
		return manager.Focus(ctx, FocusOptions{SessionID: "incremental", Query: "CancelOrder", Fresh: true, MaxTokens: 300})
	})
	score.category(t, "incremental", 10,
		unchangedErr == nil && unchanged != nil && initial != nil && unchanged.Generation == initial.Generation,
		len(unchangedReport.Parsed) == 0 && initial != nil && len(unchangedReport.Reused) == initial.Coverage.Indexed,
		changedErr == nil && changed != nil && unchanged != nil && changed.Generation == unchanged.Generation+1,
		slices.Equal(changedReport.Parsed, []string{"api/server.go"}),
		!slices.Contains(changedReport.Reused, "api/server.go") && slices.Contains(changedReport.Reused, "domain/order.go"),
		hasQualityHit(cancel, qualityGold{name: "CancelOrder", path: "api/server.go", kind: NodeSymbol}),
	)

	score.category(t, "operations", 8,
		initial.Coverage.Indexed >= 14 && initial.Coverage.Indexed == len(initial.Facts) &&
			initial.Coverage.Unsupported >= 1 && len(initialReport.Parsed) == initial.Coverage.Indexed,
		sketch.Meta.Operation == "sketch" && len(sketch.Hits) > 0,
		route.Meta.Operation == "focus" && route.Meta.Generation == initial.Generation,
		dwell.Meta.Operation == "dwell" && dwell.Meta.Depth >= 1 && len(dwell.Hits) > 0,
		impact.Meta.Operation == "impact" && hasQualityHit(impact, qualityGold{path: "api/server.go"}),
	)
	quality := min(100, max(0, int(math.Round(score.points))))
	if quality != 100 {
		t.Fatalf("Semantic quality gate failed: got %d, want 100", quality)
	}
}

func focusPath(
	timed func(string, func() (Result, error)) Result,
	manager *Manager,
	ctx context.Context,
	sessionID string,
	query string,
	maxTokens int,
) Result {
	return timed("focus "+query, func() (Result, error) {
		return manager.Focus(ctx, FocusOptions{SessionID: sessionID, Query: query, Fresh: true, MaxTokens: maxTokens})
	})
}

func focusSymbol(
	timed func(string, func() (Result, error)) Result,
	manager *Manager,
	ctx context.Context,
	sessionID string,
	query string,
	path string,
	maxTokens int,
) Result {
	return timed("focus "+query, func() (Result, error) {
		return manager.Focus(ctx, FocusOptions{
			SessionID: sessionID,
			Query:     query,
			Scope:     Scope{Path: path},
			Fresh:     true,
			MaxTokens: maxTokens,
		})
	})
}

func (score *qualityScore) category(t *testing.T, name string, weight float64, checks ...bool) {
	t.Helper()
	passed := 0
	for _, check := range checks {
		if check {
			passed++
		}
	}
	if len(checks) != 0 {
		score.points += weight * float64(passed) / float64(len(checks))
	}
	if passed != len(checks) {
		t.Logf("quality %s: %d/%d checks", name, passed, len(checks))
	}
}

func hasQualityHit(result Result, gold qualityGold) bool {
	return hasHitWhere(result, func(hit Hit) bool {
		return (gold.name == "" || hit.Name == gold.name) &&
			(gold.path == "" || hit.Path == gold.path) &&
			(gold.kind == "" || hit.Kind == gold.kind) &&
			(gold.relation == "" || hit.Relation == gold.relation) &&
			(gold.line == 0 || hit.Line == gold.line)
	})
}

func hasHitWhere(result Result, predicate func(Hit) bool) bool {
	for _, hit := range result.Hits {
		if predicate(hit) {
			return true
		}
	}
	return false
}

func withinBudget(result Result, budget int) bool {
	return result.Meta.MaxTokens == budget &&
		result.Meta.UsedTokens <= budget &&
		(len(result.Text)+3)/4 <= budget
}

func percentile95(values []time.Duration) time.Duration {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]time.Duration(nil), values...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	index := int(math.Ceil(float64(len(ordered))*0.95)) - 1
	return ordered[max(0, index)]
}
