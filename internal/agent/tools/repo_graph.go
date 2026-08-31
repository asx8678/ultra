package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/fantasy"

	"github.com/asx8678/ultra/internal/repograph"
)

const (
	RepoSketchToolName = "repo_sketch"
	RepoFocusToolName  = "repo_focus"
	RepoDwellToolName  = "repo_dwell"
	RepoImpactToolName = "repo_impact"

	minRepoGraphTokens     = 256
	maxRepoGraphTokens     = 16000
	maxRepoGraphQueryBytes = 1024
	maxRepoGraphInputBytes = 4096
)

//go:embed repo_sketch.md
var repoSketchDescription string

//go:embed repo_focus.md
var repoFocusDescription string

//go:embed repo_dwell.md
var repoDwellDescription string

//go:embed repo_impact.md
var repoImpactDescription string

type RepoSketchParams struct {
	MaxTokens int `json:"max_tokens,omitempty" description:"Maximum model-visible output tokens (default 1600, min 256, max 16000)"`
}

type RepoFocusParams struct {
	Query     string `json:"query" description:"Symbol, route, file, environment key, literal, or concept to focus"`
	Path      string `json:"path,omitempty" description:"Optional repository-relative path scope"`
	Language  string `json:"language,omitempty" description:"Optional language scope such as go, typescript, python, or rust"`
	Kind      string `json:"kind,omitempty" description:"Optional node kind: symbol, file, route, literal, or env"`
	Fresh     bool   `json:"fresh,omitempty" description:"Reset progressive disclosure and replay the current focus"`
	MaxTokens int    `json:"max_tokens,omitempty" description:"Maximum model-visible output tokens (default 4000, min 256, max 16000)"`
}

type RepoDwellParams struct {
	MaxTokens int `json:"max_tokens,omitempty" description:"Maximum model-visible output tokens (default 4000, min 256, max 16000)"`
}

type RepoImpactParams struct {
	Files       []string `json:"files,omitempty" description:"Repository-relative changed or proposed file paths"`
	Symbols     []string `json:"symbols,omitempty" description:"Changed or proposed symbol names"`
	Uncommitted bool     `json:"uncommitted,omitempty" description:"Seed from staged, unstaged, and untracked Git changes"`
	Base        string   `json:"base,omitempty" description:"Seed from the merge-base diff against this Git revision"`
	MaxTokens   int      `json:"max_tokens,omitempty" description:"Maximum model-visible output tokens (default 4000, min 256, max 16000)"`
}

type RepoGraphResponseMetadata struct {
	Operation      string                 `json:"operation"`
	Scope          repograph.Scope        `json:"scope,omitempty"`
	Status         string                 `json:"status"`
	Root           string                 `json:"root"`
	Generation     uint64                 `json:"generation"`
	ResultCount    int                    `json:"result_count"`
	SuggestedReads []repograph.ReadWindow `json:"suggested_reads,omitempty"`
	Depth          int                    `json:"depth,omitempty"`
	Truncated      bool                   `json:"truncated"`
	Degraded       bool                   `json:"degraded"`
	UsedTokens     int                    `json:"used_tokens"`
	MaxTokens      int                    `json:"max_tokens"`
	Warnings       []string               `json:"warnings,omitempty"`
	Coverage       repograph.Coverage     `json:"coverage"`
}

func NewRepoSketchTool(manager *repograph.Manager) fantasy.AgentTool {
	tool := fantasy.NewParallelAgentTool(
		RepoSketchToolName,
		repoSketchDescription,
		func(ctx context.Context, params RepoSketchParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			maxTokens, err := repoGraphTokens(params.MaxTokens, 1600)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}
			result, err := manager.Sketch(ctx, maxTokens)
			return repoGraphResponse(result, err)
		},
	)
	return withRepoGraphSchema(tool, repoSketchToolInfo())
}

