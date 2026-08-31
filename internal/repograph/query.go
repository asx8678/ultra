package repograph

import (
	"container/heap"
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	defaultQueryTokens = 1200
	maxFocusDepth      = 3
	maxDwellDepth      = 8
	maxImpactDepth     = 4
	maxDwellSessions   = 256
	dwellSessionTTL    = 30 * time.Minute
)

// dwellState owns progressive result cursors. A Manager can keep one of these
// per instance; the mutex allows concurrent sessions without global state.
type dwellState struct {
	mu       sync.Mutex
	sessions map[string]dwellCursor
	now      func() time.Time
}

type dwellCursor struct {
	generation uint64
	query      string
	scope      Scope
	hits       []Hit
	position   int
	depth      int
	disclosed  map[string]struct{}
	touchedAt  time.Time
}

func newDwellState() *dwellState {
	return &dwellState{sessions: make(map[string]dwellCursor), now: time.Now}
}

func (state *dwellState) reset(sessionID string) {
	if state == nil {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	delete(state.sessions, dwellSessionKey(sessionID))
}

func (state *dwellState) resetAll() {
	if state == nil {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	state.sessions = make(map[string]dwellCursor)
}

func (state *dwellState) cursor(sessionID string) (dwellCursor, bool) {
	if state == nil {
		return dwellCursor{}, false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	now := state.timeNow()
	state.pruneLocked(now)
	cursor, ok := state.sessions[dwellSessionKey(sessionID)]
	if !ok {
		return dwellCursor{}, false
	}
	// Callers use this accessor only to validate/restart the cursor. Avoid
	// copying the potentially large progressive tail while holding the lock;
	// dwellSnapshotContext owns the full cursor update path.
	cursor.hits = nil
	cursor.disclosed = nil
	return cursor, true
}

func (state *dwellState) timeNow() time.Time {
	if state.now == nil {
		return time.Now()
	}
	return state.now()
}

func (state *dwellState) pruneLocked(now time.Time) {
	for key, cursor := range state.sessions {
		if now.Sub(cursor.touchedAt) >= dwellSessionTTL {
			delete(state.sessions, key)
		}
	}
}

func (state *dwellState) makeRoomLocked(key string) {
	if _, exists := state.sessions[key]; exists {
		return
	}
	for len(state.sessions) >= maxDwellSessions {
		oldestKey := ""
		var oldest time.Time
		for candidate, cursor := range state.sessions {
			if oldestKey == "" || cursor.touchedAt.Before(oldest) ||
				(cursor.touchedAt.Equal(oldest) && candidate < oldestKey) {
				oldestKey = candidate
				oldest = cursor.touchedAt
			}
		}
		delete(state.sessions, oldestKey)
	}
}

// snapshotSketch summarizes graph hubs and architectural boundaries without a
// query. The returned order is stable for an identical snapshot.
func snapshotSketch(snapshot *Snapshot, maxTokens int) Result {
	maxTokens = normalizedTokenBudget(maxTokens)
	index := ensureGraph(snapshot)
	type scoredNode struct {
		node  Node
		score int64
	}
	scored := make([]scoredNode, 0, len(index.nodes))
	for _, node := range index.nodes {
		var score int64
		for _, edge := range index.outgoing[node.ID] {
			score += int64(edge.Weight)
		}
		for _, edge := range index.incoming[node.ID] {
			score += int64(edge.Weight)
		}
		switch node.Kind {
		case NodeFile:
			score += 250
		case NodeRoute:
			score += 220
		case NodeSymbol:
			score += 100
		}
		scored = append(scored, scoredNode{node: node, score: score})
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].node.Test != scored[j].node.Test {
			return !scored[i].node.Test
		}
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return graphNodeLess(scored[i].node, scored[j].node)
	})
	limit := resultHitLimit(maxTokens)
	if len(scored) < limit {
		limit = len(scored)
	}
	hits := make([]Hit, 0, limit)
	for _, item := range scored[:limit] {
		hits = append(hits, queryHitForNode(item.node, item.score))
	}
	return renderGraphResult("sketch", snapshot, hits, suggestedReadWindows(hits), 0, maxTokens, nil)
}

// sketchSnapshot is retained as a natural call site for a Manager.
func sketchSnapshot(snapshot *Snapshot, maxTokens int) Result {
	return snapshotSketch(snapshot, maxTokens)
}

// focusSnapshot resolves a query, expands weighted graph relationships, and
// records only model-visible disclosure for later dwell calls.
func focusSnapshot(snapshot *Snapshot, options FocusOptions, state *dwellState) Result {
	result, _ := focusSnapshotContext(context.Background(), snapshot, options, state)
	return result
}

func focusSnapshotContext(ctx context.Context, snapshot *Snapshot, options FocusOptions, state *dwellState) (Result, error) {
	maxTokens := normalizedTokenBudget(options.MaxTokens)
	index := ensureGraph(snapshot)
	warnings := make([]string, 0, 1)
	seeds, err := resolveFocusContext(ctx, index, options.Query, options.Scope)
	if err != nil {
		return Result{}, err
	}
	if len(seeds) == 0 {
		warnings = append(warnings, "No graph node matched the focus query.")
	}
	hits, err := expandFocusBoundedContext(
		ctx, index, seeds, options.Scope, maxFocusDepth, expansionNodeLimit(maxTokens),
	)
	if err != nil {
		return Result{}, err
	}

	generation := uint64(0)
	if snapshot != nil {
		generation = snapshot.Generation
	}
	if state == nil {
		visible := prefixHits(hits, resultHitLimit(maxTokens))
		result := renderGraphResult(
			"focus", snapshot, visible, suggestedReadWindows(visible),
			hitDepth(visible), maxTokens, warnings,
		)
		result.Meta.Scope = options.Scope
		return result, nil
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if state.sessions == nil {
		state.sessions = make(map[string]dwellCursor)
	}
	now := state.timeNow()
	state.pruneLocked(now)
	key := dwellSessionKey(options.SessionID)
	if options.Fresh {
		delete(state.sessions, key)
	}
	disclosed := make(map[string]struct{})
	if previous, ok := state.sessions[key]; ok &&
		previous.generation == generation && previous.query == options.Query && previous.scope == options.Scope {
		disclosed = cloneStringSet(previous.disclosed)
	}
	undisclosed := filterUndisclosedHits(hits, disclosed)
	visible := prefixHits(undisclosed, resultHitLimit(maxTokens))
	result := renderGraphResult(
		"focus", snapshot, visible, suggestedReadWindows(visible),
		hitDepth(visible), maxTokens, warnings,
	)
	result.Meta.Scope = options.Scope

	if len(seeds) == 0 {
		delete(state.sessions, key)
		return result, nil
	}
	for _, hit := range result.Hits {
		disclosed[hit.NodeID] = struct{}{}
	}
	state.makeRoomLocked(key)
	state.sessions[key] = dwellCursor{
		generation: generation,
		query:      options.Query,
		scope:      options.Scope,
		hits:       append([]Hit(nil), undisclosed...),
		position:   len(result.Hits),
		depth:      maxFocusDepth,
		disclosed:  disclosed,
		touchedAt:  now,
	}
	return result, nil
}

// dwellSnapshot first drains undisclosed results at the current depth, then
// widens the semantic neighborhood one graph hop at a time.
func dwellSnapshot(snapshot *Snapshot, sessionID string, maxTokens int, state *dwellState) Result {
	result, _ := dwellSnapshotContext(context.Background(), snapshot, sessionID, maxTokens, state)
	return result
}

func dwellSnapshotContext(ctx context.Context, snapshot *Snapshot, sessionID string, maxTokens int, state *dwellState) (Result, error) {
	maxTokens = normalizedTokenBudget(maxTokens)
	warnings := make([]string, 0, 2)
	if state == nil {
		result := renderGraphResult("dwell", snapshot, nil, nil, 0, maxTokens,
			[]string{"No dwell state is available; run focus first."})
		return result, nil
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	now := state.timeNow()
	state.pruneLocked(now)
	key := dwellSessionKey(sessionID)
	cursor, ok := state.sessions[key]
	generation := uint64(0)
	if snapshot != nil {
		generation = snapshot.Generation
	}
	if !ok {
		result := renderGraphResult("dwell", snapshot, nil, nil, 0, maxTokens,
			[]string{"No dwell state is available; run focus first."})
		return result, nil
	}
	if cursor.generation != generation {
		delete(state.sessions, key)
		result := renderGraphResult("dwell", snapshot, nil, nil, 0, maxTokens,
			[]string{"The graph changed; run focus again."})
		result.Meta.Scope = cursor.scope
		return result, nil
	}
	cursor.hits = append([]Hit(nil), cursor.hits...)
	cursor.disclosed = cloneStringSet(cursor.disclosed)
	index := ensureGraph(snapshot)
	widened := false
	for cursor.position >= len(cursor.hits) && cursor.depth < maxDwellDepth {
		cursor.depth++
		seeds, err := resolveFocusContext(ctx, index, cursor.query, cursor.scope)
		if err != nil {
			return Result{}, err
		}
		hits, err := expandFocusBoundedContext(
			ctx, index, seeds, cursor.scope, cursor.depth, expansionNodeLimit(maxTokens),
		)
		if err != nil {
			return Result{}, err
		}
		cursor.hits = filterUndisclosedHits(hits, cursor.disclosed)
		cursor.position = 0
		widened = true
		if len(cursor.hits) > 0 {
			break
		}
	}
	if widened {
		warnings = append(warnings, fmt.Sprintf("Dwell widened focus to graph depth %d.", cursor.depth))
	}

	end := minInt(len(cursor.hits), cursor.position+resultHitLimit(maxTokens))
	visible := append([]Hit(nil), cursor.hits[cursor.position:end]...)
	if len(visible) == 0 && cursor.depth >= maxDwellDepth {
		warnings = append(warnings, "No further related nodes remain.")
	}
	result := renderGraphResult(
		"dwell", snapshot, visible, suggestedReadWindows(visible),
		cursor.depth, maxTokens, warnings,
	)
	result.Meta.Scope = cursor.scope
	cursor.position += len(result.Hits)
	for _, hit := range result.Hits {
		cursor.disclosed[hit.NodeID] = struct{}{}
	}
	cursor.touchedAt = now
	state.sessions[key] = cursor
	return result, nil
}

// impactSnapshot ranks direct and transitive consumers of changed files or
// symbols. Incoming dependencies are favored, while shared routes and literals
// permit deterministic cross-language joins.
func impactSnapshot(snapshot *Snapshot, options ImpactOptions) Result {
	result, _ := impactSnapshotContext(context.Background(), snapshot, options)
	return result
}

func impactSnapshotContext(ctx context.Context, snapshot *Snapshot, options ImpactOptions) (Result, error) {
	maxTokens := normalizedTokenBudget(options.MaxTokens)
	index := ensureGraph(snapshot)
	seeds, err := resolveImpactSeedsContext(ctx, index, options.Files, options.Symbols, options.cochanges)
	if err != nil {
		return Result{}, err
	}
	warnings := append([]string(nil), options.warnings...)
	if len(seeds) == 0 {
		warnings = append(warnings, "No changed file or symbol matched the graph.")
	}
	hits, err := expandImpactBoundedContext(
		ctx, index, seeds, maxImpactDepth, expansionNodeLimit(maxTokens),
	)
	if err != nil {
		return Result{}, err
	}
	visible := prefixHits(hits, resultHitLimit(maxTokens))
	return renderGraphResult("impact", snapshot, visible, suggestedReadWindows(visible), maxImpactDepth, maxTokens, warnings), nil
}

// ensureGraph requires the snapshot to be fully materialized before
// publication (manager refresh and load paths build the graph eagerly).
// Publishing a facts-only snapshot and querying it concurrently would race
// here; manager construction must uphold the invariant instead.
func ensureGraph(snapshot *Snapshot) *graphIndex {
	if snapshot == nil {
		return newGraphIndex(nil)
	}
	if snapshot.index != nil {
		return snapshot.index
	}
	if len(snapshot.Nodes) == 0 && len(snapshot.Facts) > 0 {
		return buildGraph(snapshot)
	}
	snapshot.index = newGraphIndex(snapshot)
	return snapshot.index
}

type resolvedSeed struct {
	node     Node
	score    int64
	expose   bool
	relation EdgeKind
}

func resolveFocus(index *graphIndex, query string, scope Scope) []resolvedSeed {
	seeds, _ := resolveFocusContext(context.Background(), index, query, scope)
	return seeds
}

func resolveFocusContext(ctx context.Context, index *graphIndex, query string, scope Scope) ([]resolvedSeed, error) {
	query = normalizeFocusQuery(query)
	if query == "" {
		return nil, nil
	}
	normalizedQuery := normalizeName(query)
	if normalizedQuery != "" {
		exact := make([]resolvedSeed, 0, len(index.byNorm[normalizedQuery]))
		for _, nodeID := range index.byNorm[normalizedQuery] {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			node, ok := index.nodes[nodeID]
			if !ok || !queryNodeInScope(node, scope) {
				continue
			}
			score := focusMatchScore(node, query, normalizedQuery, nil)
			if score >= 3_400_000 {
				exact = append(exact, resolvedSeed{node: node, score: score})
			}
		}
		if len(exact) > 0 {
			sort.Slice(exact, func(i, j int) bool {
				if exact[i].score != exact[j].score {
					return exact[i].score > exact[j].score
				}
				return graphNodeLess(exact[i].node, exact[j].node)
			})
			if len(exact) > 64 {
				exact = exact[:64]
			}
			return exact, nil
		}
	}

	terms := queryTerms(query)
	byID := make(map[string]resolvedSeed)
	for _, node := range index.nodes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !queryNodeInScope(node, scope) {
			continue
		}
		score := focusMatchScore(node, query, normalizedQuery, terms)
		if score <= 0 {
			continue
		}
		byID[node.ID] = resolvedSeed{node: node, score: score}
	}
	seeds := make([]resolvedSeed, 0, len(byID))
	for _, seed := range byID {
		seeds = append(seeds, seed)
	}
	sort.Slice(seeds, func(i, j int) bool {
		if seeds[i].score != seeds[j].score {
			return seeds[i].score > seeds[j].score
		}
		return graphNodeLess(seeds[i].node, seeds[j].node)
	})
	// Resolution is tiered: an exact or normalized-exact match suppresses
	// incidental substring and fuzzy matches. Ambiguous matches within the
	// winning tier remain available for graph expansion.
	exactTier := len(seeds) > 0 && seeds[0].score >= 3_400_000
	if exactTier {
		cutoff := 0
		for cutoff < len(seeds) && seeds[cutoff].score >= 3_400_000 {
			cutoff++
		}
		seeds = seeds[:cutoff]
	}
	limit := 12
	if exactTier {
		// Preserve realistic duplicate declarations and overloads. The output
		// budget still bounds disclosure, while retaining the complete exact
		// tier prevents lexical file order from hiding the intended symbol.
		limit = 64
	}
	if len(seeds) > limit {
		seeds = seeds[:limit]
	}
	return seeds, nil
}

func normalizeFocusQuery(query string) string {
	query = strings.TrimSpace(query)
	fields := strings.Fields(query)
	if len(fields) == 2 && isHTTPMethod(fields[0]) {
		if route := normalizeRoute(fields[1]); route != "" {
			return strings.ToUpper(fields[0]) + " " + route
		}
	}
	if route := normalizeRoute(query); route != "" {
		return route
	}
	return query
}

func focusMatchScore(node Node, query, normalizedQuery string, terms []string) int64 {
	values := []string{node.Name, node.Qualified, node.Path, node.ID}
	for _, value := range values {
		if value == "" {
			continue
		}
		if value == query {
			return 4_000_000
		}
	}
	for _, value := range values {
		if value != "" && strings.EqualFold(value, query) {
			return 3_800_000
		}
	}
	if normalizedQuery == "" {
		return 0
	}
	best := int64(0)
	for _, value := range values {
		normalizedValue := normalizeName(value)
		if normalizedValue == "" {
			continue
		}
		switch {
		case normalizedValue == normalizedQuery:
			best = maxInt64(best, 3_400_000)
		case strings.Contains(normalizedValue, normalizedQuery):
			best = maxInt64(best, 2_400_000-int64(len(normalizedValue)-len(normalizedQuery))*1000)
		case len(normalizedValue) >= 4 && strings.Contains(normalizedQuery, normalizedValue):
			best = maxInt64(best, 2_100_000-int64(len(normalizedQuery)-len(normalizedValue))*1000)
		}
	}
	if best > 0 {
		return best
	}
	matchedTerms := 0
	for _, term := range terms {
		for _, value := range values {
			if strings.Contains(normalizeName(value), term) {
				matchedTerms++
				break
			}
		}
	}
	if matchedTerms > 0 {
		if matchedTerms == len(terms) {
			return 1_800_000 + int64(matchedTerms*10_000)
		}
		// Natural-language queries often include intent words (for example,
		// "callers of ProcessOrder"). Preserve the semantic term as a
		// lower-ranked substring match instead of forcing a fuzzy whole-query
		// comparison.
		return 1_400_000 + int64(matchedTerms*10_000)
	}

	for _, value := range []string{node.Name, node.Qualified} {
		normalizedValue := normalizeName(value)
		if normalizedValue == "" || absInt(len(normalizedValue)-len(normalizedQuery)) > maxInt(3, len(normalizedQuery)/2) {
			continue
		}
		distance := levenshtein(normalizedValue, normalizedQuery)
		longest := maxInt(len(normalizedValue), len(normalizedQuery))
		if distance <= 3 && distance*100 <= longest*40 {
			score := int64(1_200_000 - distance*100_000 - absInt(len(normalizedValue)-len(normalizedQuery))*10_000)
			best = maxInt64(best, score)
		}
	}
	return best
}

type expansionItem struct {
	nodeID    string
	score     int64
	depth     int
	relation  EdgeKind
	direction string
	via       []EdgeKind
	order     int
}

type expansionQueue []expansionItem

func (queue expansionQueue) Len() int { return len(queue) }
func (queue expansionQueue) Less(i, j int) bool {
	if queue[i].score != queue[j].score {
		return queue[i].score > queue[j].score
	}
	if queue[i].depth != queue[j].depth {
		return queue[i].depth < queue[j].depth
	}
	if queue[i].nodeID != queue[j].nodeID {
		return queue[i].nodeID < queue[j].nodeID
	}
	return queue[i].order < queue[j].order
}
func (queue expansionQueue) Swap(i, j int) { queue[i], queue[j] = queue[j], queue[i] }
func (queue *expansionQueue) Push(value any) {
	*queue = append(*queue, value.(expansionItem))
}

func (queue *expansionQueue) Pop() any {
	old := *queue
	last := old[len(old)-1]
	*queue = old[:len(old)-1]
	return last
}

func expandFocusContext(ctx context.Context, index *graphIndex, seeds []resolvedSeed, scope Scope, maxDepth int) ([]Hit, error) {
	return expandFocusBoundedContext(ctx, index, seeds, scope, maxDepth, 8_192)
}

func expandFocusBoundedContext(ctx context.Context, index *graphIndex, seeds []resolvedSeed, _ Scope, maxDepth, maxVisited int) ([]Hit, error) {
	queue := make(expansionQueue, 0, len(seeds)*2)
	best := make(map[string]int64)
	order := 0
	for _, seed := range seeds {
		heap.Push(&queue, expansionItem{nodeID: seed.node.ID, score: seed.score, order: order})
		order++
	}
	results := make(map[string]Hit)
	visited := 0
	for queue.Len() > 0 && visited < maxVisited {
		visited++
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		item := heap.Pop(&queue).(expansionItem)
		if previous, ok := best[item.nodeID]; ok && previous >= item.score {
			continue
		}
		best[item.nodeID] = item.score
		node, ok := index.node(item.nodeID)
		if !ok {
			continue
		}
		hit := queryHitForNode(node, item.score)
		hit.Relation = item.relation
		hit.Direction = item.direction
		hit.Via = append([]EdgeKind(nil), item.via...)
		results[node.ID] = hit
		if item.depth >= maxDepth {
			continue
		}
		for _, arc := range index.neighbors(item.nodeID) {
			nextScore := item.score * int64(maxInt(1, arc.Edge.Weight)) / 125
			nextScore = nextScore * int64(maxInt(1, maxDepth-item.depth)) / int64(maxDepth)
			if nextScore < 1000 || best[arc.Node.ID] >= nextScore {
				continue
			}
			via := appendCopy(item.via, arc.Edge.Kind)
			relation := item.relation
			direction := item.direction
			if relation == "" {
				relation = arc.Edge.Kind
				direction = arc.Direction
			}
			heap.Push(&queue, expansionItem{nodeID: arc.Node.ID, score: nextScore, depth: item.depth + 1, relation: relation, direction: direction, via: via, order: order})
			order++
		}
	}
	return sortedHits(results), nil
}

func resolveImpactSeedsContext(ctx context.Context, index *graphIndex, files, symbols []string, cochanges map[string]int64) ([]resolvedSeed, error) {
	seeds := make(map[string]resolvedSeed)
	for _, requested := range files {
		requested = cleanGraphPath(requested)
		for filePath, id := range index.fileByPath {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			score := int64(0)
			switch {
			case filePath == requested:
				score = 4_000_000
			case strings.EqualFold(filePath, requested):
				score = 3_800_000
			case strings.HasSuffix(filePath, "/"+requested) || strings.HasSuffix(requested, "/"+filePath):
				score = 3_000_000
			}
			if score > seeds[id].score {
				seeds[id] = resolvedSeed{node: index.nodes[id], score: score}
			}
		}
	}
	for _, requested := range symbols {
		resolved, err := resolveFocusContext(ctx, index, requested, Scope{Kind: string(NodeSymbol)})
		if err != nil {
			return nil, err
		}
		for _, seed := range resolved {
			if seed.score > seeds[seed.node.ID].score {
				seeds[seed.node.ID] = seed
			}
		}
	}
	for filePath, historicalScore := range cochanges {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id, ok := index.fileByPath[cleanGraphPath(filePath)]
		if !ok {
			continue
		}
		score := int64(1_500_000) + historicalScore
		if score > seeds[id].score {
			seeds[id] = resolvedSeed{
				node: index.nodes[id], score: score, expose: true, relation: EdgeCoChanges,
			}
		}
	}
	result := make([]resolvedSeed, 0, len(seeds))
	for _, seed := range seeds {
		result = append(result, seed)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].score != result[j].score {
			return result[i].score > result[j].score
		}
		return graphNodeLess(result[i].node, result[j].node)
	})
	return result, nil
}

