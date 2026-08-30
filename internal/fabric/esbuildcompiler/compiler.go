//go:build !fabric_disabled

// Package esbuildcompiler implements Fabric's pragmatic TypeScript compiler.
package esbuildcompiler

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/asx8678/ultra/internal/fabric"
	"github.com/evanw/esbuild/pkg/api"
)

// Compiler checks TypeScript syntax and transpiles it to isolated ES2022.
// Registry JSON Schema validation remains authoritative at invocation time.
type Compiler struct{}

// New creates a compiler with no ambient filesystem or module resolution.
func New() *Compiler { return &Compiler{} }

// Compile transpiles one function-body program without executing guest code.
func (*Compiler) Compile(ctx context.Context, request fabric.CompileRequest) (fabric.CompileResult, error) {
	if err := ctx.Err(); err != nil {
		return fabric.CompileResult{}, err
	}
	if strings.TrimSpace(request.Source) == "" {
		return fabric.CompileResult{Diagnostics: []fabric.ProgramDiagnostic{{
			Category: "syntax", Message: "Fabric program is empty", Line: 1, Column: 1,
		}}}, nil
	}

	// Declarations are parsed in the same compilation unit as the guest. They
	// are advisory; the registry validates prepared arguments authoritatively.
	wrapped := request.Declarations + namedStringDeclarations(request.NamedStrings) +
		"\nasync function __ultra_fabric_compiled__() {\n" + request.Source + "\n}\n"
	result := api.Transform(wrapped, api.TransformOptions{
		Loader:            api.LoaderTS,
		Target:            api.ES2022,
		Format:            api.FormatDefault,
		Sourcefile:        request.SourceName,
		Sourcemap:         api.SourceMapExternal,
		SourcesContent:    api.SourcesContentInclude,
		LegalComments:     api.LegalCommentsNone,
		TreeShaking:       api.TreeShakingFalse,
		IgnoreAnnotations: true,
	})
	if err := ctx.Err(); err != nil {
		return fabric.CompileResult{}, err
	}
	if len(result.Errors) > 0 {
		return fabric.CompileResult{
			Diagnostics: diagnostics(result.Errors, declarationLines(request, wrapped)),
		}, nil
	}
	javascript := string(result.Code) + "\nreturn __ultra_fabric_compiled__();\n"
	return fabric.CompileResult{JavaScript: javascript, SourceMap: string(result.Map)}, nil
}

func namedStringDeclarations(names map[string]struct{}) string {
	if len(names) == 0 {
		return "\ndeclare const π: Readonly<Record<string, string>>;\n"
	}
	keys := make([]string, 0, len(names))
	for name := range names {
		encoded, _ := json.Marshal(name)
		keys = append(keys, string(encoded)+": string")
	}
	// Ordering is not observable to the checker, but deterministic output helps
	// diagnostics and tests.
	slices.Sort(keys)
	return "\ndeclare const π: Readonly<{ " + strings.Join(keys, "; ") + " }>;\n"
}

func diagnostics(messages []api.Message, prefixLines int) []fabric.ProgramDiagnostic {
	result := make([]fabric.ProgramDiagnostic, 0, len(messages))
	for _, message := range messages {
		diagnostic := fabric.ProgramDiagnostic{Category: "syntax", Message: message.Text}
		if message.Location != nil {
			diagnostic.Line = max(1, message.Location.Line-prefixLines-1)
			diagnostic.Column = message.Location.Column + 1
			diagnostic.Length = message.Location.Length
		}
		result = append(result, diagnostic)
	}
	return result
}

func declarationLines(request fabric.CompileRequest, wrapped string) int {
	marker := "async function __ultra_fabric_compiled__() {"
	index := strings.Index(wrapped, marker)
	if index < 0 {
		return 0
	}
	return strings.Count(wrapped[:index], "\n")
}

var _ fabric.ProgramCompiler = (*Compiler)(nil)
