package repograph

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// renderGraphResult produces plain, model-oriented text under a strict byte
// budget. maxTokens is treated conservatively as four UTF-8 bytes per token.
func renderGraphResult(operation string, snapshot *Snapshot, hits []Hit, reads []ReadWindow, depth, maxTokens int, warnings []string) Result {
	maxTokens = normalizedTokenBudget(maxTokens)
	budget := tokenByteBudget(maxTokens)
	root := ""
	generation := uint64(0)
	coverage := Coverage{}
	degraded := false
	if snapshot != nil {
		root = snapshot.Root
		generation = snapshot.Generation
		coverage = snapshot.Coverage
		degraded = snapshot.Coverage.Unsupported > 0 || snapshot.Coverage.Unreadable > 0 ||
			snapshot.Coverage.Oversized > 0 || snapshot.Coverage.Omitted > 0 || len(snapshot.Coverage.Warnings) > 0
		warnings = append(warnings, snapshot.Coverage.Warnings...)
	}
	warnings = stableStrings(warnings)
	status := "ready"
	if len(hits) == 0 {
		switch operation {
		case "focus", "impact":
			status = "no_matches"
		case "dwell":
			status = "exhausted"
		default:
			status = "empty"
		}
	}

	var builder strings.Builder
	truncated := false
	appendBlock := func(block string) bool {
		if block == "" {
			return true
		}
		if builder.Len()+len(block) > budget {
			truncated = true
			return false
		}
		builder.WriteString(block)
		return true
	}

	header := fmt.Sprintf("repo_graph %s root=%s generation=%d\n", safeField(operation), safePathField(root), generation)
	if len(header) > budget {
		builder.WriteString(truncateUTF8Bytes(header, budget))
		truncated = true
	} else {
		builder.WriteString(header)
	}
	if snapshot != nil {
		appendBlock(renderCoverage(snapshot.Coverage))
	}

	renderedHits := 0
	for index, hit := range hits {
		block := renderHit(index+1, hit)
		if !appendBlock(block) {
			break
		}
		renderedHits++
	}
	if renderedHits < len(hits) {
		truncated = true
	}

	visiblePaths := make(map[string]struct{}, renderedHits)
	for _, hit := range hits[:renderedHits] {
		visiblePaths[hit.Path] = struct{}{}
	}
	candidateReads := make([]ReadWindow, 0, len(reads))
	for _, read := range reads {
		if _, visible := visiblePaths[read.Path]; visible {
			candidateReads = append(candidateReads, read)
		}
	}
	renderedReads := 0
	if len(candidateReads) > 0 && appendBlock("reads:\n") {
		for _, read := range candidateReads {
			block := fmt.Sprintf("- %s:%d-%d offset=%d limit=%d\n", safePathField(read.Path), read.StartLine, read.EndLine, read.Offset, read.Limit)
			if !appendBlock(block) {
				break
			}
			renderedReads++
		}
	}
	if renderedReads < len(candidateReads) {
		truncated = true
	}

	renderedWarnings := 0
	for _, warning := range warnings {
		if !appendBlock("warning: " + safeLine(warning) + "\n") {
			break
		}
		renderedWarnings++
	}
	if renderedWarnings < len(warnings) {
		truncated = true
	}

	text := builder.String()
	// Keep this final guard even though appendBlock is budget-aware: it makes
	// the contract robust if future header formatting changes.
	if len(text) > budget {
		text = truncateUTF8Bytes(text, budget)
		truncated = true
	}
	usedTokens := 0
	if len(text) > 0 {
		usedTokens = (len(text) + 3) / 4
	}

	return Result{
		Text:           text,
		Hits:           append([]Hit(nil), hits[:renderedHits]...),
		SuggestedReads: append([]ReadWindow(nil), candidateReads[:renderedReads]...),
		Meta: ResultMeta{
			Operation: operation, Root: root, Status: status, Generation: generation, Depth: depth,
			Truncated: truncated, Degraded: degraded, UsedTokens: usedTokens,
			MaxTokens: maxTokens, Warnings: warnings, Coverage: cloneCoverage(coverage),
		},
	}
}

func renderCoverage(coverage Coverage) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "coverage indexed=%d/%d", coverage.Indexed, coverage.Discovered)
	for _, field := range []struct {
		name  string
		value int
	}{
		{name: "unsupported", value: coverage.Unsupported},
		{name: "generated", value: coverage.Generated},
		{name: "oversized", value: coverage.Oversized},
		{name: "unreadable", value: coverage.Unreadable},
		{name: "omitted", value: coverage.Omitted},
	} {
		if field.value > 0 {
			fmt.Fprintf(&builder, " %s=%d", field.name, field.value)
		}
	}
	builder.WriteByte('\n')
	return builder.String()
}

func renderHit(index int, hit Hit) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "%d. %s %s", index, safeField(string(hit.Kind)), safeField(hit.Name))
	if hit.Path != "" {
		fmt.Fprintf(&builder, " %s", safePathField(hit.Path))
		if hit.Line > 0 {
			fmt.Fprintf(&builder, ":%d", hit.Line)
			if hit.EndLine > hit.Line {
				fmt.Fprintf(&builder, "-%d", hit.EndLine)
			}
		}
	}
	if hit.Language != "" {
		fmt.Fprintf(&builder, " lang=%s", safeField(hit.Language))
	}
	if hit.Relation != "" {
		fmt.Fprintf(&builder, " relation=%s", hit.Relation)
		if hit.Direction != "" {
			fmt.Fprintf(&builder, ":%s", safeField(hit.Direction))
		}
	}
	if len(hit.Via) > 0 {
		builder.WriteString(" via=")
		for index, kind := range hit.Via {
			if index > 0 {
				builder.WriteByte('>')
			}
			builder.WriteString(string(kind))
		}
	}
	fmt.Fprintf(&builder, " score=%d\n", hit.Score)
	return builder.String()
}

func tokenByteBudget(maxTokens int) int {
	if maxTokens <= 0 {
		return 0
	}
	maxIntValue := int(^uint(0) >> 1)
	if maxTokens > maxIntValue/4 {
		return maxIntValue
	}
	return maxTokens * 4
}

func truncateUTF8Bytes(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	end := maxBytes
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end]
}

func safeField(value string) string {
	value = safeLine(value)
	if value == "" {
		return "-"
	}
	if strings.ContainsAny(value, " \t=\"") {
		return fmt.Sprintf("%q", value)
	}
	return value
}

func safePathField(value string) string {
	if value == "" {
		return "-"
	}
	value = sanitizeDisplayControls(value)
	if strings.ContainsAny(value, " =\"") {
		return fmt.Sprintf("%q", value)
	}
	return value
}

func safeLine(value string) string {
	value = strings.TrimSpace(sanitizeDisplayControls(value))
	return strings.Join(strings.Fields(value), " ")
}

func sanitizeDisplayControls(value string) string {
	return strings.Map(func(character rune) rune {
		if unicode.IsControl(character) || unicode.In(character, unicode.Cf) {
			return ' '
		}
		return character
	}, value)
}

func stableStrings(values []string) []string {
	return boundedCoverageWarnings(values)
}
