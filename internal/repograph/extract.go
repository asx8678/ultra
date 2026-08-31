package repograph

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
)

const maxExtractLine = 16 * 1024

// extractFile extracts a deterministic, language-independent set of facts from
// one source file. path is used for language detection; rel is the path stored
// in the resulting snapshot.
func extractFile(path, rel string, data []byte, hash string) FileFacts {
	if rel == "" {
		rel = path
	}
	language := languageForPath(path)
	if language == "" {
		language = languageForPath(rel)
	}
	facts := FileFacts{
		Path:     filepath.ToSlash(rel),
		Language: language,
		Hash:     hash,
		Size:     int64(len(data)),
	}

	if facts.Language == "" {
		facts.Warnings = append(facts.Warnings, "Unsupported file type")
		return facts
	}
	if bytes.IndexByte(data, 0) >= 0 {
		facts.Warnings = append(facts.Warnings, "Binary input skipped")
		return facts
	}
	if generatedSource(data) {
		facts.Generated = true
		return facts
	}
	if minifiedSource(data, facts.Language) {
		facts.Warnings = append(facts.Warnings, "Minified input skipped")
		return facts
	}

	if facts.Language == "go" {
		extractGo(&facts, data)
	} else {
		extractGeneric(&facts, data)
	}
	if route := fileConventionRoute(facts.Path); route != "" {
		methods := fileConventionMethods(facts.Path, facts.Symbols)
		if len(methods) == 0 {
			facts.Routes = append(facts.Routes, RouteFact{Path: route, Owner: filepath.Base(facts.Path), Line: 1})
		} else {
			for _, method := range methods {
				facts.Routes = append(facts.Routes, RouteFact{
					Method: method, Path: route, Owner: filepath.Base(facts.Path), Line: 1,
				})
			}
		}
	}
	deduplicateFacts(&facts)
	return facts
}

func fileConventionRoute(filePath string) string {
	filePath = filepath.ToSlash(filePath)
	parts := strings.Split(filePath, "/")
	if len(parts) == 0 {
		return ""
	}
	base := parts[len(parts)-1]
	extension := strings.ToLower(filepath.Ext(base))
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	start := -1

	findSegment := func(name string) int {
		for index, part := range parts[:len(parts)-1] {
			if part == name {
				return index + 1
			}
		}
		return -1
	}
	switch {
	case (stem == "page" || stem == "route") && slicesContain([]string{".js", ".jsx", ".ts", ".tsx", ".mjs", ".mdx"}, extension):
		start = findSegment("app")
	case (strings.HasPrefix(stem, "+page") || strings.HasPrefix(stem, "+server")) &&
		slicesContain([]string{".js", ".ts", ".svelte", ".md"}, extension):
		start = findSegment("routes")
	case extension == ".vue":
		start = findSegment("pages")
	case extension == ".astro":
		start = findSegment("pages")
	case slicesContain([]string{".js", ".jsx", ".ts", ".tsx", ".mjs"}, extension):
		for index := 0; index+1 < len(parts)-1; index++ {
			if parts[index] == "server" && parts[index+1] == "api" {
				start = index + 1
				break
			}
		}
		if start < 0 {
			start = findSegment("pages")
		}
	}
	if start < 0 {
		return ""
	}

	if suffix := filepath.Ext(stem); isHTTPMethod(strings.TrimPrefix(suffix, ".")) {
		stem = strings.TrimSuffix(stem, suffix)
	}
	routeParts := append([]string(nil), parts[start:len(parts)-1]...)
	if stem != "index" && stem != "page" && stem != "route" &&
		!strings.HasPrefix(stem, "+page") && !strings.HasPrefix(stem, "+server") {
		routeParts = append(routeParts, stem)
	}
	normalized := make([]string, 0, len(routeParts))
	for _, part := range routeParts {
		if part == "" || (strings.HasPrefix(part, "(") && strings.HasSuffix(part, ")")) {
			continue
		}
		if (strings.HasPrefix(part, "[") || strings.HasPrefix(part, "@[")) && strings.HasSuffix(part, "]") {
			part = "{*}"
		}
		normalized = append(normalized, part)
	}
	return normalizeExtractedRoute("/" + strings.Join(normalized, "/"))
}