func NewRepoFocusTool(manager *repograph.Manager) fantasy.AgentTool {
	tool := fantasy.NewAgentTool(
		RepoFocusToolName,
		repoFocusDescription,
		func(ctx context.Context, params RepoFocusParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			sessionID := GetSessionFromContext(ctx)
			if sessionID == "" {
				return fantasy.ToolResponse{}, fmt.Errorf("session id missing from context")
			}
			query := strings.TrimSpace(params.Query)
			if query == "" {
				return fantasy.NewTextErrorResponse("query is required"), nil
			}
			if len(query) > maxRepoGraphQueryBytes {
				return fantasy.NewTextErrorResponse("query is limited to 1024 bytes"), nil
			}
			path, err := repoGraphScopePath(params.Path)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}
			kind := strings.ToLower(strings.TrimSpace(params.Kind))
			if kind != "" && !slices.Contains([]string{"symbol", "file", "route", "literal", "env"}, kind) {
				return fantasy.NewTextErrorResponse("kind must be symbol, file, route, literal, or env"), nil
			}
			language := strings.ToLower(strings.TrimSpace(params.Language))
			if len(language) > 64 {
				return fantasy.NewTextErrorResponse("language is limited to 64 bytes"), nil
			}
			maxTokens, err := repoGraphTokens(params.MaxTokens, 4000)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}
			result, err := manager.Focus(ctx, repograph.FocusOptions{
				SessionID: sessionID,
				Query:     query,
				Scope: repograph.Scope{
					Path:     path,
					Language: language,
					Kind:     kind,
				},
				Fresh:     params.Fresh,
				MaxTokens: maxTokens,
			})
			return repoGraphResponse(result, err)
		},
	)
	return withRepoGraphSchema(tool, repoFocusToolInfo())
}

func NewRepoDwellTool(manager *repograph.Manager) fantasy.AgentTool {
	tool := fantasy.NewAgentTool(
		RepoDwellToolName,
		repoDwellDescription,
		func(ctx context.Context, params RepoDwellParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			sessionID := GetSessionFromContext(ctx)
			if sessionID == "" {
				return fantasy.ToolResponse{}, fmt.Errorf("session id missing from context")
			}
			maxTokens, err := repoGraphTokens(params.MaxTokens, 4000)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}
			result, err := manager.Dwell(ctx, sessionID, maxTokens)
			return repoGraphResponse(result, err)
		},
	)
	return withRepoGraphSchema(tool, repoDwellToolInfo())
}

func NewRepoImpactTool(manager *repograph.Manager) fantasy.AgentTool {
	tool := fantasy.NewParallelAgentTool(
		RepoImpactToolName,
		repoImpactDescription,
		func(ctx context.Context, params RepoImpactParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if len(params.Files) > 256 || len(params.Symbols) > 256 {
				return fantasy.NewTextErrorResponse("files and symbols are limited to 256 entries each"), nil
			}
			base := strings.TrimSpace(params.Base)
			if len(base) > maxRepoGraphInputBytes {
				return fantasy.NewTextErrorResponse("base is limited to 4096 bytes"), nil
			}
			if len(params.Files) == 0 && len(params.Symbols) == 0 && base == "" {
				// An empty impact request means "review my current work", matching
				// the most useful interactive default.
				params.Uncommitted = true
			}
			files := make([]string, 0, len(params.Files))
			for _, candidate := range params.Files {
				path, err := repoGraphScopePath(candidate)
				if err != nil || path == "" {
					return fantasy.NewTextErrorResponse("files must contain non-empty repository-relative paths"), nil
				}
				files = append(files, path)
			}
			symbols := make([]string, 0, len(params.Symbols))
			for _, candidate := range params.Symbols {
				symbol := strings.TrimSpace(candidate)
				if symbol == "" {
					return fantasy.NewTextErrorResponse("symbols must contain non-empty names"), nil
				}
				if len(symbol) > maxRepoGraphInputBytes {
					return fantasy.NewTextErrorResponse("symbols are limited to 4096 bytes each"), nil
				}
				symbols = append(symbols, symbol)
			}
			maxTokens, err := repoGraphTokens(params.MaxTokens, 4000)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}
			result, err := manager.Impact(ctx, repograph.ImpactOptions{
				Files:       files,
				Symbols:     symbols,
				Uncommitted: params.Uncommitted,
				Base:        base,
				MaxTokens:   maxTokens,
			})
			return repoGraphResponse(result, err)
		},
	)
	return withRepoGraphSchema(tool, repoImpactToolInfo())
}

func repoGraphResponse(result repograph.Result, err error) (fantasy.ToolResponse, error) {
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return fantasy.ToolResponse{}, err
		}
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	metadata := RepoGraphResponseMetadata{
		Operation:      result.Meta.Operation,
		Scope:          result.Meta.Scope,
		Status:         result.Meta.Status,
		Root:           result.Meta.Root,
		Generation:     result.Meta.Generation,
		ResultCount:    len(result.Hits),
		SuggestedReads: result.SuggestedReads,
		Depth:          result.Meta.Depth,
		Truncated:      result.Meta.Truncated,
		Degraded:       result.Meta.Degraded,
		UsedTokens:     result.Meta.UsedTokens,
		MaxTokens:      result.Meta.MaxTokens,
		Warnings:       result.Meta.Warnings,
		Coverage:       result.Meta.Coverage,
	}
	return fantasy.WithResponseMetadata(fantasy.NewTextResponse(result.Text), metadata), nil
}

