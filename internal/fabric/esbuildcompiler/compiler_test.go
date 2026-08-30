//go:build !fabric_disabled

package esbuildcompiler

import (
	"testing"

	"github.com/asx8678/ultra/internal/fabric"
	"github.com/stretchr/testify/require"
)

func TestCompilerTranspilesTypeScriptFunctionBody(t *testing.T) {
	t.Parallel()
	result, err := New().Compile(t.Context(), fabric.CompileRequest{
		SourceName: "fabric://test/program.ts",
		Source: `
			const paths: string[] = ["a", "b"];
			const values = await Promise.all(paths.map((path) => host.view({ path })));
			return { values, literal: π.body };
		`,
		Declarations: `declare const host: {
			view(args: { path: string }): Promise<unknown>;
		};`,
		NamedStrings: map[string]struct{}{"body": {}},
	})
	require.NoError(t, err)
	require.Empty(t, result.Diagnostics)
	require.Contains(t, result.JavaScript, "__ultra_fabric_compiled__")
	require.NotContains(t, result.JavaScript, "string[]")
	require.NotEmpty(t, result.SourceMap)
}

func TestCompilerReportsSyntaxWithoutOutput(t *testing.T) {
	t.Parallel()
	result, err := New().Compile(t.Context(), fabric.CompileRequest{
		SourceName: "fabric://test/program.ts",
		Source:     `const broken: = 1; return broken;`,
	})
	require.NoError(t, err)
	require.Empty(t, result.JavaScript)
	require.NotEmpty(t, result.Diagnostics)
	require.Equal(t, "syntax", result.Diagnostics[0].Category)
	require.Equal(t, 1, result.Diagnostics[0].Line)
}