func expandImpactContext(ctx context.Context, index *graphIndex, seeds []resolvedSeed, maxDepth int) ([]Hit, error) {
	return expandImpactBoundedContext(ctx, index, seeds, maxDepth, 8_192)
}

func expandImpactBoundedContext(ctx context.Context, index *graphIndex, seeds []resolvedSeed, maxDepth, maxVisited int) ([]Hit, error) {
	queue := make(expansionQueue, 0, len(seeds))
	seedIDs := make(map[string]struct{}, len(seeds))
	exposedSeeds := make(map[string]EdgeKind, len(seeds))
	seedPaths := make(map[string]struct{}, len(seeds))
	order := 0
	for _, seed := range seeds {
		seedIDs[seed.node.ID] = struct{}{}
		if seed.expose {
			exposedSeeds[seed.node.ID] = seed.relation
		} else if seed.node.Kind == NodeFile && seed.node.Path != "" {
			seedPaths[seed.node.Path] = struct{}{}
		}
		heap.Push(&queue, expansionItem{nodeID: seed.node.ID, score: seed.score, order: order})
		order++
	}
	best := make(map[string]int64)
	results := make(map[string]Hit)
	visited := 0
	for queue.Len() > 0 && visited < maxVisited {
		visited++
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		item := heap.Pop(&queue).(expansionItem)
		if previous, ok := best[item.nodeID]; ok && previous >= item.score {
			continue
		}
		best[item.nodeID] = item.score
		node, ok := index.node(item.nodeID)
		if !ok {
			continue
		}
		_, seed := seedIDs[item.nodeID]
		seedRelation, exposedSeed := exposedSeeds[item.nodeID]
		_, insideSeedFile := seedPaths[node.Path]
		if (!seed || exposedSeed) && !insideSeedFile {
			hit := queryHitForNode(node, item.score)
			hit.Relation = item.relation
			hit.Direction = item.direction
			if exposedSeed {
				hit.Relation = seedRelation
				hit.Direction = "historical"
			}
			hit.Via = append([]EdgeKind(nil), item.via...)
			results[node.ID] = hit
		}
		if item.depth >= maxDepth {
			continue
		}
		for _, arc := range index.neighbors(item.nodeID) {
			factor := impactArcFactor(arc)
			if factor == 0 {
				continue
			}
			nextScore := item.score * int64(arc.Edge.Weight) * int64(factor) / 10_000
			nextScore = nextScore * int64(maxDepth-item.depth) / int64(maxDepth)
			if nextScore < 1000 || best[arc.Node.ID] >= nextScore {
				continue
			}
			via := appendCopy(item.via, arc.Edge.Kind)
			relation := item.relation
			direction := item.direction
			if relation == "" {
				relation = arc.Edge.Kind
				direction = arc.Direction
			}
			heap.Push(&queue, expansionItem{nodeID: arc.Node.ID, score: nextScore, depth: item.depth + 1, relation: relation, direction: direction, via: via, order: order})
			order++
		}
	}
	return sortedHits(results), nil
}

