package repograph

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var (
	genericCallPattern       = regexp.MustCompile(`[A-Za-z_$][A-Za-z0-9_$]*(?:\s*\.\s*[A-Za-z_$][A-Za-z0-9_$]*)*\s*\(`)
	routeCallPattern         = regexp.MustCompile(`(?i)(get|post|put|patch|delete|options|head|connect|trace|all|any|route|group|path|re_path|url|match|root|forward|resources|handle|handlefunc|use|mount|add_url_rule|add_(?:get|post|put|patch|delete|view)|fetch|redirect|respondredirect|redirect_to|websocket|[a-z]+mapping)\s*\(\s*(?:[rbuf]{0,3})?["'\x60]([^"'\x60]+)`)
	routeVerbArgumentPattern = regexp.MustCompile(`(?i)(?:method|methodfunc|add_route|add_view|route)\s*\(\s*["'](GET|POST|PUT|PATCH|DELETE|OPTIONS|HEAD|CONNECT|TRACE)["']\s*,\s*["']([^"']+)`)
	routePrefixPattern       = regexp.MustCompile(`(?i)@(controller|requestmapping)\s*\(\s*["']([^"']+)`)
	nestedRoutePrefixPattern = regexp.MustCompile(`(?i)^\s*(?:scope\s+["']([^"']+)["'].*\bdo\s*$|route\s*\(\s*["']([^"']+)["'][^)]*\)\s*\{)`)
	elixirRoutePattern       = regexp.MustCompile(`(?i)^\s*(get|post|put|patch|delete|options|head|forward|resources)\s+["']([^"']+)["']`)
	identifierPattern        = regexp.MustCompile(`[A-Za-z_$][A-Za-z0-9_$]*(?:\.[A-Za-z_$][A-Za-z0-9_$]*)*`)
	envPatterns              = []*regexp.Regexp{
		regexp.MustCompile(`\bprocess\.env\.([A-Z][A-Z0-9_]{3,})\b`),
		regexp.MustCompile(`\b(?:ENV|environ)\s*\[\s*["']([A-Z][A-Z0-9_]{3,})["']\s*\]`),
		regexp.MustCompile(`\$\{([A-Z][A-Z0-9_]{3,})\}`),
		regexp.MustCompile(`\$([A-Z][A-Z0-9_]{3,})\b`),
	}
	ecmaClassPattern       = regexp.MustCompile(`^(?:export\s+)?(?:default\s+)?(?:declare\s+)?(?:abstract\s+)?(class|interface|enum|namespace|type)\s+([A-Za-z_$][A-Za-z0-9_$]*)\b(.*)$`)
	ecmaFunctionPattern    = regexp.MustCompile(`^(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s*\*?\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`)
	ecmaArrowPattern       = regexp.MustCompile(`^(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*(?::[^=]+)?=\s*(?:async\s*)?(?:\([^)]*\)|[A-Za-z_$][A-Za-z0-9_$]*)\s*=>`)
	ecmaMethodPattern      = regexp.MustCompile(`^(?:(?:public|private|protected|static|abstract|async|readonly|get|set)\s+)*([A-Za-z_$][A-Za-z0-9_$]*)\s*(?:<[^>{}]*>)?\s*\(`)
	pythonClassPattern     = regexp.MustCompile(`^class\s+([A-Za-z_][A-Za-z0-9_]*)\s*(?:\(([^)]*)\))?\s*:`)
	pythonFunctionPattern  = regexp.MustCompile(`^(?:async\s+)?def\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	rustTypePattern        = regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?(struct|enum|trait|union|type|mod)\s+([A-Za-z_][A-Za-z0-9_]*)\b`)
	rustFunctionPattern    = regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?(?:async\s+)?(?:unsafe\s+)?(?:extern\s+"[^"]+"\s+)?fn\s+([A-Za-z_][A-Za-z0-9_]*)\s*(?:<[^>{}]*>)?\s*\(`)
	rustConstantPattern    = regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?(const|static)\s+(?:mut\s+)?([A-Za-z_][A-Za-z0-9_]*)\b`)
	rustImplPattern        = regexp.MustCompile(`^impl(?:\s*<[^>{}]*>)?\s+(.+?)\s*\{`)
	jvmTypePattern         = regexp.MustCompile(`^(?:(?:public|private|protected|internal|abstract|final|sealed|open|data|annotation|value|static)\s+)*(class|interface|enum|object|record)\s+(?:class\s+)?([A-Za-z_$][A-Za-z0-9_$]*)\b(.*)$`)
	jvmJavaMethodPattern   = regexp.MustCompile(`^(?:(?:public|private|protected|static|final|abstract|synchronized|native|default|strictfp)\s+)*(?:<[^>{}]+>\s+)?(?:[A-Za-z_$][A-Za-z0-9_$<>,.?\[\]]*\s+)([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`)
	jvmKotlinFuncPattern   = regexp.MustCompile(`^(?:(?:public|private|protected|internal|open|final|abstract|override|suspend|inline|operator|tailrec)\s+)*fun\s+(?:<[^>{}]+>\s*)?(?:[A-Za-z_$][A-Za-z0-9_$<>?.]*\.)?([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`)
	rubyTypePattern        = regexp.MustCompile(`^(class|module)\s+([A-Z][A-Za-z0-9_:]*)(?:\s*<\s*([A-Z][A-Za-z0-9_:]*))?`)
	rubyMethodPattern      = regexp.MustCompile(`^def\s+(?:self\.)?([A-Za-z_][A-Za-z0-9_!?=]*)`)
	shellFunctionPattern   = regexp.MustCompile(`^(?:function\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*(?:\(\s*\))?\s*\{`)
	cTypePattern           = regexp.MustCompile(`^(?:typedef\s+)?(struct|union|enum|class)\s+([A-Za-z_][A-Za-z0-9_]*)\b`)
	cFunctionPattern       = regexp.MustCompile(`^(?:(?:extern|static|inline|constexpr|consteval|virtual|friend|explicit)\s+)*(?:[A-Za-z_][A-Za-z0-9_:<>,*&\s\[\]]*\s+)([~A-Za-z_][A-Za-z0-9_:~]*)\s*\([^;{}]*\)\s*(?:const\b\s*)?(?:noexcept(?:\s*\([^)]*\))?\s*)?(?:\{|;|$)`)
	elixirTypePattern      = regexp.MustCompile(`^(defmodule|defprotocol|defimpl)\s+([A-Za-z_][A-Za-z0-9_.]*)\b`)
	elixirFunctionPattern  = regexp.MustCompile(`^(def|defp|defmacro|defmacrop)\s+([a-z_][A-Za-z0-9_!?=]*)\b`)
	luaFunctionPattern     = regexp.MustCompile(`^(?:local\s+)?function\s+([A-Za-z_][A-Za-z0-9_.:]*)\s*\(`)
	phpTypePattern         = regexp.MustCompile(`^(?:(?:abstract|final|readonly)\s+)*(class|interface|trait|enum)\s+([A-Za-z_][A-Za-z0-9_]*)\b`)
	phpFunctionPattern     = regexp.MustCompile(`^(?:(?:public|protected|private|static|final|abstract|readonly)\s+)*function\s+&?\s*([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	swiftTypePattern       = regexp.MustCompile(`^(?:(?:public|internal|private|fileprivate|open|final|indirect)\s+)*(class|struct|protocol|enum|actor|extension)\s+([A-Za-z_][A-Za-z0-9_]*)\b`)
	swiftFunctionPattern   = regexp.MustCompile(`^(?:(?:public|internal|private|fileprivate|open|final|static|class|mutating|nonmutating|override)\s+)*func\s+([A-Za-z_][A-Za-z0-9_]*)\s*(?:<[^>{}]*>)?\s*\(`)
	scalaTypePattern       = regexp.MustCompile(`^(?:(?:private|protected|sealed|abstract|final|case|implicit|lazy)\s+)*(class|trait|object|enum)\s+([A-Za-z_][A-Za-z0-9_]*)\b`)
	scalaFunctionPattern   = regexp.MustCompile(`^(?:(?:private|protected|final|override|implicit|inline|transparent|lazy)\s+)*def\s+([A-Za-z_][A-Za-z0-9_]*)\b`)
	haskellTypePattern     = regexp.MustCompile(`^(data|newtype|type|class)\s+(?:\([^)]*\)\s*=>\s*)?([A-Z][A-Za-z0-9_']*)\b`)
	haskellSignature       = regexp.MustCompile(`^([a-z_][A-Za-z0-9_']*)\s*::`)
	haskellBinding         = regexp.MustCompile(`^([a-z_][A-Za-z0-9_']*)\b[^=]*=`)
	hclBlockPattern        = regexp.MustCompile(`^(resource|data|module|variable|output|provider|terraform|locals)\b(?:\s+"([^"]+)")?(?:\s+"([^"]+)")?\s*\{`)
	hclKeyPattern          = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_-]*)\s*=`)
	envAssignmentPattern   = regexp.MustCompile(`^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=`)
	shellAssignmentPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
	rubyBareRoutePattern   = regexp.MustCompile(`^\s*(get|post|put|patch|delete|options|head|match|root|redirect|mount)\s+["']([^"']+)`)
	rubyBareCallPattern    = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_!?]*(?:\.[A-Za-z_][A-Za-z0-9_!?]*)*)\s+`)
	yamlKeyPattern         = regexp.MustCompile(`^([ ]*)(?:-\s*)?([A-Za-z0-9_.-]+)\s*:`)
	jsonKeyPattern         = regexp.MustCompile(`"([^"\\]+)"\s*:`)
	tomlTablePattern       = regexp.MustCompile(`^\s*\[\[?([^]\s]+)\]\]?\s*$`)
	tomlKeyPattern         = regexp.MustCompile(`^\s*([A-Za-z0-9_.-]+)\s*=`)
	markdownHeadingPattern = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*#*\s*$`)
	rubyBlockOpenPattern   = regexp.MustCompile(`^(?:class|module|def|if|unless|case|while|until|for|begin)\b|\bdo\s*(?:\|[^|]*\|)?\s*$`)
	genericImportPatterns  = map[string][]*regexp.Regexp{
		"typescript": {
			regexp.MustCompile(`^\s*import(?:[^"']*\s+from\s+)?\s*["']([^"']+)["']`),
			regexp.MustCompile(`\brequire\s*\(\s*["']([^"']+)["']\s*\)`),
		},
		"javascript": {
			regexp.MustCompile(`^\s*import(?:[^"']*\s+from\s+)?\s*["']([^"']+)["']`),
			regexp.MustCompile(`\brequire\s*\(\s*["']([^"']+)["']\s*\)`),
		},
		"python":  {regexp.MustCompile(`^\s*(?:from\s+([A-Za-z0-9_.]+)\s+import|import\s+([A-Za-z0-9_.]+))`)},
		"rust":    {regexp.MustCompile(`^\s*use\s+([^;]+)`)},
		"java":    {regexp.MustCompile(`^\s*import\s+(?:static\s+)?([A-Za-z0-9_.*]+)\s*;`)},
		"kotlin":  {regexp.MustCompile(`^\s*import\s+([A-Za-z0-9_.*]+)`)},
		"ruby":    {regexp.MustCompile(`^\s*(?:require|require_relative|load)\s*(?:\(\s*)?["']([^"']+)["']`)},
		"shell":   {regexp.MustCompile(`^\s*(?:source|\.)\s+["']?([^\s"']+)`)},
		"c":       {regexp.MustCompile(`^\s*#\s*include\s*[<"]([^>"]+)[>"]`)},
		"cpp":     {regexp.MustCompile(`^\s*#\s*include\s*[<"]([^>"]+)[>"]`)},
		"elixir":  {regexp.MustCompile(`^\s*(?:alias|import|use)\s+([A-Za-z0-9_.]+)`)},
		"lua":     {regexp.MustCompile(`\brequire\s*\(?\s*["']([^"']+)["']`)},
		"php":     {regexp.MustCompile(`^\s*use\s+([^;]+)\s*;`)},
		"swift":   {regexp.MustCompile(`^\s*import\s+([A-Za-z0-9_.]+)`)},
		"scala":   {regexp.MustCompile(`^\s*import\s+([A-Za-z0-9_.*{}]+)`)},
		"haskell": {regexp.MustCompile(`^\s*import\s+(?:qualified\s+)?([A-Za-z0-9_.]+)`)},
	}
)

func extractGeneric(facts *FileFacts, data []byte) {
	raw := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	lines := sanitizeSourceLines(raw, facts.Language)

	switch facts.Language {
	case "yaml":
		extractYAMLOutline(facts, lines)
	case "json":
		extractJSONOutline(facts, lines)
	case "toml":
		extractTOMLOutline(facts, lines)
	case "hcl":
		extractHCLOutline(facts, lines)
	case "env":
		extractEnvOutline(facts, lines)
	case "markdown":
		extractMarkdownOutline(facts, lines)
	default:
		extractCodeDeclarations(facts, lines)
		extractGenericImports(facts, lines)
	}
	extractGenericUses(facts, lines)
	composeRoutePrefixes(facts, lines)
	composeNestedRoutePrefixes(facts, lines)
}

func extractCodeDeclarations(facts *FileFacts, lines []string) {
	for index, line := range lines {
		lineNo := index + 1
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || len(trimmed) > maxExtractLine {
			continue
		}

		switch facts.Language {
		case "typescript", "javascript", "svelte", "vue", "astro":
			extractECMAScriptDeclaration(facts, lines, index, trimmed)
		case "python":
			extractPythonDeclaration(facts, lines, index, trimmed)
		case "rust":
			extractRustDeclaration(facts, lines, index, trimmed)
		case "java", "kotlin":
			extractJVMDeclaration(facts, lines, index, trimmed)
		case "ruby":
			extractRubyDeclaration(facts, lines, index, trimmed)
		case "shell":
			extractShellDeclaration(facts, lines, index, trimmed)
		case "c", "cpp":
			extractCDeclaration(facts, lines, index, trimmed)
		case "elixir":
			extractElixirDeclaration(facts, lines, index, trimmed)
		case "lua":
			extractLuaDeclaration(facts, lines, index, trimmed)
		case "php":
			extractPHPDeclaration(facts, lines, index, trimmed)
		case "swift":
			extractSwiftDeclaration(facts, lines, index, trimmed)
		case "scala":
			extractScalaDeclaration(facts, lines, index, trimmed)
		case "haskell":
			extractHaskellDeclaration(facts, lines, index, trimmed)
		}

		_ = lineNo
	}
}

func extractECMAScriptDeclaration(facts *FileFacts, lines []string, index int, line string) {
	classPattern := ecmaClassPattern
	functionPattern := ecmaFunctionPattern
	arrowPattern := ecmaArrowPattern
	methodPattern := ecmaMethodPattern

	if match := classPattern.FindStringSubmatch(line); match != nil {
		kind, name, tail := match[1], match[2], match[3]
		end := index + 1
		if kind != "type" || strings.Contains(line, "{") {
			end = braceBlockEnd(lines, index)
		}
		addGenericSymbol(facts, name, kind, line, index+1, end, "", strings.Contains(line, "export ") || strings.Contains(line, "default "))
		for _, parent := range parentsAfterKeywords(tail, "extends", "implements") {
			facts.Inheritance = append(facts.Inheritance, InheritanceFact{Child: name, Parent: parent, Line: index + 1})
		}
		return
	}
	if match := functionPattern.FindStringSubmatch(line); match != nil {
		addGenericSymbol(facts, match[1], "function", line, index+1, braceBlockEnd(lines, index), "", strings.Contains(line, "export "))
		return
	}
	if match := arrowPattern.FindStringSubmatch(line); match != nil {
		end := index + 1
		if strings.Contains(line, "{") {
			end = braceBlockEnd(lines, index)
		}
		addGenericSymbol(facts, match[1], "function", line, index+1, end, "", strings.Contains(line, "export "))
		return
	}
	parent := enclosingType(facts.Symbols, index+1)
	if parent != "" {
		if match := methodPattern.FindStringSubmatch(line); match != nil && !genericKeyword(match[1]) {
			addGenericSymbol(facts, match[1], "method", line, index+1, braceBlockEnd(lines, index), parent, !strings.HasPrefix(line, "private "))
		}
	}
}

func extractPythonDeclaration(facts *FileFacts, lines []string, index int, line string) {
	classPattern := pythonClassPattern
	functionPattern := pythonFunctionPattern
	if match := classPattern.FindStringSubmatch(line); match != nil {
		name := match[1]
		addGenericSymbol(facts, name, "class", line, index+1, indentationBlockEnd(lines, index), "", !strings.HasPrefix(name, "_"))
		for _, parent := range splitParentNames(match[2]) {
			facts.Inheritance = append(facts.Inheritance, InheritanceFact{Child: name, Parent: parent, Line: index + 1})
		}
		return
	}
	if match := functionPattern.FindStringSubmatch(line); match != nil {
		parent := enclosingType(facts.Symbols, index+1)
		kind := "function"
		if parent != "" {
			kind = "method"
		}
		addGenericSymbol(facts, match[1], kind, line, index+1, indentationBlockEnd(lines, index), parent, !strings.HasPrefix(match[1], "_"))
	}
}

func extractRustDeclaration(facts *FileFacts, lines []string, index int, line string) {
	typePattern := rustTypePattern
	functionPattern := rustFunctionPattern
	constantPattern := rustConstantPattern
	implPattern := rustImplPattern
	if match := typePattern.FindStringSubmatch(line); match != nil {
		addGenericSymbol(facts, match[2], match[1], line, index+1, braceBlockEnd(lines, index), "", strings.HasPrefix(line, "pub"))
		return
	}
	if match := implPattern.FindStringSubmatch(line); match != nil {
		trait, child := rustImplParts(match[1])
		addGenericSymbol(facts, child, "impl", line, index+1, braceBlockEnd(lines, index), "", false)
		if trait != "" && child != "" {
			facts.Inheritance = append(facts.Inheritance, InheritanceFact{Child: child, Parent: trait, Line: index + 1})
		}
		return
	}
	if match := functionPattern.FindStringSubmatch(line); match != nil {
		parent := enclosingType(facts.Symbols, index+1)
		kind := "function"
		if parent != "" {
			kind = "method"
		}
		addGenericSymbol(facts, match[1], kind, line, index+1, braceBlockEnd(lines, index), parent, strings.HasPrefix(line, "pub"))
		return
	}
	if match := constantPattern.FindStringSubmatch(line); match != nil {
		addGenericSymbol(facts, match[2], match[1], line, index+1, index+1, enclosingType(facts.Symbols, index+1), strings.HasPrefix(line, "pub"))
	}
}

func extractJVMDeclaration(facts *FileFacts, lines []string, index int, line string) {
	typePattern := jvmTypePattern
	javaMethodPattern := jvmJavaMethodPattern
	kotlinFunctionPattern := jvmKotlinFuncPattern
	if match := typePattern.FindStringSubmatch(line); match != nil {
		name := match[2]
		addGenericSymbol(facts, name, match[1], line, index+1, braceBlockEnd(lines, index), "", strings.Contains(line, "public ") || facts.Language == "kotlin")
		if facts.Language == "kotlin" {
			if colon := strings.Index(match[3], ":"); colon >= 0 {
				for _, parent := range splitParentNames(match[3][colon+1:]) {
					facts.Inheritance = append(facts.Inheritance, InheritanceFact{Child: name, Parent: parent, Line: index + 1})
				}
			}
		} else {
			for _, parent := range parentsAfterKeywords(match[3], "extends", "implements") {
				facts.Inheritance = append(facts.Inheritance, InheritanceFact{Child: name, Parent: parent, Line: index + 1})
			}
		}
		return
	}
	var match []string
	if facts.Language == "kotlin" {
		match = kotlinFunctionPattern.FindStringSubmatch(line)
	} else {
		match = javaMethodPattern.FindStringSubmatch(line)
	}
	if match != nil {
		parent := enclosingType(facts.Symbols, index+1)
		kind := "function"
		if parent != "" {
			kind = "method"
		}
		addGenericSymbol(facts, match[1], kind, line, index+1, braceBlockEnd(lines, index), parent, strings.Contains(line, "public ") || facts.Language == "kotlin")
	}
}

func extractRubyDeclaration(facts *FileFacts, lines []string, index int, line string) {
	typePattern := rubyTypePattern
	methodPattern := rubyMethodPattern
	if match := typePattern.FindStringSubmatch(line); match != nil {
		addGenericSymbol(facts, match[2], match[1], line, index+1, rubyBlockEnd(lines, index), "", true)
		if match[3] != "" {
			facts.Inheritance = append(facts.Inheritance, InheritanceFact{Child: match[2], Parent: match[3], Line: index + 1})
		}
		return
	}
	if match := methodPattern.FindStringSubmatch(line); match != nil {
		parent := enclosingType(facts.Symbols, index+1)
		addGenericSymbol(facts, match[1], "method", line, index+1, rubyBlockEnd(lines, index), parent, !strings.HasPrefix(match[1], "_"))
	}
}

func extractShellDeclaration(facts *FileFacts, lines []string, index int, line string) {
	pattern := shellFunctionPattern
	if match := pattern.FindStringSubmatch(line); match != nil {
		addGenericSymbol(facts, match[1], "function", line, index+1, braceBlockEnd(lines, index), "", true)
	}
}

func extractCDeclaration(facts *FileFacts, lines []string, index int, line string) {
	if match := cTypePattern.FindStringSubmatch(line); match != nil {
		end := index + 1
		if strings.Contains(line, "{") {
			end = braceBlockEnd(lines, index)
		}
		addGenericSymbol(facts, match[2], match[1], line, index+1, end, "", exportedOutlineName(match[2]))
		return
	}
	for _, keyword := range []string{"return ", "if ", "for ", "while ", "switch ", "catch ", "sizeof "} {
		if strings.HasPrefix(line, keyword) {
			return
		}
	}
	match := cFunctionPattern.FindStringSubmatch(line)
	if match == nil {
		return
	}
	qualified := match[1]
	name := qualified
	parent := ""
	if separator := strings.LastIndex(qualified, "::"); separator >= 0 {
		parent = qualified[:separator]
		name = qualified[separator+2:]
	} else {
		parent = enclosingType(facts.Symbols, index+1)
	}
	kind := "function"
	if parent != "" {
		kind = "method"
	}
	end := index + 1
	if strings.Contains(line, "{") {
		end = braceBlockEnd(lines, index)
	}
	addGenericSymbol(facts, name, kind, line, index+1, end, parent, exportedOutlineName(name))
}

func extractElixirDeclaration(facts *FileFacts, lines []string, index int, line string) {
	if match := elixirTypePattern.FindStringSubmatch(line); match != nil {
		kind := "module"
		if match[1] == "defprotocol" {
			kind = "interface"
		} else if match[1] == "defimpl" {
			kind = "impl"
		}
		addGenericSymbol(facts, match[2], kind, line, index+1, elixirBlockEnd(lines, index), "", true)
		return
	}
	if match := elixirFunctionPattern.FindStringSubmatch(line); match != nil {
		parent := enclosingType(facts.Symbols, index+1)
		kind := "function"
		if parent != "" {
			kind = "method"
		}
		exported := match[1] != "defp" && match[1] != "defmacrop"
		addGenericSymbol(facts, match[2], kind, line, index+1, elixirBlockEnd(lines, index), parent, exported)
	}
}

func extractLuaDeclaration(facts *FileFacts, lines []string, index int, line string) {
	match := luaFunctionPattern.FindStringSubmatch(line)
	if match == nil {
		return
	}
	qualified := match[1]
	separator := strings.LastIndexAny(qualified, ".:")
	name := qualified
	parent := ""
	if separator >= 0 {
		parent = qualified[:separator]
		name = qualified[separator+1:]
	}
	kind := "function"
	if parent != "" {
		kind = "method"
	}
	addGenericSymbol(facts, name, kind, line, index+1, luaBlockEnd(lines, index), parent, !strings.HasPrefix(line, "local "))
}

func extractPHPDeclaration(facts *FileFacts, lines []string, index int, line string) {
	if match := phpTypePattern.FindStringSubmatch(line); match != nil {
		name := match[2]
		addGenericSymbol(facts, name, match[1], line, index+1, braceBlockEnd(lines, index), "", true)
		for _, parent := range parentsAfterKeywords(line, "extends", "implements") {
			facts.Inheritance = append(facts.Inheritance, InheritanceFact{Child: name, Parent: parent, Line: index + 1})
		}
		return
	}
	if match := phpFunctionPattern.FindStringSubmatch(line); match != nil {
		parent := enclosingType(facts.Symbols, index+1)
		kind := "function"
		if parent != "" {
			kind = "method"
		}
		end := index + 1
		if strings.Contains(line, "{") {
			end = braceBlockEnd(lines, index)
		}
		exported := !strings.Contains(line, "private ") && !strings.HasPrefix(match[1], "_")
		addGenericSymbol(facts, match[1], kind, line, index+1, end, parent, exported)
	}
}

func extractSwiftDeclaration(facts *FileFacts, lines []string, index int, line string) {
	if match := swiftTypePattern.FindStringSubmatch(line); match != nil {
		name := match[2]
		addGenericSymbol(facts, name, match[1], line, index+1, braceBlockEnd(lines, index), "", !strings.Contains(line, "private "))
		if colon := strings.Index(line, ":"); colon >= 0 {
			for _, parent := range splitParentNames(line[colon+1:]) {
				facts.Inheritance = append(facts.Inheritance, InheritanceFact{Child: name, Parent: parent, Line: index + 1})
			}
		}
		return
	}
	if match := swiftFunctionPattern.FindStringSubmatch(line); match != nil {
		parent := enclosingType(facts.Symbols, index+1)
		kind := "function"
		if parent != "" {
			kind = "method"
		}
		addGenericSymbol(facts, match[1], kind, line, index+1, braceBlockEnd(lines, index), parent, !strings.Contains(line, "private "))
	}
}

func extractScalaDeclaration(facts *FileFacts, lines []string, index int, line string) {
	if match := scalaTypePattern.FindStringSubmatch(line); match != nil {
		name := match[2]
		end := indentationBlockEnd(lines, index)
		if strings.Contains(line, "{") {
			end = braceBlockEnd(lines, index)
		}
		addGenericSymbol(facts, name, match[1], line, index+1, end, "", !strings.Contains(line, "private "))
		for _, parent := range parentsAfterKeywords(line, "extends", "with") {
			facts.Inheritance = append(facts.Inheritance, InheritanceFact{Child: name, Parent: parent, Line: index + 1})
		}
		return
	}
	if match := scalaFunctionPattern.FindStringSubmatch(line); match != nil {
		parent := enclosingType(facts.Symbols, index+1)
		kind := "function"
		if parent != "" {
			kind = "method"
		}
		end := indentationBlockEnd(lines, index)
		if strings.Contains(line, "{") {
			end = braceBlockEnd(lines, index)
		}
		addGenericSymbol(facts, match[1], kind, line, index+1, end, parent, !strings.Contains(line, "private "))
	}
}

func extractHaskellDeclaration(facts *FileFacts, lines []string, index int, line string) {
	if match := haskellTypePattern.FindStringSubmatch(line); match != nil {
		addGenericSymbol(facts, match[2], match[1], line, index+1, indentationBlockEnd(lines, index), "", true)
		return
	}
	match := haskellSignature.FindStringSubmatch(line)
	if match == nil {
		match = haskellBinding.FindStringSubmatch(line)
	}
	if match == nil || genericKeyword(match[1]) || hasSymbolNamed(facts.Symbols, match[1]) {
		return
	}
	addGenericSymbol(facts, match[1], "function", line, index+1, indentationBlockEnd(lines, index), "", !strings.HasPrefix(match[1], "_"))
}

func exportedOutlineName(name string) bool {
	name = strings.TrimPrefix(name, "~")
	return name != "" && !strings.HasPrefix(name, "_")
}

func hasSymbolNamed(symbols []SymbolFact, name string) bool {
	for _, symbol := range symbols {
		if symbol.Name == name {
			return true
		}
	}
	return false
}

func extractGenericImports(facts *FileFacts, lines []string) {
	languagePatterns := genericImportPatterns[facts.Language]
	if slicesContain([]string{"svelte", "vue", "astro"}, facts.Language) {
		languagePatterns = genericImportPatterns["typescript"]
	}
	for index, line := range lines {
		for _, pattern := range languagePatterns {
			for _, match := range pattern.FindAllStringSubmatch(line, -1) {
				target := ""
				for _, group := range match[1:] {
					if group != "" {
						target = strings.TrimSpace(group)
						break
					}
				}
				if target != "" {
					facts.Imports = append(facts.Imports, ImportFact{Target: target, Line: index + 1})
				}
			}
		}
	}
}

func extractGenericUses(facts *FileFacts, lines []string) {
	for index, line := range lines {
		if len(line) > maxExtractLine {
			continue
		}
		lineNo := index + 1
		owner := enclosingCallable(facts.Symbols, lineNo)
		if !outlineLanguage(facts.Language) {
			callLine := structuralText(line)
			for _, match := range genericCallPattern.FindAllString(callLine, -1) {
				callee := strings.ReplaceAll(strings.TrimSpace(strings.TrimSuffix(match, "(")), " ", "")
				part := callee
				if dot := strings.LastIndexByte(part, '.'); dot >= 0 {
					part = part[dot+1:]
				}
				if genericKeyword(part) || declarationAt(facts.Symbols, lineNo, part) {
					continue
				}
				facts.Calls = append(facts.Calls, CallFact{Caller: owner, Callee: callee, Line: lineNo})
			}
			if facts.Language == "shell" {
				extractShellCalls(facts, line, lineNo, owner)
			} else if facts.Language == "ruby" {
				extractRubyBareCall(facts, line, lineNo, owner)
			} else if facts.Language == "elixir" {
				extractElixirBareRoute(facts, line, lineNo, owner)
			}
			for _, match := range routeVerbArgumentPattern.FindAllStringSubmatchIndex(line, -1) {
				if !outsideQuotedString(line, match[0]) {
					continue
				}
				path := normalizeExtractedRoute(line[match[4]:match[5]])
				if path == "" {
					continue
				}
				routeOwner := owner
				if routeOwner == "" {
					routeOwner = enclosingType(facts.Symbols, lineNo)
				}
				facts.Routes = append(facts.Routes, RouteFact{
					Method: strings.ToUpper(line[match[2]:match[3]]), Path: path, Owner: routeOwner, Line: lineNo,
				})
			}
			for _, match := range routeCallPattern.FindAllStringSubmatchIndex(line, -1) {
				if !outsideQuotedString(line, match[0]) {
					continue
				}
				verb := line[match[2]:match[3]]
				rawPath := line[match[4]:match[5]]
				path := normalizeExtractedRoute(rawPath)
				if path == "" && (strings.HasPrefix(strings.TrimSpace(line), "@") ||
					strings.EqualFold(verb, "path") || strings.EqualFold(verb, "re_path") ||
					strings.EqualFold(verb, "url")) {
					path = normalizeExtractedRoute("/" + strings.TrimPrefix(rawPath, "/"))
				}
				if path == "" {
					continue
				}
				method := routeMethod(verb)
				routeOwner := owner
				if routeOwner == "" {
					routeOwner = enclosingType(facts.Symbols, lineNo)
				}
				if routeOwner == "" || strings.HasPrefix(strings.TrimSpace(line), "@") {
					if next := nextCallable(facts.Symbols, lineNo, 3); next != "" {
						routeOwner = next
					}
				}
				facts.Routes = append(facts.Routes, RouteFact{Method: method, Path: path, Owner: routeOwner, Line: lineNo})
			}
		}
		for _, value := range quotedValues(line) {
			if kind := literalKind(value); kind != "" {
				facts.Literals = append(facts.Literals, LiteralFact{Value: value, Kind: kind, Line: lineNo})
				if kind == "path" && configLanguage(facts.Language) && configRouteLine(line) {
					facts.Routes = append(facts.Routes, RouteFact{Path: normalizeExtractedRoute(value), Line: lineNo})
				}
			}
		}
		for _, pattern := range envPatterns {
			for _, match := range pattern.FindAllStringSubmatch(line, -1) {
				facts.Literals = append(facts.Literals, LiteralFact{Value: match[1], Kind: "env", Line: lineNo})
			}
		}
		if (facts.Language == "yaml" || facts.Language == "toml") && configRouteLine(line) {
			if value := unquotedConfigRoute(line); value != "" {
				facts.Literals = append(facts.Literals, LiteralFact{Value: value, Kind: "path", Line: lineNo})
				facts.Routes = append(facts.Routes, RouteFact{Path: normalizeExtractedRoute(value), Line: lineNo})
			}
		}
	}
}

func composeRoutePrefixes(facts *FileFacts, lines []string) {
	type routePrefix struct {
		path string
		line int
	}
	prefixes := make(map[string]routePrefix)
	prefixLines := make(map[int]struct{})
	for index, line := range lines {
		match := routePrefixPattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		prefix := normalizeExtractedRoute(match[2])
		if prefix == "" {
			prefix = normalizeExtractedRoute("/" + strings.TrimPrefix(strings.TrimSpace(match[2]), "/"))
		}
		if prefix == "" {
			continue
		}
		lineNo := index + 1
		for _, symbol := range facts.Symbols {
			if symbol.Kind != "class" || symbol.StartLine <= lineNo || symbol.StartLine > lineNo+4 {
				continue
			}
			prefixes[symbol.Name] = routePrefix{path: prefix, line: lineNo}
			prefixes[symbol.Qualified] = routePrefix{path: prefix, line: lineNo}
			prefixLines[lineNo] = struct{}{}
			break
		}
	}
	if len(prefixes) == 0 {
		return
	}
	composed := facts.Routes[:0]
	for _, route := range facts.Routes {
		if _, classPrefix := prefixLines[route.Line]; classPrefix {
			continue
		}
		owner := route.Owner
		className := owner
		if dot := strings.IndexByte(className, '.'); dot >= 0 {
			className = className[:dot]
		}
		prefix, ok := prefixes[className]
		if !ok {
			composed = append(composed, route)
			continue
		}
		route.Path = joinExtractedRoute(prefix.path, route.Path)
		composed = append(composed, route)
	}
	facts.Routes = composed
}

func composeNestedRoutePrefixes(facts *FileFacts, lines []string) {
	type routeScope struct {
		path       string
		start, end int
	}
	scopes := make([]routeScope, 0)
	for index, line := range lines {
		match := nestedRoutePrefixPattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		path := match[1]
		end := elixirBlockEnd(lines, index)
		if path == "" {
			path = match[2]
			end = braceBlockEnd(lines, index)
		}
		rawPath := strings.TrimSpace(path)
		path = normalizeExtractedRoute(rawPath)
		if path == "" {
			path = normalizeExtractedRoute("/" + strings.TrimPrefix(rawPath, "/"))
		}
		if path == "" || end <= index+1 {
			continue
		}
		scopes = append(scopes, routeScope{path: path, start: index + 1, end: end})
	}
	if len(scopes) == 0 {
		return
	}
	for index := range facts.Routes {
		route := &facts.Routes[index]
		prefix := ""
		for _, scope := range scopes {
			if route.Line > scope.start && route.Line <= scope.end {
				prefix = joinExtractedRoute(prefix, scope.path)
			}
		}
		if prefix != "" {
			route.Path = joinExtractedRoute(prefix, route.Path)
		}
	}
}

func joinExtractedRoute(prefix, suffix string) string {
	prefix = strings.TrimSuffix(prefix, "/")
	suffix = strings.TrimPrefix(suffix, "/")
	if suffix == "" {
		return normalizeExtractedRoute(prefix)
	}
	return normalizeExtractedRoute(prefix + "/" + suffix)
}

func outlineLanguage(language string) bool {
	return configLanguage(language) || language == "markdown"
}

func configLanguage(language string) bool {
	return language == "yaml" || language == "json" || language == "toml" || language == "hcl" || language == "env"
}

func outsideQuotedString(line string, offset int) bool {
	quote := byte(0)
	escaped := false
	for index := 0; index < offset && index < len(line); index++ {
		value := line[index]
		if quote != 0 {
			if escaped {
				escaped = false
			} else if value == '\\' {
				escaped = true
			} else if value == quote {
				quote = 0
			}
			continue
		}
		if value == '\'' || value == '"' || value == '`' {
			quote = value
		}
	}
	return quote == 0
}

func extractShellCalls(facts *FileFacts, line string, lineNo int, owner string) {
	segments := strings.FieldsFunc(structuralText(line), func(r rune) bool {
		return r == ';' || r == '|' || r == '&'
	})
	for _, segment := range segments {
		fields := strings.Fields(segment)
		for len(fields) > 0 && shellAssignmentPattern.MatchString(fields[0]) {
			fields = fields[1:]
		}
		if len(fields) == 0 {
			continue
		}
		command := fields[0]
		switch command {
		case "if", "then", "else", "elif", "fi", "for", "while", "until", "do", "done", "case", "esac", "function", "source", ".", "export", "local", "readonly", "declare", "return":
			continue
		}
		if strings.Contains(command, "=") || strings.HasPrefix(command, "-") || command == "{" || command == "}" {
			continue
		}
		if strings.HasSuffix(command, "()") {
			name := strings.TrimSuffix(command, "()")
			if declarationAt(facts.Symbols, lineNo, name) {
				continue
			}
		}
		facts.Calls = append(facts.Calls, CallFact{Caller: owner, Callee: command, Line: lineNo})
	}
}

func extractRubyBareCall(facts *FileFacts, line string, lineNo int, owner string) {
	if route := rubyBareRoutePattern.FindStringSubmatch(line); route != nil {
		if path := normalizeExtractedRoute(route[2]); path != "" {
			facts.Routes = append(facts.Routes, RouteFact{Method: routeMethod(route[1]), Path: path, Owner: owner, Line: lineNo})
		}
	}
	match := rubyBareCallPattern.FindStringSubmatch(structuralText(line))
	if match == nil || genericKeyword(match[1]) {
		return
	}
	switch match[1] {
	case "def", "class", "module", "require", "require_relative", "load", "include", "extend", "attr_reader", "attr_writer", "attr_accessor":
		return
	}
	facts.Calls = append(facts.Calls, CallFact{Caller: owner, Callee: match[1], Line: lineNo})
}

func extractElixirBareRoute(facts *FileFacts, line string, lineNo int, owner string) {
	match := elixirRoutePattern.FindStringSubmatch(line)
	if match == nil {
		return
	}
	path := normalizeExtractedRoute(match[2])
	if path == "" {
		return
	}
	if owner == "" {
		owner = enclosingType(facts.Symbols, lineNo)
	}
	facts.Routes = append(facts.Routes, RouteFact{
		Method: routeMethod(match[1]), Path: path, Owner: owner, Line: lineNo,
	})
}

func extractYAMLOutline(facts *FileFacts, lines []string) {
	keyPattern := yamlKeyPattern
	type level struct {
		indent int
		name   string
	}
	stack := make([]level, 0)
	for index, line := range lines {
		match := keyPattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		indent := len(match[1])
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		parent := ""
		if len(stack) > 0 {
			parent = stack[len(stack)-1].name
		}
		name := match[2]
		end := indentationExtent(lines, index, indent)
		addGenericSymbol(facts, name, "key", strings.TrimSpace(line), index+1, end, parent, false)
		qualified := name
		if parent != "" {
			qualified = parent + "." + name
		}
		stack = append(stack, level{indent: indent, name: qualified})
	}
}

func extractJSONOutline(facts *FileFacts, lines []string) {
	keyPattern := jsonKeyPattern
	for index, line := range lines {
		for _, match := range keyPattern.FindAllStringSubmatch(line, -1) {
			addGenericSymbol(facts, match[1], "key", match[0], index+1, index+1, "", false)
		}
	}
}

func extractTOMLOutline(facts *FileFacts, lines []string) {
	tablePattern := tomlTablePattern
	keyPattern := tomlKeyPattern
	parent := ""
	for index, line := range lines {
		if match := tablePattern.FindStringSubmatch(line); match != nil {
			parent = match[1]
			end := len(lines)
			for next := index + 1; next < len(lines); next++ {
				if tablePattern.MatchString(lines[next]) {
					end = next
					break
				}
			}
			addGenericSymbol(facts, parent, "table", strings.TrimSpace(line), index+1, end, "", false)
			continue
		}
		if match := keyPattern.FindStringSubmatch(line); match != nil {
			addGenericSymbol(facts, match[1], "key", strings.TrimSpace(line), index+1, index+1, parent, false)
		}
	}
}

func extractHCLOutline(facts *FileFacts, lines []string) {
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		match := hclBlockPattern.FindStringSubmatch(trimmed)
		if match == nil {
			continue
		}
		labels := make([]string, 0, 2)
		for _, label := range match[2:] {
			if label != "" {
				labels = append(labels, label)
			}
		}
		name := match[1]
		if len(labels) > 0 {
			name = strings.Join(labels, ".")
		}
		addGenericSymbol(facts, name, match[1], trimmed, index+1, braceBlockEnd(lines, index), "", false)
	}
	for index, line := range lines {
		match := hclKeyPattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		parent := enclosingType(facts.Symbols, index+1)
		addGenericSymbol(facts, match[1], "key", strings.TrimSpace(line), index+1, index+1, parent, false)
	}
}

func extractEnvOutline(facts *FileFacts, lines []string) {
	for index, line := range lines {
		match := envAssignmentPattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		name := match[1]
		addGenericSymbol(facts, name, "key", strings.TrimSpace(line), index+1, index+1, "", false)
		if environmentKey.MatchString(name) {
			facts.Literals = append(facts.Literals, LiteralFact{Value: name, Kind: "env", Line: index + 1})
		}
	}
}

func extractMarkdownOutline(facts *FileFacts, lines []string) {
	headingPattern := markdownHeadingPattern
	type heading struct {
		level int
		name  string
	}
	stack := make([]heading, 0)
	for index, line := range lines {
		match := headingPattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		level := len(match[1])
		name := strings.TrimSpace(match[2])
		for len(stack) > 0 && stack[len(stack)-1].level >= level {
			stack = stack[:len(stack)-1]
		}
		parent := ""
		if len(stack) > 0 {
			parent = stack[len(stack)-1].name
		}
		end := len(lines)
		for next := index + 1; next < len(lines); next++ {
			if nextMatch := headingPattern.FindStringSubmatch(lines[next]); nextMatch != nil && len(nextMatch[1]) <= level {
				end = next
				break
			}
		}
		addGenericSymbol(facts, name, "heading", strings.TrimSpace(line), index+1, end, parent, false)
		stack = append(stack, heading{level: level, name: name})
	}
}

func addGenericSymbol(facts *FileFacts, name, kind, signature string, start, end int, parent string, exported bool) {
	if name == "" {
		return
	}
	qualified := name
	if parent != "" {
		qualified = parent + "." + name
	}
	facts.Symbols = append(facts.Symbols, SymbolFact{
		Name: name, Qualified: qualified, Kind: kind, Signature: strings.TrimSpace(signature),
		StartLine: start, EndLine: max(start, end), Parent: parent, Exported: exported,
	})
}

func sanitizeSourceLines(lines []string, language string) []string {
	out := make([]string, len(lines))
	blockComments := slicesContain(
		[]string{"typescript", "javascript", "svelte", "vue", "astro", "rust", "java", "kotlin", "c", "cpp", "php", "swift", "scala", "hcl"},
		language,
	)
	hashComments := slicesContain([]string{"python", "ruby", "shell", "yaml", "toml", "hcl", "env", "elixir", "php"}, language)
	dashComments := language == "lua" || language == "haskell"
	inBlock := false
	for index, line := range lines {
		if language == "markdown" {
			out[index] = line
			continue
		}
		var b strings.Builder
		quote := rune(0)
		escaped := false
		runes := []rune(line)
		for position := 0; position < len(runes); position++ {
			r := runes[position]
			next := rune(0)
			if position+1 < len(runes) {
				next = runes[position+1]
			}
			if inBlock {
				if r == '*' && next == '/' {
					inBlock = false
					position++
				}
				continue
			}
			if quote != 0 {
				b.WriteRune(r)
				if escaped {
					escaped = false
				} else if r == '\\' {
					escaped = true
				} else if r == quote {
					quote = 0
				}
				continue
			}
			if r == '\'' || r == '"' || r == '`' {
				quote = r
				b.WriteRune(r)
				continue
			}
			if blockComments && r == '/' && next == '*' {
				inBlock = true
				position++
				continue
			}
			if blockComments && r == '/' && next == '/' {
				break
			}
			if dashComments && r == '-' && next == '-' {
				break
			}
			if hashComments && r == '#' {
				break
			}
			b.WriteRune(r)
		}
		out[index] = b.String()
	}
	return out
}

func braceBlockEnd(lines []string, start int) int {
	depth := 0
	seen := false
	for index := start; index < len(lines); index++ {
		for _, r := range structuralText(lines[index]) {
			switch r {
			case '{':
				depth++
				seen = true
			case '}':
				if seen {
					depth--
					if depth == 0 {
						return index + 1
					}
				}
			}
		}
		if !seen && index > start+2 {
			break
		}
	}
	return start + 1
}

func structuralText(line string) string {
	var b strings.Builder
	quote := rune(0)
	escaped := false
	for _, r := range line {
		if quote != 0 {
			if escaped {
				escaped = false
			} else if r == '\\' {
				escaped = true
			} else if r == quote {
				quote = 0
			}
			b.WriteRune(' ')
			continue
		}
		if r == '\'' || r == '"' || r == '`' {
			quote = r
			b.WriteRune(' ')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func indentationBlockEnd(lines []string, start int) int {
	indent := leadingIndent(lines[start])
	end := start + 1
	for index := start + 1; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) == "" {
			continue
		}
		if leadingIndent(lines[index]) <= indent {
			break
		}
		end = index + 1
	}
	return end
}

func indentationExtent(lines []string, start, indent int) int {
	end := start + 1
	for index := start + 1; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) == "" {
			continue
		}
		if leadingIndent(lines[index]) <= indent {
			break
		}
		end = index + 1
	}
	return end
}

func leadingIndent(line string) int {
	count := 0
	for _, r := range line {
		if r == ' ' {
			count++
		} else if r == '\t' {
			count += 4
		} else {
			break
		}
	}
	return count
}

func elixirBlockEnd(lines []string, start int) int {
	depth := 0
	for index := start; index < len(lines); index++ {
		line := strings.TrimSpace(structuralText(lines[index]))
		if strings.Contains(line, " do") && !strings.Contains(line, "do:") {
			depth++
		}
		if strings.Contains(line, "fn ") && strings.Contains(line, "->") {
			depth++
		}
		if line == "end" || strings.HasPrefix(line, "end ") {
			depth--
			if depth <= 0 {
				return index + 1
			}
		}
	}
	return start + 1
}

func luaBlockEnd(lines []string, start int) int {
	depth := 0
	for index := start; index < len(lines); index++ {
		line := strings.TrimSpace(structuralText(lines[index]))
		switch {
		case strings.HasPrefix(line, "function "), strings.HasPrefix(line, "local function "):
			depth++
		case strings.HasPrefix(line, "if ") && strings.HasSuffix(line, " then"):
			depth++
		case strings.HasPrefix(line, "for ") && strings.HasSuffix(line, " do"):
			depth++
		case strings.HasPrefix(line, "while ") && strings.HasSuffix(line, " do"):
			depth++
		case line == "do", strings.HasPrefix(line, "repeat"):
			depth++
		}
		if line == "end" || strings.HasPrefix(line, "end ") || strings.HasPrefix(line, "until ") {
			depth--
			if depth <= 0 {
				return index + 1
			}
		}
	}
	return start + 1
}

func rubyBlockEnd(lines []string, start int) int {
	depth := 0
	for index := start; index < len(lines); index++ {
		trimmed := strings.TrimSpace(lines[index])
		if rubyBlockOpenPattern.MatchString(trimmed) {
			depth++
		}
		if trimmed == "end" || strings.HasPrefix(trimmed, "end ") {
			depth--
			if depth <= 0 {
				return index + 1
			}
		}
	}
	return start + 1
}

func enclosingType(symbols []SymbolFact, line int) string {
	bestStart := -1
	name := ""
	for _, symbol := range symbols {
		if !typeLike(symbol.Kind) || symbol.StartLine >= line || line > symbol.EndLine {
			continue
		}
		if symbol.StartLine > bestStart {
			bestStart = symbol.StartLine
			name = symbol.Name
		}
	}
	return name
}

func typeLike(kind string) bool {
	switch kind {
	case "class", "interface", "enum", "namespace", "struct", "trait", "union", "impl", "object", "record", "module", "protocol", "actor", "extension", "resource", "data", "variable", "output", "provider", "terraform", "locals":
		return true
	default:
		return false
	}
}

func enclosingCallable(symbols []SymbolFact, line int) string {
	bestStart := -1
	name := ""
	for _, symbol := range symbols {
		if symbol.Kind != "function" && symbol.Kind != "method" {
			continue
		}
		if symbol.StartLine <= line && line <= symbol.EndLine && symbol.StartLine >= bestStart {
			bestStart = symbol.StartLine
			name = symbol.Qualified
		}
	}
	return name
}

func nextCallable(symbols []SymbolFact, line, distance int) string {
	for _, symbol := range symbols {
		if (symbol.Kind == "function" || symbol.Kind == "method") && symbol.StartLine > line && symbol.StartLine <= line+distance {
			return symbol.Qualified
		}
	}
	return ""
}

func declarationAt(symbols []SymbolFact, line int, name string) bool {
	for _, symbol := range symbols {
		if symbol.StartLine == line && symbol.Name == name {
			return true
		}
	}
	return false
}

func genericKeyword(value string) bool {
	switch value {
	case "if", "for", "while", "switch", "catch", "with", "return", "sizeof", "typeof", "function", "def", "fn", "class", "super", "when", "unless":
		return true
	default:
		return false
	}
}

func parentsAfterKeywords(value string, keywords ...string) []string {
	parents := make([]string, 0)
	for _, keyword := range keywords {
		pattern := regexp.MustCompile(`\b` + keyword + `\s+([^{}]+)`)
		match := pattern.FindStringSubmatch(value)
		if match == nil {
			continue
		}
		segment := match[1]
		for _, other := range keywords {
			if index := strings.Index(segment, " "+other+" "); index >= 0 {
				segment = segment[:index]
			}
		}
		parents = append(parents, splitParentNames(segment)...)
	}
	return parents
}

func splitParentNames(value string) []string {
	values := strings.Split(value, ",")
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if open := strings.IndexAny(value, "<("); open >= 0 {
			value = value[:open]
		}
		if match := identifierPattern.FindString(value); match != "" && match != "public" && match != "private" {
			out = append(out, match)
		}
	}
	return out
}

func rustImplParts(value string) (string, string) {
	parts := strings.SplitN(value, " for ", 2)
	if len(parts) == 2 {
		trait := firstIdentifier(parts[0])
		child := firstIdentifier(parts[1])
		return trait, child
	}
	return "", firstIdentifier(value)
}

func firstIdentifier(value string) string {
	matches := identifierPattern.FindAllString(value, -1)
	if len(matches) == 0 {
		return ""
	}
	return matches[len(matches)-1]
}

func routeMethod(value string) string {
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "add_") {
		lower = strings.TrimPrefix(lower, "add_")
	}
	if strings.HasSuffix(lower, "mapping") {
		lower = strings.TrimSuffix(lower, "mapping")
	}
	switch lower {
	case "get", "post", "put", "patch", "delete", "options", "head", "connect", "trace":
		return strings.ToUpper(lower)
	case "fetch", "redirect", "respondredirect", "redirect_to":
		return "GET"
	default:
		return ""
	}
}

func quotedValues(line string) []string {
	values := make([]string, 0)
	runes := []rune(line)
	for index := 0; index < len(runes); index++ {
		quote := runes[index]
		if quote != '\'' && quote != '"' && quote != '`' {
			continue
		}
		start := index
		index++
		escaped := false
		for ; index < len(runes); index++ {
			if escaped {
				escaped = false
				continue
			}
			if runes[index] == '\\' {
				escaped = true
				continue
			}
			if runes[index] != quote {
				continue
			}
			raw := string(runes[start : index+1])
			if quote == '`' {
				values = append(values, string(runes[start+1:index]))
			} else if value, err := strconv.Unquote(raw); err == nil {
				values = append(values, value)
			} else {
				values = append(values, string(runes[start+1:index]))
			}
			break
		}
	}
	return values
}

func configRouteLine(line string) bool {
	separator := strings.IndexAny(line, ":=")
	if separator < 0 {
		return false
	}
	key := strings.TrimSpace(line[:separator])
	key = strings.Trim(key, "\"'")
	lower := strings.ToLower(key)
	if normalizeRoute(key) != "" {
		return true
	}
	return lower == "route" || lower == "endpoint" || lower == "url" || lower == "uri" ||
		strings.HasSuffix(lower, "_route") || strings.HasSuffix(lower, "_endpoint") ||
		strings.HasSuffix(lower, "_url") || strings.HasSuffix(lower, "_uri")
}

func unquotedConfigRoute(line string) string {
	colon := strings.IndexAny(line, ":=")
	if colon < 0 {
		return ""
	}
	value := strings.TrimSpace(line[colon+1:])
	if value == "" || strings.HasPrefix(value, "\"") || strings.HasPrefix(value, "'") {
		return ""
	}
	if space := strings.IndexFunc(value, unicode.IsSpace); space >= 0 {
		value = value[:space]
	}
	if normalizeRoute(value) == "" {
		return ""
	}
	return value
}
