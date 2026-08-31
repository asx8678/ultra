package repograph

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLanguageForPathRecognizesRepositoryFormats(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"Dockerfile.dev": "shell",
		"GNUmakefile":    "shell",
		"build/rules.mk": "shell",
		".envrc":         "shell",
		"config.env":     "env",
		"app.tfvars":     "hcl",
		"Guide.markdown": "markdown",
		"Page.svelte":    "svelte",
		"Page.vue":       "vue",
		"Page.astro":     "astro",
	}
	for path, expected := range cases {
		require.Equal(t, expected, languageForPath(path), path)
	}
}

func TestNormalizeRouteCanonicalizesDynamicSegments(t *testing.T) {
	t.Parallel()
	for _, route := range []string{
		"/users/:id",
		"/users/{id}",
		"/users/[id]",
		"/users/<id>",
		"/users/${id}",
		"/users/*",
	} {
		require.Equal(t, "/users/{*}", normalizeRoute(route), route)
	}
	require.Equal(t, "/", normalizeRoute("/"))
	require.Empty(t, normalizeRoute("users/{id}"))
}