func impactArcFactor(arc graphArc) int {
	switch arc.Edge.Kind {
	case EdgeCalls, EdgeImports:
		if arc.Direction == "in" {
			return 125
		}
		return 35
	case EdgeTests:
		if arc.Direction == "in" {
			return 140
		}
		return 45
	case EdgeInherits:
		if arc.Direction == "in" {
			return 130
		}
		return 75
	case EdgeRoutes, EdgeShares:
		return 105
	case EdgeContains:
		return 65
	default:
		return 25
	}
}

func sortedHits(results map[string]Hit) []Hit {
	hits := make([]Hit, 0, len(results))
	for _, hit := range results {
		hits = append(hits, hit)
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if hits[i].Path != hits[j].Path {
			return hits[i].Path < hits[j].Path
		}
		if hits[i].Line != hits[j].Line {
			return hits[i].Line < hits[j].Line
		}
		if hits[i].Kind != hits[j].Kind {
			return hits[i].Kind < hits[j].Kind
		}
		if hits[i].Name != hits[j].Name {
			return hits[i].Name < hits[j].Name
		}
		return hits[i].NodeID < hits[j].NodeID
	})
	return hits
}

func queryHitForNode(node Node, score int64) Hit {
	return Hit{
		NodeID: node.ID, Name: node.Name, Kind: node.Kind, Path: node.Path,
		Line: node.Line, EndLine: node.EndLine, Language: node.Language, Score: score,
	}
}

