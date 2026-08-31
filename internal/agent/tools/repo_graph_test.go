package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"

	"github.com/asx8678/ultra/internal/fabric"
	"github.com/asx8678/ultra/internal/repograph"
)

func TestRepoGraphToolsExposeBoundedProgressiveWorkflow(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "service.go"), []byte(`package service

func LoadAccount() string { return "ACCOUNT_STORE_URL" }
func HandleAccount() string { return LoadAccount() }
`), 0o644))
	manager, err := repograph.NewManager(root, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })

	toolSet := []fantasy.AgentTool{
		NewRepoSketchTool(manager),
		NewRepoFocusTool(manager),
		NewRepoDwellTool(manager),
		NewRepoImpactTool(manager),
	}
	require.Equal(t, []string{
		RepoSketchToolName, RepoFocusToolName, RepoDwellToolName, RepoImpactToolName,
	}, []string{
		toolSet[0].Info().Name, toolSet[1].Info().Name, toolSet[2].Info().Name, toolSet[3].Info().Name,
	})
	provider, err := NewUltraFabricProvider(toolSet)
	require.NoError(t, err)
	descriptors, err := provider.List(t.Context(), fabric.ListActionsRequest{}, fabric.DiscoveryContext{})
	require.NoError(t, err)
	require.Len(t, descriptors, 4)
	for _, descriptor := range descriptors {
		require.Equal(t, fabric.RiskRead, descriptor.Risk)
		require.NotNil(t, descriptor.Effect)
		if descriptor.Name == RepoFocusToolName || descriptor.Name == RepoDwellToolName {
			require.False(t, descriptor.Effect.Commutative, descriptor.Name)
		} else {
			require.True(t, descriptor.Effect.Commutative, descriptor.Name)
		}
	}

	focusInfo := toolSet[1].Info()
	require.Equal(t, []string{"query"}, focusInfo.Required)
	querySchema := focusInfo.Parameters["query"].(map[string]any)
	require.Equal(t, 1, querySchema["minLength"])
	require.Equal(t, maxRepoGraphQueryBytes, querySchema["maxLength"])
	pathSchema := focusInfo.Parameters["path"].(map[string]any)
	require.Equal(t, maxRepoGraphInputBytes, pathSchema["maxLength"])
	kindSchema := focusInfo.Parameters["kind"].(map[string]any)
	require.Equal(t, []string{"symbol", "file", "route", "literal", "env"}, kindSchema["enum"])
	tokenSchema := focusInfo.Parameters["max_tokens"].(map[string]any)
	require.Equal(t, minRepoGraphTokens, tokenSchema["minimum"])
	require.Equal(t, maxRepoGraphTokens, tokenSchema["maximum"])
	require.Equal(t, 4000, tokenSchema["default"])
	impactInfo := toolSet[3].Info()
	filesSchema := impactInfo.Parameters["files"].(map[string]any)
	require.Equal(t, 256, filesSchema["maxItems"])
	fileItemSchema := filesSchema["items"].(map[string]any)
	require.Equal(t, 1, fileItemSchema["minLength"])
	uncommittedSchema := impactInfo.Parameters["uncommitted"].(map[string]any)
	require.Equal(t, false, uncommittedSchema["default"])

	var fabricFocusSchema map[string]any
	for _, descriptor := range descriptors {
		if descriptor.Name == RepoFocusToolName {
			require.NoError(t, json.Unmarshal(descriptor.InputSchema, &fabricFocusSchema))
		}
	}
	require.Equal(t, false, fabricFocusSchema["additionalProperties"])
	properties := fabricFocusSchema["properties"].(map[string]any)
	fabricTokens := properties["max_tokens"].(map[string]any)
	require.Equal(t, float64(minRepoGraphTokens), fabricTokens["minimum"])
	require.Equal(t, float64(maxRepoGraphTokens), fabricTokens["maximum"])

	sketch := runRepoGraphTool(t, context.Background(), toolSet[0], RepoSketchParams{MaxTokens: 256})
	require.False(t, sketch.IsError, sketch.Content)
	require.Contains(t, sketch.Content, "LoadAccount")

	ctx := context.WithValue(context.Background(), SessionIDContextKey, "session-graph")
	focus := runRepoGraphTool(t, ctx, toolSet[1], RepoFocusParams{Query: "HandleAccount", MaxTokens: 256})
	require.False(t, focus.IsError, focus.Content)
	require.Contains(t, focus.Content, "HandleAccount")
	require.Contains(t, focus.Content, "LoadAccount")
	var focusMetadata RepoGraphResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(focus.Metadata), &focusMetadata))
	require.Equal(t, "focus", focusMetadata.Operation)
	require.Equal(t, "ready", focusMetadata.Status)
	require.NotZero(t, focusMetadata.Generation)
	require.Equal(t, renderedRepoGraphHitCount(focus.Content), focusMetadata.ResultCount)
	for _, window := range focusMetadata.SuggestedReads {
		require.Contains(t, focus.Content, window.Path)
	}

	defaultBudget := runRepoGraphTool(t, ctx, toolSet[1], RepoFocusParams{
		Query: "LoadAccount", Fresh: true,
	})
	require.False(t, defaultBudget.IsError, defaultBudget.Content)
	var defaultMetadata RepoGraphResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(defaultBudget.Metadata), &defaultMetadata))
	require.Equal(t, 4000, defaultMetadata.MaxTokens)

	missingCtx := context.WithValue(context.Background(), SessionIDContextKey, "session-missing")
	missing := runRepoGraphTool(t, missingCtx, toolSet[1], RepoFocusParams{
		Query: "symbol_that_does_not_exist_74b1", Fresh: true, MaxTokens: 256,
	})
	require.False(t, missing.IsError, missing.Content)
	var missingMetadata RepoGraphResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(missing.Metadata), &missingMetadata))
	require.Equal(t, "no_matches", missingMetadata.Status)
	require.Zero(t, missingMetadata.ResultCount)
	require.Zero(t, renderedRepoGraphHitCount(missing.Content))

	dwell := runRepoGraphTool(t, ctx, toolSet[2], RepoDwellParams{MaxTokens: 256})
	require.False(t, dwell.IsError, dwell.Content)
	var metadata RepoGraphResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(dwell.Metadata), &metadata))
	require.Equal(t, "dwell", metadata.Operation)
	require.LessOrEqual(t, metadata.UsedTokens, metadata.MaxTokens)

	impact := runRepoGraphTool(t, context.Background(), toolSet[3], RepoImpactParams{
		Files: []string{"service.go"}, MaxTokens: 256,
	})
	require.False(t, impact.IsError, impact.Content)
	require.Contains(t, impact.Content, "repo_graph impact")
}

