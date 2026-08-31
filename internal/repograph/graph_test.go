package repograph

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildGraphCreatesSemanticEdgesDeterministically(t *testing.T) {
	t.Parallel()

	makeSnapshot := func() *Snapshot {
		return &Snapshot{Facts: map[string]FileFacts{
			"client/view.ts": {
				Path: "client/view.ts", Language: "typescript",
				Symbols:  []SymbolFact{{Name: "loadUser", Qualified: "loadUser", Kind: "function", StartLine: 3, EndLine: 7}},
				Imports:  []ImportFact{{Target: "../server/service", Line: 1}},
				Calls:    []CallFact{{Caller: "loadUser", Callee: "GetUser", Line: 5}},
				Literals: []LiteralFact{{Value: "/users/42", Kind: "path", Line: 4}, {Value: "API_TOKEN", Kind: "env", Line: 6}},
			},
			"server/base.go": {
				Path: "server/base.go", Language: "go",
				Symbols: []SymbolFact{{Name: "Base", Qualified: "Base", Kind: "interface", StartLine: 2, EndLine: 4}},
			},
			"server/service.go": {
				Path: "server/service.go", Language: "go",
				Symbols: []SymbolFact{
					{Name: "Service", Qualified: "Service", Kind: "struct", StartLine: 3, EndLine: 12},
					{Name: "GetUser", Qualified: "Service.GetUser", Kind: "method", StartLine: 5, EndLine: 10},
				},
				Imports:     []ImportFact{{Target: "./base", Line: 1}},
				Routes:      []RouteFact{{Method: "GET", Path: "/users/:id", Owner: "Service.GetUser", Line: 6}},
				Literals:    []LiteralFact{{Value: "API_TOKEN", Kind: "env", Line: 7}},
				Inheritance: []InheritanceFact{{Child: "Service", Parent: "Base", Line: 3}},
			},
			"server/service_test.go": {
				Path: "server/service_test.go", Language: "go",
				Symbols: []SymbolFact{{Name: "TestGetUser", Qualified: "TestGetUser", Kind: "function", StartLine: 3, EndLine: 8}},
				Imports: []ImportFact{{Target: "./service", Line: 1}},
				Calls:   []CallFact{{Caller: "TestGetUser", Callee: "GetUser", Line: 5}},
			},
		}}
	}

	first := makeSnapshot()
	second := makeSnapshot()
	firstIndex := buildGraph(first)
	buildGraph(second)
	require.Equal(t, first.Nodes, second.Nodes)
	require.Equal(t, first.Edges, second.Edges)

	kinds := make(map[EdgeKind]int)
	for _, edge := range first.Edges {
		kinds[edge.Kind]++
	}
	for _, kind := range []EdgeKind{EdgeContains, EdgeImports, EdgeCalls, EdgeTests, EdgeRoutes, EdgeShares, EdgeInherits} {
		require.Positive(t, kinds[kind], "missing %s edge", kind)
	}

	getUser := findGraphNode(t, first.Nodes, NodeSymbol, "GetUser")
	loadUser := findGraphNode(t, first.Nodes, NodeSymbol, "loadUser")
	requireEdge(t, first.Edges, loadUser.ID, getUser.ID, EdgeCalls)

	// The route is declared after the client path in lexical file order. The
	// literal must still join it after the graph's route pre-pass.
	route := findGraphNode(t, first.Nodes, NodeRoute, "GET /users/{*}")
	requireEdge(t, first.Edges, loadUser.ID, route.ID, EdgeRoutes)

	neighbors := firstIndex.neighbors(getUser.ID)
	require.NotEmpty(t, neighbors)
	for index := 1; index < len(neighbors); index++ {
		require.GreaterOrEqual(t, neighbors[index-1].Edge.Weight, neighbors[index].Edge.Weight)
	}
}