func suggestedReadWindows(hits []Hit) []ReadWindow {
	windows := make([]ReadWindow, 0, minInt(len(hits), 10))
	for _, hit := range hits {
		if hit.Path == "" || hit.Kind == NodeRoute || hit.Kind == NodeLiteral {
			continue
		}
		start := hit.Line
		end := hit.EndLine
		if start <= 0 {
			start = 1
		}
		if end < start {
			end = start
		}
		start = maxInt(1, start-3)
		end += 3
		if end-start+1 > 200 {
			end = start + 199
		}
		merged := false
		for index := range windows {
			existing := &windows[index]
			if existing.Path != hit.Path || start > existing.EndLine+8 || end+8 < existing.StartLine {
				continue
			}
			existing.StartLine = minInt(existing.StartLine, start)
			existing.EndLine = maxInt(existing.EndLine, end)
			if existing.EndLine-existing.StartLine+1 > 200 {
				existing.EndLine = existing.StartLine + 199
			}
			existing.Offset = existing.StartLine - 1
			existing.Limit = existing.EndLine - existing.StartLine + 1
			merged = true
			break
		}
		if merged {
			continue
		}
		windows = append(windows, ReadWindow{Path: hit.Path, StartLine: start, EndLine: end, Offset: start - 1, Limit: end - start + 1})
		if len(windows) == 10 {
			break
		}
	}
	return windows
}

