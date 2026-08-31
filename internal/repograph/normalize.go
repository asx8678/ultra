package repograph

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

var (
	routeParameter = regexp.MustCompile(`^(?::[^/]+|\{[^/]+\}|\[[^/]+\]|<[^/]+>|\$\{[^/]+\}|\*)$`)
	environmentKey = regexp.MustCompile(`^[A-Z][A-Z0-9_]{3,}$`)
)

func stableID(parts ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(digest[:12])
}

func normalizeName(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func normalizeRoute(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "/") {
		return ""
	}
	parts := strings.Split(strings.TrimSuffix(value, "/"), "/")
	for i := 1; i < len(parts); i++ {
		if routeParameter.MatchString(parts[i]) {
			parts[i] = "{*}"
		}
	}
	if len(parts) == 1 {
		return "/"
	}
	return strings.Join(parts, "/")
}

func literalKind(value string) string {
	switch {
	case environmentKey.MatchString(value):
		return "env"
	case normalizeRoute(value) != "":
		return "path"
	case len(value) >= 8 && len(strings.Fields(value)) <= 4:
		return "distinctive"
	default:
		return ""
	}
}

func languageForPath(path string) string {
	base := strings.ToLower(filepath.Base(path))
	switch {
	case base == "dockerfile" || strings.HasPrefix(base, "dockerfile.") || base == "makefile" ||
		base == "gnumakefile" || strings.HasSuffix(base, ".mk") || base == ".envrc":
		return "shell"
	case strings.HasSuffix(base, ".go"):
		return "go"
	case strings.HasSuffix(base, ".ts") || strings.HasSuffix(base, ".tsx") || strings.HasSuffix(base, ".mts") || strings.HasSuffix(base, ".cts"):
		return "typescript"
	case strings.HasSuffix(base, ".js") || strings.HasSuffix(base, ".jsx") || strings.HasSuffix(base, ".mjs") || strings.HasSuffix(base, ".cjs"):
		return "javascript"
	case strings.HasSuffix(base, ".svelte"):
		return "svelte"
	case strings.HasSuffix(base, ".vue"):
		return "vue"
	case strings.HasSuffix(base, ".astro"):
		return "astro"
	case strings.HasSuffix(base, ".py"):
		return "python"
	case strings.HasSuffix(base, ".rs"):
		return "rust"
	case strings.HasSuffix(base, ".java"):
		return "java"
	case strings.HasSuffix(base, ".kt") || strings.HasSuffix(base, ".kts"):
		return "kotlin"
	case strings.HasSuffix(base, ".rb"):
		return "ruby"
	case strings.HasSuffix(base, ".c") || strings.HasSuffix(base, ".h"):
		return "c"
	case strings.HasSuffix(base, ".cc") || strings.HasSuffix(base, ".cpp") || strings.HasSuffix(base, ".cxx") ||
		strings.HasSuffix(base, ".hh") || strings.HasSuffix(base, ".hpp"):
		return "cpp"
	case strings.HasSuffix(base, ".php"):
		return "php"
	case strings.HasSuffix(base, ".swift"):
		return "swift"
	case strings.HasSuffix(base, ".scala"):
		return "scala"
	case strings.HasSuffix(base, ".hs"):
		return "haskell"
	case strings.HasSuffix(base, ".lua"):
		return "lua"
	case strings.HasSuffix(base, ".ex") || strings.HasSuffix(base, ".exs"):
		return "elixir"
	case strings.HasSuffix(base, ".tf") || strings.HasSuffix(base, ".tfvars") || strings.HasSuffix(base, ".hcl"):
		return "hcl"
	case base == ".env" || strings.HasPrefix(base, ".env.") || strings.HasSuffix(base, ".env"):
		return "env"
	case strings.HasSuffix(base, ".sh") || strings.HasSuffix(base, ".bash") || strings.HasSuffix(base, ".zsh"):
		return "shell"
	case strings.HasSuffix(base, ".yaml") || strings.HasSuffix(base, ".yml"):
		return "yaml"
	case strings.HasSuffix(base, ".json"):
		return "json"
	case strings.HasSuffix(base, ".toml"):
		return "toml"
	case strings.HasSuffix(base, ".md") || strings.HasSuffix(base, ".mdx") || strings.HasSuffix(base, ".markdown"):
		return "markdown"
	default:
		return ""
	}
}
