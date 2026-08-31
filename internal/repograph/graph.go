package repograph

import (
	"context"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const (
	weightContains = 20
	weightImports  = 65
	weightCalls    = 90
	weightTests    = 100
	weightRoutes   = 85
	weightShares   = 45
	weightInherits = 95
)

// graphIndex contains immutable lookup tables derived from a snapshot. One
// index is retained per immutable snapshot generation and safely shared by
// concurrent queries.
type graphIndex struct {
	nodes      map[string]Node
	byPath     map[string][]string
	byName     map[string][]string
	byNorm     map[string][]string
	outgoing   map[string][]Edge
	incoming   map[string][]Edge
	fileByPath map[string]string
}

type graphArc struct {
	Edge      Edge
	Node      Node
	Direction string
}

// buildGraph replaces the materialized graph in snapshot with a deterministic
// graph built from its facts and returns an index over that graph.
func buildGraph(snapshot *Snapshot) *graphIndex {
	index, _ := buildGraphContext(context.Background(), snapshot)
	return index
}

func buildGraphContext(ctx context.Context, snapshot *Snapshot) (*graphIndex, error) {
	if snapshot == nil {
		return newGraphIndex(nil), nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	builder := graphBuilder{
		ctx:             ctx,
		snapshot:        snapshot,
		nodes:           make(map[string]Node),
		edges:           make(map[string]Edge),
		fileIDs:         make(map[string]string),
		symbolsByFile:   make(map[string][]Node),
		symbolsByName:   make(map[string][]Node),
		importsByFile:   make(map[string][]string),
		routesByPath:    make(map[string][]Node),
		literalFiles:    make(map[string]int),
		symbolKeyCounts: make(map[string]int),
	}
	if err := builder.build(); err != nil {
		return nil, err
	}

	snapshot.Nodes = make([]Node, 0, len(builder.nodes))
	for _, node := range builder.nodes {
		snapshot.Nodes = append(snapshot.Nodes, node)
	}
	sortNodes(snapshot.Nodes)

	snapshot.Edges = make([]Edge, 0, len(builder.edges))
	for _, edge := range builder.edges {
		snapshot.Edges = append(snapshot.Edges, edge)
	}
	sortEdges(snapshot.Edges)
	snapshot.index = newGraphIndex(snapshot)
	return snapshot.index, nil
}

type graphBuilder struct {
	ctx             context.Context
	snapshot        *Snapshot
	nodes           map[string]Node
	edges           map[string]Edge
	fileIDs         map[string]string
	symbolsByFile   map[string][]Node
	symbolsByName   map[string][]Node
	importsByFile   map[string][]string
	importResolver  *importPathResolver
	routesByPath    map[string][]Node
	literalFiles    map[string]int
	symbolKeyCounts map[string]int
}

func (b *graphBuilder) build() error {
	paths := sortedFactPaths(b.snapshot.Facts)
	for _, filePath := range paths {
		if err := b.ctx.Err(); err != nil {
			return err
		}
		facts := b.snapshot.Facts[filePath]
		filePath = cleanGraphPath(filePath)
		facts.Path = filePath
		b.addFile(facts)
	}
	b.importResolver = newImportPathResolver(b.fileIDs)
	for _, filePath := range paths {
		if err := b.ctx.Err(); err != nil {
			return err
		}
		facts := b.snapshot.Facts[filePath]
		facts.Path = cleanGraphPath(filePath)
		b.addSymbols(facts)
	}
	b.sortSymbolIndexes()
	for _, filePath := range paths {
		if err := b.ctx.Err(); err != nil {
			return err
		}
		facts := b.snapshot.Facts[filePath]
		facts.Path = cleanGraphPath(filePath)
		b.addImports(facts)
	}
	for _, filePath := range paths {
		if err := b.ctx.Err(); err != nil {
			return err
		}
		facts := b.snapshot.Facts[filePath]
		facts.Path = cleanGraphPath(filePath)
		b.addRoutes(facts)
	}
	b.indexLiteralFrequency(paths)
	for _, filePath := range paths {
		if err := b.ctx.Err(); err != nil {
			return err
		}
		facts := b.snapshot.Facts[filePath]
		facts.Path = cleanGraphPath(filePath)
		b.addLiterals(facts)
	}
	for _, filePath := range paths {
		if err := b.ctx.Err(); err != nil {
			return err
		}
		facts := b.snapshot.Facts[filePath]
		facts.Path = cleanGraphPath(filePath)
		b.addCalls(facts)
		b.addInferredCalls(facts)
		b.addInheritance(facts)
		b.addTestConventions(facts)
	}
	return b.ctx.Err()
}

func (b *graphBuilder) addFile(facts FileFacts) {
	filePath := cleanGraphPath(facts.Path)
	language := facts.Language
	if language == "" {
		language = languageForPath(filePath)
	}
	id := stableID("file", filePath)
	node := Node{
		ID:       id,
		Kind:     NodeFile,
		Name:     filepath.Base(filePath),
		Path:     filePath,
		Language: language,
		Test:     isTestPath(filePath),
	}
	b.nodes[id] = node
	b.fileIDs[filePath] = id
}

func (b *graphBuilder) addSymbols(facts FileFacts) {
	fileID, ok := b.fileIDs[facts.Path]
	if !ok {
		return
	}
	language := facts.Language
	if language == "" {
		language = languageForPath(facts.Path)
	}
	for _, fact := range facts.Symbols {
		if b.ctx.Err() != nil {
			return
		}
		qualified := strings.TrimSpace(fact.Qualified)
		identity := qualified
		if identity == "" {
			identity = strings.TrimSpace(fact.Name)
		}
		key := strings.Join([]string{facts.Path, identity, fact.Kind, fact.Signature}, "\x00")
		ordinal := b.symbolKeyCounts[key]
		b.symbolKeyCounts[key]++
		idParts := []string{"symbol", facts.Path, identity, fact.Kind, fact.Signature}
		if ordinal > 0 {
			idParts = append(idParts, itoa(ordinal))
		}
		id := stableID(idParts...)
		node := Node{
			ID:        id,
			Kind:      NodeSymbol,
			Name:      fact.Name,
			Qualified: qualified,
			Path:      facts.Path,
			Language:  language,
			Symbol:    fact.Kind,
			Signature: fact.Signature,
			Line:      positiveLine(fact.StartLine),
			EndLine:   maxInt(positiveLine(fact.StartLine), fact.EndLine),
			Test:      isTestPath(facts.Path) || isTestSymbol(fact.Name, fact.Kind),
		}
		b.nodes[id] = node
		b.symbolsByFile[facts.Path] = append(b.symbolsByFile[facts.Path], node)
		nameKey := normalizeName(fact.Name)
		if nameKey != "" {
			b.symbolsByName[nameKey] = append(b.symbolsByName[nameKey], node)
		}
		qualifiedKey := normalizeName(qualified)
		if qualifiedKey != "" && qualifiedKey != nameKey {
			b.symbolsByName[qualifiedKey] = append(b.symbolsByName[qualifiedKey], node)
		}
		b.addEdge(Edge{From: fileID, To: id, Kind: EdgeContains, Weight: weightContains, Path: facts.Path, Line: node.Line})
	}
}

func (b *graphBuilder) sortSymbolIndexes() {
	for key := range b.symbolsByFile {
		sortNodes(b.symbolsByFile[key])
	}
	for key := range b.symbolsByName {
		sortNodes(b.symbolsByName[key])
	}
}

func (b *graphBuilder) addImports(facts FileFacts) {
	from := b.fileIDs[facts.Path]
	for _, fact := range facts.Imports {
		if b.ctx.Err() != nil {
			return
		}
		target := b.importResolver.resolve(facts.Path, fact.Target)
		if target == "" || target == facts.Path {
			continue
		}
		b.importsByFile[facts.Path] = appendUnique(b.importsByFile[facts.Path], target)
		b.addEdge(Edge{
			From: from, To: b.fileIDs[target], Kind: EdgeImports, Weight: weightImports,
			Path: facts.Path, Line: positiveLine(fact.Line), Evidence: strings.TrimSpace(fact.Target),
		})
		if isTestPath(facts.Path) && !isTestPath(target) {
			b.addEdge(Edge{
				From: from, To: b.fileIDs[target], Kind: EdgeTests, Weight: weightTests,
				Path: facts.Path, Line: positiveLine(fact.Line), Evidence: "test import",
			})
		}
	}
	for key := range b.importsByFile {
		sort.Strings(b.importsByFile[key])
	}
}

func (b *graphBuilder) addRoutes(facts FileFacts) {
	language := facts.Language
	if language == "" {
		language = languageForPath(facts.Path)
	}
	for _, fact := range facts.Routes {
		if b.ctx.Err() != nil {
			return
		}
		routePath := normalizeRoute(fact.Path)
		if routePath == "" {
			continue
		}
		method := strings.ToUpper(strings.TrimSpace(fact.Method))
		id := stableID("route", method, routePath)
		node := Node{ID: id, Kind: NodeRoute, Name: routeName(method, routePath), Path: facts.Path, Language: language, Line: positiveLine(fact.Line), Test: isTestPath(facts.Path)}
		if previous, ok := b.nodes[id]; ok {
			node = preferredGraphNode(previous, node)
		}
		b.nodes[id] = node
		b.routesByPath[routePath] = appendNodeUnique(b.routesByPath[routePath], node)
		owner := b.resolveOwner(facts.Path, fact.Owner, fact.Line)
		b.addEdge(Edge{From: owner.ID, To: id, Kind: EdgeRoutes, Weight: weightRoutes, Path: facts.Path, Line: positiveLine(fact.Line), Evidence: strings.TrimSpace(fact.Path)})
	}
	for routePath := range b.routesByPath {
		sortNodes(b.routesByPath[routePath])
	}
}

func (b *graphBuilder) indexLiteralFrequency(paths []string) {
	for _, filePath := range paths {
		if b.ctx.Err() != nil {
			return
		}
		seen := make(map[string]struct{})
		for _, fact := range b.snapshot.Facts[filePath].Literals {
			value := strings.TrimSpace(fact.Value)
			kind := strings.TrimSpace(fact.Kind)
			if kind == "" {
				kind = literalKind(value)
			}
			if value == "" || kind == "" {
				continue
			}
			key := kind + "\x00" + canonicalLiteral(kind, value)
			seen[key] = struct{}{}
		}
		for key := range seen {
			b.literalFiles[key]++
		}
	}
}

func (b *graphBuilder) addLiterals(facts FileFacts) {
	language := facts.Language
	if language == "" {
		language = languageForPath(facts.Path)
	}
	frequencyLimit := minInt(32, maxInt(8, len(b.snapshot.Facts)/50))
	for _, fact := range facts.Literals {
		if b.ctx.Err() != nil {
			return
		}
		value := strings.TrimSpace(fact.Value)
		kind := strings.TrimSpace(fact.Kind)
		if kind == "" {
			kind = literalKind(value)
		}
		if value == "" || kind == "" {
			continue
		}
		canonical := canonicalLiteral(kind, value)
		owner := b.resolveOwner(facts.Path, "", fact.Line)
		if b.literalFiles[kind+"\x00"+canonical] <= frequencyLimit {
			id := stableID("literal", kind, canonical)
			node := Node{ID: id, Kind: NodeLiteral, Name: value, Path: facts.Path, Language: language, Line: positiveLine(fact.Line), Symbol: kind, Test: isTestPath(facts.Path)}
			if previous, ok := b.nodes[id]; ok {
				node = preferredGraphNode(previous, node)
			}
			b.nodes[id] = node
			b.addEdge(Edge{From: owner.ID, To: id, Kind: EdgeShares, Weight: weightShares, Path: facts.Path, Line: positiveLine(fact.Line), Evidence: value})
		}

		for routePath, routes := range b.routesByPath {
			if !routeMatchesLiteral(routePath, value) {
				continue
			}
			for _, route := range routes {
				b.addEdge(Edge{From: owner.ID, To: route.ID, Kind: EdgeRoutes, Weight: weightRoutes, Path: facts.Path, Line: positiveLine(fact.Line), Evidence: value})
			}
		}
	}
}

func (b *graphBuilder) addCalls(facts FileFacts) {
	for _, fact := range facts.Calls {
		if b.ctx.Err() != nil {
			return
		}
		caller := b.resolveOwner(facts.Path, fact.Caller, fact.Line)
		callee, ok := b.resolveSymbol(facts.Path, fact.Callee)
		if !ok || caller.ID == callee.ID {
			continue
		}
		b.addEdge(Edge{From: caller.ID, To: callee.ID, Kind: EdgeCalls, Weight: weightCalls, Path: facts.Path, Line: positiveLine(fact.Line), Evidence: strings.TrimSpace(fact.Callee)})
		if caller.Test && !callee.Test {
			b.addEdge(Edge{From: caller.ID, To: callee.ID, Kind: EdgeTests, Weight: weightTests, Path: facts.Path, Line: positiveLine(fact.Line), Evidence: strings.TrimSpace(fact.Callee)})
		}
	}
}

// addInferredCalls repairs a conservative extractor ambiguity common in
// JavaScript-family languages: a call such as publish(value) can be emitted as
// a one-line nested symbol. When a real declaration with that name exists,
// the enclosing symbol is unambiguously the caller.
func (b *graphBuilder) addInferredCalls(facts FileFacts) {
	switch facts.Language {
	case "typescript", "javascript", "svelte", "vue", "astro":
	default:
		return
	}
	for _, nested := range b.symbolsByFile[facts.Path] {
		if b.ctx.Err() != nil {
			return
		}
		if nested.Line <= 0 || nested.EndLine != nested.Line {
			continue
		}
		var caller Node
		for _, candidate := range b.symbolsByFile[facts.Path] {
			if candidate.ID == nested.ID || candidate.Line >= nested.Line || candidate.EndLine < nested.Line {
				continue
			}
			if caller.ID == "" || candidate.EndLine-candidate.Line < caller.EndLine-caller.Line || (candidate.EndLine-candidate.Line == caller.EndLine-caller.Line && graphNodeLess(candidate, caller)) {
				caller = candidate
			}
		}
		if caller.ID == "" {
			continue
		}
		var target Node
		targetRank := 0
		for _, candidate := range b.symbolsByName[normalizeName(nested.Name)] {
			rank := inferredCallTargetRank(candidate, nested, caller)
			if rank > targetRank || (rank == targetRank && rank > 0 && graphNodeLess(candidate, target)) {
				target = candidate
				targetRank = rank
			}
		}
		if targetRank <= 0 {
			continue
		}
		b.addEdge(Edge{From: caller.ID, To: target.ID, Kind: EdgeCalls, Weight: weightCalls, Path: facts.Path, Line: nested.Line, Evidence: nested.Name})
	}
}

func (b *graphBuilder) addInheritance(facts FileFacts) {
	for _, fact := range facts.Inheritance {
		if b.ctx.Err() != nil {
			return
		}
		child, childOK := b.resolveSymbol(facts.Path, fact.Child)
		parent, parentOK := b.resolveSymbol(facts.Path, fact.Parent)
		if !childOK || !parentOK || child.ID == parent.ID {
			continue
		}
		b.addEdge(Edge{From: child.ID, To: parent.ID, Kind: EdgeInherits, Weight: weightInherits, Path: facts.Path, Line: positiveLine(fact.Line), Evidence: strings.TrimSpace(fact.Parent)})
	}
}

func (b *graphBuilder) addTestConventions(facts FileFacts) {
	if b.ctx.Err() != nil || !isTestPath(facts.Path) {
		return
	}
	production := productionPathForTest(facts.Path, b.fileIDs)
	if production != "" {
		b.addEdge(Edge{From: b.fileIDs[facts.Path], To: b.fileIDs[production], Kind: EdgeTests, Weight: weightTests, Path: facts.Path, Evidence: "test file"})
	}
	for _, symbol := range b.symbolsByFile[facts.Path] {
		name := productionNameForTest(symbol.Name)
		if name == "" {
			continue
		}
		if target, ok := b.resolveSymbol(production, name); ok && target.ID != symbol.ID {
			b.addEdge(Edge{From: symbol.ID, To: target.ID, Kind: EdgeTests, Weight: weightTests, Path: facts.Path, Line: symbol.Line, Evidence: "test name"})
		}
	}
}

func (b *graphBuilder) resolveOwner(filePath, name string, line int) Node {
	if strings.TrimSpace(name) != "" {
		if node, ok := b.resolveSymbol(filePath, name); ok {
			return node
		}
	}
	var best Node
	for _, node := range b.symbolsByFile[filePath] {
		if line < node.Line || (node.EndLine > 0 && line > node.EndLine) {
			continue
		}
		if best.ID == "" || node.Line > best.Line || (node.Line == best.Line && node.EndLine < best.EndLine) {
			best = node
		}
	}
	if best.ID != "" {
		return best
	}
	return b.nodes[b.fileIDs[filePath]]
}

func (b *graphBuilder) resolveSymbol(filePath, reference string) (Node, bool) {
	reference = strings.TrimSpace(reference)
	normalized := normalizeName(reference)
	if normalized == "" {
		return Node{}, false
	}
	candidates := append([]Node(nil), b.symbolsByName[normalized]...)
	if len(candidates) == 0 {
		// Qualified call sites may use a package, type, namespace, or path
		// prefix that is absent from the declaration's short name. Resolve
		// that final component directly instead of scanning every symbol key
		// for each unresolved call.
		if short := normalizeName(referenceTail(reference)); short != "" && short != normalized {
			candidates = append(candidates, b.symbolsByName[short]...)
		}
	}
	if len(candidates) == 0 {
		return Node{}, false
	}
	imports := b.importsByFile[filePath]
	sort.SliceStable(candidates, func(i, j int) bool {
		left := symbolResolutionRank(candidates[i], filePath, imports, reference)
		right := symbolResolutionRank(candidates[j], filePath, imports, reference)
		if left != right {
			return left > right
		}
		return graphNodeLess(candidates[i], candidates[j])
	})
	if len(candidates) > 8 &&
		symbolResolutionRank(candidates[0], filePath, imports, reference) ==
			symbolResolutionRank(candidates[1], filePath, imports, reference) {
		// A highly ambiguous call is less useful than no inferred edge. Picking
		// the lexically first declaration creates a deterministic false hub.
		return Node{}, false
	}
	return candidates[0], true
}

func referenceTail(reference string) string {
	if index := strings.LastIndexAny(reference, ".:/\\#"); index >= 0 && index+1 < len(reference) {
		return reference[index+1:]
	}
	return reference
}

func (b *graphBuilder) addEdge(edge Edge) {
	if edge.From == "" || edge.To == "" || edge.From == edge.To {
		return
	}
	if edge.Weight <= 0 {
		edge.Weight = edgeWeight(edge.Kind)
	}
	key := strings.Join([]string{edge.From, edge.To, string(edge.Kind)}, "\x00")
	if previous, ok := b.edges[key]; ok {
		if previous.Weight > edge.Weight || (previous.Weight == edge.Weight && !edgeLess(edge, previous)) {
			return
		}
	}
	b.edges[key] = edge
}

func newGraphIndex(snapshot *Snapshot) *graphIndex {
	index := &graphIndex{
		nodes:      make(map[string]Node),
		byPath:     make(map[string][]string),
		byName:     make(map[string][]string),
		byNorm:     make(map[string][]string),
		outgoing:   make(map[string][]Edge),
		incoming:   make(map[string][]Edge),
		fileByPath: make(map[string]string),
	}
	if snapshot == nil {
		return index
	}
	for _, node := range snapshot.Nodes {
		index.nodes[node.ID] = node
		if node.Path != "" {
			index.byPath[cleanGraphPath(node.Path)] = append(index.byPath[cleanGraphPath(node.Path)], node.ID)
		}
		index.byName[node.Name] = append(index.byName[node.Name], node.ID)
		for _, value := range []string{node.Name, node.Qualified, node.Path, node.ID} {
			if normalized := normalizeName(value); normalized != "" {
				index.byNorm[normalized] = appendUnique(index.byNorm[normalized], node.ID)
			}
		}
		if node.Kind == NodeFile {
			index.fileByPath[cleanGraphPath(node.Path)] = node.ID
		}
	}
	for _, edge := range snapshot.Edges {
		if _, fromOK := index.nodes[edge.From]; !fromOK {
			continue
		}
		if _, toOK := index.nodes[edge.To]; !toOK {
			continue
		}
		index.outgoing[edge.From] = append(index.outgoing[edge.From], edge)
		index.incoming[edge.To] = append(index.incoming[edge.To], edge)
	}
	for key := range index.byPath {
		sort.Strings(index.byPath[key])
	}
	for key := range index.byName {
		sort.Strings(index.byName[key])
	}
	for key := range index.byNorm {
		sort.Strings(index.byNorm[key])
	}
	for key := range index.outgoing {
		sortEdges(index.outgoing[key])
	}
	for key := range index.incoming {
		sortEdges(index.incoming[key])
	}
	return index
}

func (g *graphIndex) node(id string) (Node, bool) {
	node, ok := g.nodes[id]
	return node, ok
}

func (g *graphIndex) neighbors(id string) []graphArc {
	arcs := make([]graphArc, 0, len(g.outgoing[id])+len(g.incoming[id]))
	for _, edge := range g.outgoing[id] {
		if node, ok := g.nodes[edge.To]; ok {
			arcs = append(arcs, graphArc{Edge: edge, Node: node, Direction: "out"})
		}
	}
	for _, edge := range g.incoming[id] {
		if node, ok := g.nodes[edge.From]; ok {
			arcs = append(arcs, graphArc{Edge: edge, Node: node, Direction: "in"})
		}
	}
	sort.SliceStable(arcs, func(i, j int) bool {
		if arcs[i].Edge.Weight != arcs[j].Edge.Weight {
			return arcs[i].Edge.Weight > arcs[j].Edge.Weight
		}
		if arcs[i].Direction != arcs[j].Direction {
			return arcs[i].Direction < arcs[j].Direction
		}
		if arcs[i].Edge.Kind != arcs[j].Edge.Kind {
			return arcs[i].Edge.Kind < arcs[j].Edge.Kind
		}
		return graphNodeLess(arcs[i].Node, arcs[j].Node)
	})
	return arcs
}

var importExtensions = [...]string{
	".go", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".py", ".rs",
	".java", ".kt", ".rb", ".sh", ".json", ".yaml", ".yml", ".toml",
}

type importPathFile struct {
	path                  string
	withoutExtension      string
	directory             string
	extension             string
	base                  string
	slashWithoutExtension string
	slashDirectory        string
	slashBase             string
}

type importPathResolver struct {
	files map[string]string
	index []importPathFile
}

func newImportPathResolver(files map[string]string) *importPathResolver {
	resolver := &importPathResolver{files: files, index: make([]importPathFile, 0, len(files))}
	for filePath := range files {
		extension := path.Ext(filePath)
		withoutExtension := strings.TrimSuffix(filePath, extension)
		directory := path.Dir(withoutExtension)
		base := path.Base(withoutExtension)
		resolver.index = append(resolver.index, importPathFile{
			path:                  filePath,
			withoutExtension:      withoutExtension,
			directory:             directory,
			extension:             extension,
			base:                  base,
			slashWithoutExtension: "/" + withoutExtension,
			slashDirectory:        "/" + directory,
			slashBase:             "/" + base,
		})
	}
	return resolver
}

func resolveImportPath(source, target string, files map[string]string) string {
	return newImportPathResolver(files).resolve(source, target)
}

func (resolver *importPathResolver) resolve(source, target string) string {
	target = strings.Trim(strings.TrimSpace(target), "\"'`")
	if target == "" {
		return ""
	}
	target = strings.TrimPrefix(target, "file://")
	target = strings.ReplaceAll(target, "\\", "/")
	target = strings.ReplaceAll(target, "::", "/")

	bases := make([]string, 0, 2)
	addBase := func(candidate string) {
		candidate = cleanGraphPath(candidate)
		if candidate != "" && !slicesContainsString(bases, candidate) {
			bases = append(bases, candidate)
		}
	}
	if strings.HasPrefix(target, ".") {
		addBase(path.Join(path.Dir(source), target))
	} else {
		addBase(target)
		addBase(strings.ReplaceAll(target, ".", "/"))
	}
	for _, candidate := range bases {
		if _, ok := resolver.files[candidate]; ok {
			return candidate
		}
	}
	for _, candidate := range bases {
		if path.Ext(candidate) != "" {
			continue
		}
		for _, extension := range importExtensions {
			if resolved := resolver.exact(candidate + extension); resolved != "" {
				return resolved
			}
		}
		base := path.Base(candidate)
		for _, extension := range importExtensions {
			if resolved := resolver.exact(candidate + "/index" + extension); resolved != "" {
				return resolved
			}
			if resolved := resolver.exact(candidate + "/" + base + extension); resolved != "" {
				return resolved
			}
		}
	}

	// Package imports (Go modules, Python packages, and JS aliases) are
	// resolved by the longest matching path suffix, with stable tie-breaking.
	targetPath := strings.Trim(strings.ReplaceAll(target, ".", "/"), "/")
	targetDirectory := path.Dir(targetPath)
	targetBase := path.Base(targetPath)
	slashTargetPath := "/" + targetPath
	sourceDirectory := path.Dir(source)
	sourceExtension := path.Ext(source)
	bestPath := ""
	bestScore := 0
	for _, candidate := range resolver.index {
		score := 0
		switch {
		case candidate.withoutExtension == targetPath:
			score = 1000 + len(targetPath)
		case strings.HasSuffix(targetPath, candidate.slashWithoutExtension):
			score = 950 + len(candidate.withoutExtension)
		case strings.HasSuffix(candidate.withoutExtension, slashTargetPath):
			score = 900 + len(targetPath)
		case candidate.directory == targetPath:
			score = 850 + len(targetPath)
		case strings.HasSuffix(targetPath, candidate.slashDirectory):
			score = 825 + len(candidate.directory)
		case strings.HasSuffix(candidate.directory, slashTargetPath):
			score = 800 + len(targetPath)
		case strings.HasSuffix(targetDirectory, candidate.slashBase):
			score = 700 + len(candidate.base)
		case candidate.base == targetBase:
			score = 100
		}
		if score == 0 {
			continue
		}
		if candidate.extension == sourceExtension {
			score += 150
		}
		score += commonDirectoryPrefix(sourceDirectory, candidate.directory) * 20
		if score > bestScore || (score == bestScore && (bestPath == "" || candidate.path < bestPath)) {
			bestPath = candidate.path
			bestScore = score
		}
	}
	return bestPath
}

func (resolver *importPathResolver) exact(candidate string) string {
	if _, ok := resolver.files[candidate]; ok {
		return candidate
	}
	return ""
}

func slicesContainsString(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func commonDirectoryPrefix(left, right string) int {
	count := 0
	for {
		leftComponent, leftRest, leftFound := strings.Cut(left, "/")
		rightComponent, rightRest, rightFound := strings.Cut(right, "/")
		if leftComponent != rightComponent {
			return count
		}
		count++
		if !leftFound || !rightFound {
			return count
		}
		left = leftRest
		right = rightRest
	}
}

func sortedFactPaths(facts map[string]FileFacts) []string {
	paths := make([]string, 0, len(facts))
	for key := range facts {
		paths = append(paths, key)
	}
	sort.Strings(paths)
	return paths
}

func sortNodes(nodes []Node) {
	sort.SliceStable(nodes, func(i, j int) bool { return graphNodeLess(nodes[i], nodes[j]) })
}

func preferredGraphNode(left, right Node) Node {
	if left.Test != right.Test {
		if left.Test {
			return right
		}
		return left
	}
	if graphNodeLess(left, right) {
		return left
	}
	return right
}

func graphNodeLess(left, right Node) bool {
	if left.Path != right.Path {
		return left.Path < right.Path
	}
	if left.Line != right.Line {
		return left.Line < right.Line
	}
	if left.Kind != right.Kind {
		return left.Kind < right.Kind
	}
	if left.Qualified != right.Qualified {
		return left.Qualified < right.Qualified
	}
	if left.Name != right.Name {
		return left.Name < right.Name
	}
	return left.ID < right.ID
}

func sortEdges(edges []Edge) {
	sort.SliceStable(edges, func(i, j int) bool { return edgeLess(edges[i], edges[j]) })
}

func edgeLess(left, right Edge) bool {
	if left.From != right.From {
		return left.From < right.From
	}
	if left.To != right.To {
		return left.To < right.To
	}
	if left.Kind != right.Kind {
		return left.Kind < right.Kind
	}
	if left.Path != right.Path {
		return left.Path < right.Path
	}
	if left.Line != right.Line {
		return left.Line < right.Line
	}
	return left.Evidence < right.Evidence
}

func edgeWeight(kind EdgeKind) int {
	switch kind {
	case EdgeContains:
		return weightContains
	case EdgeImports:
		return weightImports
	case EdgeCalls:
		return weightCalls
	case EdgeTests:
		return weightTests
	case EdgeRoutes:
		return weightRoutes
	case EdgeShares:
		return weightShares
	case EdgeInherits:
		return weightInherits
	default:
		return 1
	}
}

func inferredCallTargetRank(node, nested, caller Node) int {
	if node.ID == nested.ID || node.ID == caller.ID || node.Name != nested.Name {
		return 0
	}
	rank := 1
	if node.Path == nested.Path {
		rank += 20
	}
	if node.Line < caller.Line || node.Line > caller.EndLine {
		rank += 20
	}
	switch strings.ToLower(node.Symbol) {
	case "function", "method", "func":
		rank += 20
	}
	if node.EndLine > node.Line {
		rank += 10
	}
	return rank
}

func symbolResolutionRank(node Node, source string, imports []string, reference string) int {
	rank := 0
	if node.Path == source {
		rank += 1000
	}
	for _, imported := range imports {
		if node.Path == imported {
			rank += 1200
			break
		}
	}
	if node.Name == reference || node.Qualified == reference {
		rank += 200
	} else if strings.EqualFold(node.Name, reference) || strings.EqualFold(node.Qualified, reference) {
		rank += 100
	}
	if node.Test {
		rank -= 10
	}
	return rank
}

func cleanGraphPath(value string) string {
	// Repository-relative paths may legally contain backslashes on Unix or
	// begin or end with whitespace. Discovery already normalizes platform
	// separators, so preserve the remaining path identity here.
	value = strings.TrimPrefix(value, "./")
	if value == "" {
		return ""
	}
	cleaned := path.Clean(value)
	if cleaned == "." {
		return ""
	}
	return strings.TrimPrefix(cleaned, "/")
}

func canonicalLiteral(kind, value string) string {
	value = strings.TrimSpace(value)
	if kind == "path" {
		if route := normalizeRoute(value); route != "" {
			return route
		}
	}
	if kind == "env" {
		return strings.ToUpper(value)
	}
	return strings.ToLower(value)
}

func routeMatchesLiteral(routePath, literal string) bool {
	literalPath := normalizeRoute(literal)
	if routePath == "" || literalPath == "" {
		return false
	}
	routeParts := strings.Split(strings.Trim(routePath, "/"), "/")
	literalParts := strings.Split(strings.Trim(literalPath, "/"), "/")
	if len(routeParts) != len(literalParts) {
		return false
	}
	for index := range routeParts {
		if routeParts[index] != "{*}" && routeParts[index] != literalParts[index] {
			return false
		}
	}
	return true
}

func routeName(method, routePath string) string {
	if method == "" {
		return routePath
	}
	return method + " " + routePath
}

func isTestPath(filePath string) bool {
	lower := strings.ToLower(strings.ReplaceAll(filePath, "\\", "/"))
	base := path.Base(lower)
	return strings.HasSuffix(base, "_test.go") || strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") || strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py") || strings.Contains(lower, "/test/") || strings.Contains(lower, "/tests/") || strings.Contains(lower, "__tests__")
}

func isTestSymbol(name, kind string) bool {
	lower := strings.ToLower(name)
	return strings.HasPrefix(lower, "test") || strings.Contains(strings.ToLower(kind), "test")
}

func productionNameForTest(name string) string {
	for _, prefix := range []string{"Test", "test_", "test"} {
		if strings.HasPrefix(name, prefix) && len(name) > len(prefix) {
			result := strings.TrimLeft(name[len(prefix):], "_")
			if result != "" {
				return result
			}
		}
	}
	return ""
}

func productionPathForTest(testPath string, files map[string]string) string {
	candidates := []string{
		strings.TrimSuffix(testPath, "_test.go") + ".go",
		strings.Replace(testPath, ".test.", ".", 1),
		strings.Replace(testPath, ".spec.", ".", 1),
	}
	base := path.Base(testPath)
	if strings.HasPrefix(base, "test_") {
		candidates = append(candidates, path.Join(path.Dir(testPath), strings.TrimPrefix(base, "test_")))
	}
	for _, candidate := range candidates {
		if candidate != testPath {
			if _, ok := files[candidate]; ok {
				return candidate
			}
		}
	}
	return ""
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func appendNodeUnique(nodes []Node, node Node) []Node {
	for index, existing := range nodes {
		if existing.ID == node.ID {
			nodes[index] = node
			return nodes
		}
	}
	return append(nodes, node)
}

func positiveLine(line int) int {
	if line < 1 {
		return 1
	}
	return line
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	position := len(digits)
	for value > 0 {
		position--
		digits[position] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[position:])
}