func queryNodeInScope(node Node, scope Scope) bool {
	if scope.Path != "" {
		scopePath := cleanGraphPath(scope.Path)
		if node.Path != scopePath && !strings.HasPrefix(node.Path, strings.TrimSuffix(scopePath, "/")+"/") {
			return false
		}
	}
	if scope.Language != "" && !strings.EqualFold(node.Language, scope.Language) {
		return false
	}
	if scope.Kind != "" && !strings.EqualFold(string(node.Kind), scope.Kind) && !strings.EqualFold(node.Symbol, scope.Kind) {
		return false
	}
	return true
}

const maxFocusQueryTerms = 16

func queryTerms(query string) []string {
	fields := strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	terms := make([]string, 0, len(fields))
	for _, field := range fields {
		term := normalizeName(field)
		if len(term) < 2 || isQueryStopWord(term) {
			continue
		}
		terms = appendUnique(terms, term)
		if len(terms) == maxFocusQueryTerms {
			break
		}
	}
	if len(terms) == 0 {
		if normalized := normalizeName(query); normalized != "" {
			terms = append(terms, normalized)
		}
	}
	return terms
}

func isQueryStopWord(value string) bool {
	switch value {
	case "a", "an", "and", "for", "from", "in", "of", "on", "the", "to", "where", "with":
		return true
	default:
		return false
	}
}

