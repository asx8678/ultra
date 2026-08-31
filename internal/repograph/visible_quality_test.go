package repograph

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type visibleQrel struct {
	name     string
	path     string
	relation EdgeKind
	grade    float64
}

type visibleQualityCase struct {
	name        string
	query       string
	scope       Scope
	impactFiles []string
	qrels       []visibleQrel
}

func TestVisibleSemanticUtility(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.CopyFS(root, os.DirFS("testdata/semantic-mini")))
	manager, err := NewManager(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })

	cases := []visibleQualityCase{
		{
			name: "go-call", query: "GetOrder", scope: Scope{Path: "api"},
			qrels: []visibleQrel{
				{name: "GetOrder", path: "api/server.go", grade: 3},
				{name: "LoadOrder", path: "domain/order.go", relation: EdgeCalls, grade: 2},
			},
		},
		{
			name: "typescript-call", query: "getOrder", scope: Scope{Path: "web"},
			qrels: []visibleQrel{
				{name: "getOrder", path: "web/router.ts", grade: 3},
				{name: "publishAudit", path: "web/router.ts", relation: EdgeCalls, grade: 2},
			},
		},
		{
			name: "python-call", query: "get_order", scope: Scope{Path: "python"},
			qrels: []visibleQrel{
				{name: "get_order", path: "python/service.py", grade: 3},
				{name: "fetch", path: "python/service.py", relation: EdgeCalls, grade: 2},
			},
		},
		{
			name: "rust-call", query: "get_order", scope: Scope{Path: "rust"},
			qrels: []visibleQrel{
				{name: "get_order", path: "rust/src/lib.rs", grade: 3},
				{name: "load_order", path: "rust/src/repository.rs", relation: EdgeCalls, grade: 2},
			},
		},
		{
			name: "route", query: "GET /orders/{*}",
			qrels: []visibleQrel{
				{name: "GET /orders/{*}", grade: 3},
				{path: "api/server.go", relation: EdgeRoutes, grade: 2},
				{path: "web/router.ts", relation: EdgeRoutes, grade: 1},
			},
		},
		{
			name: "configuration", query: "ORDER_AUDIT_TOPIC",
			qrels: []visibleQrel{
				{name: "ORDER_AUDIT_TOPIC", grade: 3},
				{path: "config/service.yaml", relation: EdgeShares, grade: 2},
			},
		},
		{
			name: "impact", impactFiles: []string{"domain/order.go"},
			qrels: []visibleQrel{
				{path: "api/server_test.go", relation: EdgeImports, grade: 3},
				{path: "api/server.go", relation: EdgeImports, grade: 2},
			},
		},
	}

	var strata []float64
	for _, budget := range []int{256, 512, 1024} {
		for _, testCase := range cases {
			testCase := testCase
			run := func(sessionPrefix string) (Result, error) {
				if len(testCase.impactFiles) > 0 {
					return manager.Impact(t.Context(), ImpactOptions{
						Files: testCase.impactFiles, MaxTokens: budget,
					})
				}
				return manager.Focus(t.Context(), FocusOptions{
					SessionID: fmt.Sprintf("%s-%s-%d", sessionPrefix, testCase.name, budget),
					Query:     testCase.query, Scope: testCase.scope, Fresh: true, MaxTokens: budget,
				})
			}
			result, queryErr := run("vsu")
			require.NoError(t, queryErr, testCase.name)
			require.LessOrEqual(t, len(result.Text), budget*4, testCase.name)
			require.Equal(t, len(result.Hits), renderedHitCount(result.Text), testCase.name)
			for index, hit := range result.Hits {
				require.Contains(t, result.Text, strings.TrimSpace(renderHit(index+1, hit)), testCase.name)
			}
			utility := visibleUtility(result.Hits, testCase.qrels)
			strata = append(strata, utility)
			t.Logf("VSU %s budget=%d: %.2f", testCase.name, budget, utility*100)
			require.GreaterOrEqual(t, utility, 0.55, testCase.name)

			repeated, repeatErr := run("vsu-repeat")
			require.NoError(t, repeatErr)
			require.Equal(t, result.Text, repeated.Text, testCase.name)
			require.Equal(t, result.Hits, repeated.Hits, testCase.name)
		}
	}

	utility := mean(strata) * 100
	fmt.Printf("    METRIC visible_semantic_utility=%.2f\n", utility)
	require.GreaterOrEqual(t, utility, 90.0)
}