func TestRepoGraphToolsValidateOrderedInputs(t *testing.T) {
	t.Parallel()
	manager, err := repograph.NewManager(t.TempDir(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })

	input, err := json.Marshal(RepoFocusParams{Query: "x"})
	require.NoError(t, err)
	_, err = NewRepoFocusTool(manager).Run(context.Background(), fantasy.ToolCall{
		ID: "missing-session", Name: RepoFocusToolName, Input: string(input),
	})
	require.Error(t, err)

	ctx := context.WithValue(context.Background(), SessionIDContextKey, "session")
	rootScope, err := repoGraphScopePath(".")
	require.NoError(t, err)
	require.Empty(t, rootScope)
	invalidPath := runRepoGraphTool(t, ctx, NewRepoFocusTool(manager), RepoFocusParams{Query: "x", Path: "../escape"})
	require.True(t, invalidPath.IsError)
	invalidNULPath := runRepoGraphTool(t, ctx, NewRepoFocusTool(manager), RepoFocusParams{Query: "x", Path: "bad\x00path"})
	require.True(t, invalidNULPath.IsError)
	invalidBudget := runRepoGraphTool(t, ctx, NewRepoFocusTool(manager), RepoFocusParams{Query: "x", MaxTokens: 1})
	require.True(t, invalidBudget.IsError)
	oversizedQuery := runRepoGraphTool(t, ctx, NewRepoFocusTool(manager), RepoFocusParams{Query: strings.Repeat("x", maxRepoGraphQueryBytes+1)})
	require.True(t, oversizedQuery.IsError)
	impactTool := NewRepoImpactTool(manager)
	emptyImpact := runRepoGraphTool(t, ctx, impactTool, RepoImpactParams{})
	require.True(t, emptyImpact.IsError)
	emptySymbol := runRepoGraphTool(t, ctx, impactTool, RepoImpactParams{Symbols: []string{" "}})
	require.True(t, emptySymbol.IsError)
	oversizedSymbol := runRepoGraphTool(t, ctx, impactTool, RepoImpactParams{
		Symbols: []string{strings.Repeat("x", maxRepoGraphInputBytes+1)},
	})
	require.True(t, oversizedSymbol.IsError)
	tooManySymbols := runRepoGraphTool(t, ctx, impactTool, RepoImpactParams{Symbols: make([]string, 257)})
	require.True(t, tooManySymbols.IsError)
	oversizedBase := runRepoGraphTool(t, ctx, impactTool, RepoImpactParams{
		Base: strings.Repeat("x", maxRepoGraphInputBytes+1),
	})
	require.True(t, oversizedBase.IsError)
	missingFocus := runRepoGraphTool(t, ctx, NewRepoDwellTool(manager), RepoDwellParams{})
	require.True(t, missingFocus.IsError)
	emptySketch := runRepoGraphTool(t, ctx, NewRepoSketchTool(manager), RepoSketchParams{})
	require.True(t, emptySketch.IsError)

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	input, err = json.Marshal(RepoFocusParams{Query: "x", MaxTokens: 256})
	require.NoError(t, err)
	_, err = NewRepoFocusTool(manager).Run(canceled, fantasy.ToolCall{
		ID: "canceled", Name: RepoFocusToolName, Input: string(input),
	})
	require.ErrorIs(t, err, context.Canceled)

	focusTool := NewRepoFocusTool(manager)
	malformed, err := focusTool.Run(ctx, fantasy.ToolCall{
		ID: "malformed", Name: RepoFocusToolName, Input: `{`,
	})
	require.NoError(t, err)
	require.True(t, malformed.IsError)
	unknown, err := focusTool.Run(ctx, fantasy.ToolCall{
		ID: "unknown", Name: RepoFocusToolName,
		Input: `{"query":"x","unexpected":true}`,
	})
	require.NoError(t, err)
	require.True(t, unknown.IsError)
	require.Contains(t, unknown.Content, "unexpected")
	nullInput, err := focusTool.Run(ctx, fantasy.ToolCall{
		ID: "null", Name: RepoFocusToolName, Input: `null`,
	})
	require.NoError(t, err)
	require.True(t, nullInput.IsError)
}

func renderedRepoGraphHitCount(content string) int {
	count := 0
	for _, line := range strings.Split(content, "\n") {
		if _, err := fmt.Sscanf(line, "%d.", new(int)); err == nil {
			count++
		}
	}
	return count
}

func runRepoGraphTool(t *testing.T, ctx context.Context, tool fantasy.AgentTool, params any) fantasy.ToolResponse {
	t.Helper()
	input, err := json.Marshal(params)
	require.NoError(t, err)
	response, err := tool.Run(ctx, fantasy.ToolCall{ID: "repo-call", Name: tool.Info().Name, Input: string(input)})
	require.NoError(t, err)
	return response
}