func levenshtein(left, right string) int {
	a := []rune(left)
	b := []rune(right)
	if len(a) > len(b) {
		a, b = b, a
	}
	previous := make([]int, len(a)+1)
	current := make([]int, len(a)+1)
	for i := range previous {
		previous[i] = i
	}
	for row, rightRune := range b {
		current[0] = row + 1
		for column, leftRune := range a {
			cost := 0
			if leftRune != rightRune {
				cost = 1
			}
			current[column+1] = minInt(previous[column+1]+1, current[column]+1, previous[column]+cost)
		}
		previous, current = current, previous
	}
	return previous[len(a)]
}

func normalizedTokenBudget(maxTokens int) int {
	if maxTokens <= 0 {
		return defaultQueryTokens
	}
	return maxTokens
}

func resultHitLimit(maxTokens int) int {
	return maxInt(1, minInt(64, maxTokens/24))
}

func expansionNodeLimit(maxTokens int) int {
	return maxInt(256, minInt(8_192, resultHitLimit(maxTokens)*64))
}

func prefixHits(hits []Hit, limit int) []Hit {
	if len(hits) <= limit {
		return append([]Hit(nil), hits...)
	}
	return append([]Hit(nil), hits[:limit]...)
}

func filterUndisclosedHits(hits []Hit, disclosed map[string]struct{}) []Hit {
	result := make([]Hit, 0, len(hits))
	for _, hit := range hits {
		if _, exists := disclosed[hit.NodeID]; !exists {
			result = append(result, hit)
		}
	}
	return result
}

func cloneStringSet(values map[string]struct{}) map[string]struct{} {
	clone := make(map[string]struct{}, len(values))
	for value := range values {
		clone[value] = struct{}{}
	}
	return clone
}

func hitDepth(hits []Hit) int {
	depth := 0
	for _, hit := range hits {
		depth = maxInt(depth, len(hit.Via))
	}
	return depth
}

func dwellSessionKey(sessionID string) string {
	if strings.TrimSpace(sessionID) == "" {
		return "__default__"
	}
	return sessionID
}

func appendCopy(values []EdgeKind, value EdgeKind) []EdgeKind {
	result := make([]EdgeKind, len(values)+1)
	copy(result, values)
	result[len(values)] = value
	return result
}

func minInt(values ...int) int {
	result := values[0]
	for _, value := range values[1:] {
		if value < result {
			result = value
		}
	}
	return result
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