func TestBuildGraphPrefersProductionEvidenceForSharedNodes(t *testing.T) {
	t.Parallel()

	snapshot := &Snapshot{Facts: map[string]FileFacts{
		"aaa_test.go": {
			Path: "aaa_test.go", Language: "go",
			Routes:   []RouteFact{{Method: "GET", Path: "/shared", Line: 1}},
			Literals: []LiteralFact{{Value: "SHARED_TOKEN", Kind: "env", Line: 2}},
		},
		"zzz.go": {
			Path: "zzz.go", Language: "go",
			Routes:   []RouteFact{{Method: "GET", Path: "/shared", Line: 1}},
			Literals: []LiteralFact{{Value: "SHARED_TOKEN", Kind: "env", Line: 2}},
		},
	}}
	buildGraph(snapshot)

	route := findGraphNode(t, snapshot.Nodes, NodeRoute, "GET /shared")
	require.False(t, route.Test)
	require.Equal(t, "zzz.go", route.Path)
	literal := findGraphNode(t, snapshot.Nodes, NodeLiteral, "SHARED_TOKEN")
	require.False(t, literal.Test)
	require.Equal(t, "zzz.go", literal.Path)
}

func TestBuildGraphPreservesLegalRepositoryPaths(t *testing.T) {
	t.Parallel()

	snapshot := &Snapshot{Facts: map[string]FileFacts{
		"lead.go":        {Path: "lead.go", Language: "go"},
		" lead.go":       {Path: " lead.go", Language: "go"},
		"back/slash.go":  {Path: "back/slash.go", Language: "go"},
		"back\\slash.go": {Path: "back\\slash.go", Language: "go"},
	}}
	buildGraph(snapshot)

	paths := make(map[string]struct{})
	for _, node := range snapshot.Nodes {
		if node.Kind == NodeFile {
			paths[node.Path] = struct{}{}
		}
	}
	require.Contains(t, paths, "lead.go")
	require.Contains(t, paths, " lead.go")
	require.Contains(t, paths, "back/slash.go")
	require.Contains(t, paths, "back\\slash.go")
	require.Len(t, paths, 4)
}

func TestBuildGraphSuppressesHighFrequencyLiteralHubs(t *testing.T) {
	t.Parallel()

	facts := make(map[string]FileFacts)
	for index := range 10 {
		filePath := fmt.Sprintf("file-%02d.go", index)
		facts[filePath] = FileFacts{
			Path: filePath, Language: "go",
			Literals: []LiteralFact{{Value: "COMMON_VALUE", Kind: "env", Line: 1}},
		}
	}
	snapshot := &Snapshot{Facts: facts}
	buildGraph(snapshot)
	for _, node := range snapshot.Nodes {
		require.False(t, node.Kind == NodeLiteral && node.Name == "COMMON_VALUE")
	}
}

func TestResolveImportPathAcrossLanguageConventions(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		"src/pkg/index.ts":  "a",
		"src/util.py":       "b",
		"internal/auth.go":  "c",
		"other/internal.go": "d",
	}
	require.Equal(t, "src/pkg/index.ts", resolveImportPath("src/app.ts", "./pkg", files))
	require.Equal(t, "src/util.py", resolveImportPath("tests/test_util.py", "src.util", files))
	require.Equal(t, "internal/auth.go", resolveImportPath("cmd/main.go", "example.com/project/internal/auth", files))
}

func findGraphNode(t *testing.T, nodes []Node, kind NodeKind, name string) Node {
	t.Helper()
	for _, node := range nodes {
		if node.Kind == kind && node.Name == name {
			return node
		}
	}
	require.FailNow(t, "node not found", "%s %q", kind, name)
	return Node{}
}

func requireEdge(t *testing.T, edges []Edge, from, to string, kind EdgeKind) {
	t.Helper()
	for _, edge := range edges {
		if edge.From == from && edge.To == to && edge.Kind == kind {
			return
		}
	}
	require.FailNow(t, "edge not found", "%s -> %s (%s)", from, to, kind)
}