type repoGraphSchemaTool struct {
	fantasy.AgentTool
	info fantasy.ToolInfo
}

func (tool *repoGraphSchemaTool) Info() fantasy.ToolInfo {
	return tool.info
}

func (tool *repoGraphSchemaTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	input := strings.TrimSpace(call.Input)
	if input == "" {
		input = "{}"
	}
	var parameters map[string]json.RawMessage
	if err := json.Unmarshal([]byte(input), &parameters); err != nil {
		return fantasy.NewTextErrorResponse("input must be a JSON object: " + err.Error()), nil
	}
	if parameters == nil {
		return fantasy.NewTextErrorResponse("input must be a JSON object"), nil
	}
	unknown := make([]string, 0)
	for name := range parameters {
		if _, ok := tool.info.Parameters[name]; !ok {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		slices.Sort(unknown)
		return fantasy.NewTextErrorResponse("unknown parameters: " + strings.Join(unknown, ", ")), nil
	}
	call.Input = input
	return tool.AgentTool.Run(ctx, call)
}

func withRepoGraphSchema(tool fantasy.AgentTool, info fantasy.ToolInfo) fantasy.AgentTool {
	return &repoGraphSchemaTool{AgentTool: tool, info: info}
}

func repoSketchToolInfo() fantasy.ToolInfo {
	return fantasy.ToolInfo{
		Name: RepoSketchToolName, Description: repoSketchDescription,
		Parameters: map[string]any{"max_tokens": repoGraphTokenSchema(1600)},
	}
}

func repoFocusToolInfo() fantasy.ToolInfo {
	return fantasy.ToolInfo{
		Name: RepoFocusToolName, Description: repoFocusDescription,
		Parameters: map[string]any{
			"query": map[string]any{
				"type": "string", "minLength": 1, "maxLength": maxRepoGraphQueryBytes,
			},
			"path": map[string]any{
				"type": "string", "maxLength": maxRepoGraphInputBytes,
				"description": "Optional repository-relative path scope",
			},
			"language": map[string]any{"type": "string", "maxLength": 64},
			"kind": map[string]any{
				"type": "string", "enum": []string{"symbol", "file", "route", "literal", "env"},
			},
			"fresh":      map[string]any{"type": "boolean", "default": false},
			"max_tokens": repoGraphTokenSchema(4000),
		},
		Required: []string{"query"},
	}
}

func repoDwellToolInfo() fantasy.ToolInfo {
	return fantasy.ToolInfo{
		Name: RepoDwellToolName, Description: repoDwellDescription,
		Parameters: map[string]any{"max_tokens": repoGraphTokenSchema(4000)},
	}
}

func repoImpactToolInfo() fantasy.ToolInfo {
	stringArray := func(description string) map[string]any {
		return map[string]any{
			"type": "array", "maxItems": 256,
			"items": map[string]any{
				"type": "string", "minLength": 1, "maxLength": maxRepoGraphInputBytes,
			},
			"description": description,
		}
	}
	return fantasy.ToolInfo{
		Name: RepoImpactToolName, Description: repoImpactDescription,
		Parameters: map[string]any{
			"files":       stringArray("Repository-relative changed or proposed file paths"),
			"symbols":     stringArray("Changed or proposed symbol names"),
			"uncommitted": map[string]any{"type": "boolean", "default": false},
			"base": map[string]any{
				"type": "string", "maxLength": maxRepoGraphInputBytes,
				"description": "Git revision used as a diff seed",
			},
			"max_tokens": repoGraphTokenSchema(4000),
		},
	}
}

func repoGraphTokenSchema(defaultValue int) map[string]any {
	return map[string]any{
		"type": "integer", "minimum": minRepoGraphTokens,
		"maximum": maxRepoGraphTokens, "default": defaultValue,
	}
}

func repoGraphTokens(value, fallback int) (int, error) {
	if value == 0 {
		return fallback, nil
	}
	if value < minRepoGraphTokens || value > maxRepoGraphTokens {
		return 0, fmt.Errorf("max_tokens must be between %d and %d", minRepoGraphTokens, maxRepoGraphTokens)
	}
	return value, nil
}

func repoGraphScopePath(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if len(value) > maxRepoGraphInputBytes {
		return "", fmt.Errorf("path is limited to 4096 bytes")
	}
	if strings.ContainsRune(value, '\x00') || filepath.IsAbs(value) || filepath.VolumeName(value) != "" {
		return "", fmt.Errorf("path must be repository-relative")
	}
	cleaned := filepath.ToSlash(filepath.Clean(value))
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("path must stay within the repository")
	}
	if cleaned == "." {
		return "", nil
	}
	return strings.TrimPrefix(cleaned, "./"), nil
}