func fileConventionMethods(filePath string, symbols []SymbolFact) []string {
	filePath = filepath.ToSlash(filePath)
	base := filepath.Base(filePath)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if suffix := strings.TrimPrefix(strings.ToLower(filepath.Ext(stem)), "."); isHTTPMethod(suffix) {
		return []string{strings.ToUpper(suffix)}
	}
	exportDriven := stem == "route" || strings.HasPrefix(stem, "+server") ||
		strings.Contains("/"+strings.Trim(filePath, "/")+"/", "/src/pages/")
	if !exportDriven {
		return nil
	}
	methods := make([]string, 0, 7)
	for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"} {
		for _, symbol := range symbols {
			if symbol.Exported && strings.EqualFold(symbol.Name, method) {
				methods = append(methods, method)
				break
			}
		}
	}
	return methods
}

func slicesContain(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func generatedSource(data []byte) bool {
	return isGenerated(data)
}

func minifiedSource(data []byte, language string) bool {
	// Compact configuration files are common, but very large one-line JSON is
	// generally generated output and provides a poor structural outline.
	if language == "json" {
		return len(data) > 256*1024 && bytes.Count(data, []byte{'\n'}) < 3
	}
	if language == "yaml" || language == "toml" || language == "markdown" {
		return false
	}
	lines := bytes.Split(data, []byte{'\n'})
	for _, line := range lines {
		if len(line) > maxExtractLine {
			return true
		}
	}
	if len(data) > 4096 && len(lines) < 4 {
		return true
	}
	return false
}

func normalizeExtractedRoute(value string) string {
	route := normalizeRoute(value)
	if route == "" {
		return ""
	}
	parts := strings.Split(route, "/")
	for index := 1; index < len(parts); index++ {
		part := parts[index]
		if strings.HasPrefix(part, ":") || part == "*" ||
			(len(part) > 2 && ((part[0] == '{' && part[len(part)-1] == '}') ||
				(part[0] == '[' && part[len(part)-1] == ']') ||
				(part[0] == '<' && part[len(part)-1] == '>'))) ||
			(len(part) > 3 && strings.HasPrefix(part, "${") && strings.HasSuffix(part, "}")) {
			parts[index] = "{*}"
		}
	}
	return strings.Join(parts, "/")
}

func deduplicateFacts(facts *FileFacts) {
	facts.Symbols = uniqueBy(facts.Symbols, func(f SymbolFact) string {
		return fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%d", f.Name, f.Qualified, f.Kind, f.StartLine, f.EndLine)
	})
	facts.Imports = uniqueBy(facts.Imports, func(f ImportFact) string {
		return fmt.Sprintf("%s\x00%d", f.Target, f.Line)
	})
	facts.Calls = uniqueBy(facts.Calls, func(f CallFact) string {
		return fmt.Sprintf("%s\x00%s\x00%d", f.Caller, f.Callee, f.Line)
	})
	facts.Literals = uniqueBy(facts.Literals, func(f LiteralFact) string {
		return fmt.Sprintf("%s\x00%s\x00%d", f.Value, f.Kind, f.Line)
	})
	facts.Routes = uniqueBy(facts.Routes, func(f RouteFact) string {
		return fmt.Sprintf("%s\x00%s\x00%s\x00%d", f.Method, f.Path, f.Owner, f.Line)
	})
	facts.Inheritance = uniqueBy(facts.Inheritance, func(f InheritanceFact) string {
		return fmt.Sprintf("%s\x00%s\x00%d", f.Child, f.Parent, f.Line)
	})
	facts.Warnings = uniqueBy(facts.Warnings, func(value string) string { return value })
}

func uniqueBy[T any](values []T, key func(T) string) []T {
	if len(values) < 2 {
		return values
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]T, 0, len(values))
	for _, value := range values {
		k := key(value)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, value)
	}
	return out
}