func TestVisibleSemanticUtilityHeldOutRepository(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeQualityFixture(t, root, map[string]string{
		"go.mod": `module example.com/heldout

go 1.27
`,
		"ledger/commit.go": `package ledger

func CommitInvoice() string { return "INVOICE_STREAM" }
`,
		"gateway/routes.go": `package gateway

import "example.com/heldout/ledger"

type Router interface { POST(string, func() string) }
func InstallRoutes(router Router) { router.POST("/invoices/:invoice_id", SettleInvoice) }
func SettleInvoice() string { return ledger.CommitInvoice() }
`,
		"gateway/routes_test.go": `package gateway

import "testing"
func TestSettleInvoice(t *testing.T) { _ = SettleInvoice() }
`,
		"worker/dispatch.ts": `export function dispatchInvoice(): string {
  return recordInvoice();
}
function recordInvoice(): string {
  return "INVOICE_STREAM";
}
`,
		"config/stream.yaml": "stream: \"INVOICE_STREAM\"\n",
	})
	manager, err := NewManager(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })

	cases := []visibleQualityCase{
		{
			name: "heldout-go", query: "SettleInvoice", scope: Scope{Path: "gateway"},
			qrels: []visibleQrel{
				{name: "SettleInvoice", path: "gateway/routes.go", grade: 3},
				{name: "CommitInvoice", path: "ledger/commit.go", relation: EdgeCalls, grade: 2},
			},
		},
		{
			name: "heldout-typescript", query: "dispatchInvoice", scope: Scope{Path: "worker"},
			qrels: []visibleQrel{
				{name: "dispatchInvoice", path: "worker/dispatch.ts", grade: 3},
				{name: "recordInvoice", path: "worker/dispatch.ts", relation: EdgeCalls, grade: 2},
			},
		},
		{
			name: "heldout-route", query: "POST /invoices/{*}",
			qrels: []visibleQrel{
				{name: "POST /invoices/{*}", grade: 3},
				{path: "gateway/routes.go", relation: EdgeRoutes, grade: 2},
			},
		},
		{
			name: "heldout-configuration", query: "INVOICE_STREAM",
			qrels: []visibleQrel{
				{name: "INVOICE_STREAM", grade: 3},
				{path: "ledger/commit.go", relation: EdgeShares, grade: 2},
			},
		},
	}

	var strata []float64
	for _, budget := range []int{256, 512} {
		for _, testCase := range cases {
			result, queryErr := manager.Focus(t.Context(), FocusOptions{
				SessionID: fmt.Sprintf("%s-%d", testCase.name, budget),
				Query:     testCase.query, Scope: testCase.scope, Fresh: true, MaxTokens: budget,
			})
			require.NoError(t, queryErr, testCase.name)
			require.Equal(t, len(result.Hits), renderedHitCount(result.Text), testCase.name)
			utility := visibleUtility(result.Hits, testCase.qrels)
			strata = append(strata, utility)
			t.Logf("Held-out VSU %s budget=%d: %.2f", testCase.name, budget, utility*100)
			require.GreaterOrEqual(t, utility, 0.55, testCase.name)
		}
	}
	utility := mean(strata) * 100
	fmt.Printf("    METRIC heldout_visible_semantic_utility=%.2f\n", utility)
	require.GreaterOrEqual(t, utility, 90.0)
}

func TestFocusHandlesAmbiguityAndNegativeQueries(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.CopyFS(root, os.DirFS("testdata/semantic-mini")))
	for index := range 24 {
		path := filepath.Join(root, "aaa", fmt.Sprintf("duplicate_%02d.go", index))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(fmt.Sprintf(
			"package duplicate\nfunc GetOrder() int { return %d }\n", index,
		)), 0o644))
	}
	manager, err := NewManager(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })

	ambiguous, err := manager.Focus(t.Context(), FocusOptions{
		SessionID: "ambiguous", Query: "GetOrder", Fresh: true, MaxTokens: 1024,
	})
	require.NoError(t, err)
	require.True(t, hasHitWhere(ambiguous, func(hit Hit) bool {
		return hit.Name == "GetOrder" && hit.Path == "api/server.go"
	}))
	for index, hit := range ambiguous.Hits {
		if index == 3 {
			break
		}
		require.NotEqual(t, "GetOlderOrder", hit.Name)
	}

	missing, err := manager.Focus(t.Context(), FocusOptions{
		SessionID: "missing", Query: "symbol_that_cannot_exist_9f41", Fresh: true, MaxTokens: 256,
	})
	require.NoError(t, err)
	require.Equal(t, "no_matches", missing.Meta.Status)
	require.Empty(t, missing.Hits)
}

func visibleUtility(hits []Hit, qrels []visibleQrel) float64 {
	limit := min(10, len(hits))
	matched := make(map[int]struct{}, len(qrels))
	dcg := 0.0
	relationExpected := 0
	relationCorrect := 0
	for rank, hit := range hits[:limit] {
		for index, qrel := range qrels {
			if _, exists := matched[index]; exists || !qrelMatchesHit(qrel, hit, false) {
				continue
			}
			matched[index] = struct{}{}
			dcg += (math.Pow(2, qrel.grade) - 1) / math.Log2(float64(rank)+2)
			break
		}
	}
	grades := make([]float64, 0, len(qrels))
	for _, qrel := range qrels {
		grades = append(grades, qrel.grade)
		if qrel.relation != "" {
			relationExpected++
			for _, hit := range hits[:limit] {
				if qrelMatchesHit(qrel, hit, true) {
					relationCorrect++
					break
				}
			}
		}
	}
	sort.Slice(grades, func(i, j int) bool { return grades[i] > grades[j] })
	ideal := 0.0
	for rank, grade := range grades {
		ideal += (math.Pow(2, grade) - 1) / math.Log2(float64(rank)+2)
	}
	ndcg := 1.0
	if ideal > 0 {
		ndcg = dcg / ideal
	}
	recall := float64(len(matched)) / float64(len(qrels))
	relationAccuracy := 1.0
	if relationExpected > 0 {
		relationAccuracy = float64(relationCorrect) / float64(relationExpected)
	}
	return 0.60*ndcg + 0.25*recall + 0.15*relationAccuracy
}

func qrelMatchesHit(qrel visibleQrel, hit Hit, requireRelation bool) bool {
	if qrel.name != "" && qrel.name != hit.Name {
		return false
	}
	if qrel.path != "" && qrel.path != hit.Path {
		return false
	}
	return !requireRelation || qrel.relation == "" || qrel.relation == hit.Relation
}

func renderedHitCount(text string) int {
	count := 0
	for _, line := range strings.Split(text, "\n") {
		if _, err := fmt.Sscanf(line, "%d.", new(int)); err == nil {
			count++
		}
	}
	return count
}

func writeQualityFixture(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for path, content := range files {
		fullPath := filepath.Join(root, filepath.FromSlash(path))
		require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0o755))
		require.NoError(t, os.WriteFile(fullPath, []byte(content), 0o644))
	}
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}
