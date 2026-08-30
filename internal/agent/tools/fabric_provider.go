package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"charm.land/fantasy"
	"github.com/asx8678/ultra/internal/fabric"
	"github.com/asx8678/ultra/internal/hooks"
	"github.com/asx8678/ultra/internal/permission"
	"github.com/asx8678/ultra/internal/toolmeta"
	"github.com/tidwall/sjson"
)

const ultraFabricProviderName = "host"

// UltraFabricProvider exposes a fixed Ultra tool catalog through Fabric. The
// input should already contain Ultra's hook and policy wrappers.
type UltraFabricProvider struct {
	tools       map[string]fantasy.AgentTool
	descriptors map[string]fabric.ActionDescriptor
	hookRunner  *hooks.Runner
}

const (
	ultraFabricHookStateKey       = "ultra.fabric.hook"
	ultraFabricPermissionStateKey = "ultra.fabric.permission"
)

// NewUltraFabricProvider snapshots executable tools and their schemas.
func NewUltraFabricProvider(agentTools []fantasy.AgentTool) (*UltraFabricProvider, error) {
	return newUltraFabricProvider(agentTools, nil)
}

// NewUltraFabricProviderWithHooks also runs hooks during argument preparation.
func NewUltraFabricProviderWithHooks(
	agentTools []fantasy.AgentTool,
	hookRunner *hooks.Runner,
) (*UltraFabricProvider, error) {
	return newUltraFabricProvider(agentTools, hookRunner)
}

func newUltraFabricProvider(
	agentTools []fantasy.AgentTool,
	hookRunner *hooks.Runner,
) (*UltraFabricProvider, error) {
	provider := &UltraFabricProvider{
		tools:       make(map[string]fantasy.AgentTool, len(agentTools)),
		descriptors: make(map[string]fabric.ActionDescriptor, len(agentTools)),
		hookRunner:  hookRunner,
	}
	for _, tool := range agentTools {
		if tool == nil {
			return nil, errors.New("nil Ultra tool")
		}
		info := tool.Info()
		if info.Name == "" || info.Name == FabricExecToolName {
			continue
		}
		if _, duplicate := provider.tools[info.Name]; duplicate {
			return nil, fmt.Errorf("duplicate Ultra tool %q", info.Name)
		}
		schema, err := ultraToolSchema(info)
		if err != nil {
			return nil, err
		}
		provider.tools[info.Name] = tool
		provider.descriptors[info.Name] = ultraToolDescriptor(info, schema)
	}
	return provider, nil
}

func (p *UltraFabricProvider) Name() string { return ultraFabricProviderName }

func (p *UltraFabricProvider) Description() string {
	return "Ultra native tools captured behind the Fabric registry and policy pipeline."
}

func (p *UltraFabricProvider) List(
	_ context.Context,
	request fabric.ListActionsRequest,
	_ fabric.DiscoveryContext,
) ([]fabric.ActionDescriptor, error) {
	query := strings.ToLower(strings.TrimSpace(request.Query))
	names := make([]string, 0, len(p.descriptors))
	for name, descriptor := range p.descriptors {
		if query == "" || strings.Contains(strings.ToLower(name+" "+descriptor.Description), query) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	limit := request.Limit
	if limit <= 0 || limit > len(names) {
		limit = len(names)
	}
	result := make([]fabric.ActionDescriptor, 0, limit)
	for _, name := range names[:limit] {
		result = append(result, p.descriptors[name])
	}
	return result, nil
}

func (p *UltraFabricProvider) Describe(
	_ context.Context,
	action string,
	_ fabric.DiscoveryContext,
) (fabric.ActionDescriptor, bool, error) {
	descriptor, ok := p.descriptors[action]
	return descriptor, ok, nil
}

// PrepareArguments runs Ultra's hook preflight before Fabric validates and
// approves the authoritative nested arguments.
func (p *UltraFabricProvider) PrepareArguments(
	ctx context.Context,
	action string,
	args fabric.JSONObject,
	invocation fabric.InvocationContext,
) (fabric.JSONObject, error) {
	if p.hookRunner == nil {
		return args, nil
	}
	input, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("marshal Ultra Fabric hook input: %w", err)
	}
	result, hookErr := p.hookRunner.Run(
		ctx, hooks.EventPreToolUse, invocation.SessionID, action, string(input),
	)
	if hookErr != nil {
		slog.Warn("Fabric hook execution error, proceeding with nested tool call", "tool", action, "error", hookErr)
	}
	if result.Decision == hooks.DecisionDeny || result.Halt {
		return nil, fmt.Errorf("nested tool call blocked by hook: %s", result.Reason)
	}
	prepared := args
	if result.UpdatedInput != "" {
		if err := json.Unmarshal([]byte(result.UpdatedInput), &prepared); err != nil {
			return nil, fmt.Errorf("decode Fabric hook-updated input for %q: %w", action, err)
		}
	}
	invocation.SetState(ultraFabricHookStateKey, result)
	return prepared, nil
}

func (p *UltraFabricProvider) Invoke(
	ctx context.Context,
	action string,
	args fabric.JSONObject,
	invocation fabric.InvocationContext,
) (fabric.JSONValue, error) {
	tool := p.tools[action]
	if tool == nil {
		return nil, fmt.Errorf("ultra Fabric tool %q is unavailable", action)
	}
	input, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("marshal Ultra Fabric tool %q arguments: %w", action, err)
	}
	ctx = context.WithValue(ctx, SessionIDContextKey, invocation.SessionID)
	ctx = context.WithValue(ctx, MessageIDContextKey, invocation.ExecutionID)
	if p.hookRunner != nil {
		ctx = WithFabricHookPrepared(ctx)
	}
	if approved, ok := invocation.State(ultraFabricPermissionStateKey); ok && approved == true {
		ctx = permission.WithHookApproval(ctx, invocation.NestedToolCallID)
	}
	var hookResult hooks.AggregateResult
	if state, ok := invocation.State(ultraFabricHookStateKey); ok {
		hookResult, _ = state.(hooks.AggregateResult)
		if hookResult.Decision == hooks.DecisionAllow {
			ctx = permission.WithHookApproval(ctx, invocation.NestedToolCallID)
		}
	}
	response, err := tool.Run(ctx, fantasy.ToolCall{
		ID: invocation.NestedToolCallID, Name: action, Input: string(input),
	})
	if err != nil {
		return nil, err
	}
	if hookResult.Context != "" {
		if response.Content != "" {
			response.Content += "\n"
		}
		response.Content += hookResult.Context
	}
	response.Metadata = mergeFabricHookMetadata(response.Metadata, hookResult)
	result := fabric.JSONObject{
		"type": response.Type, "content": response.Content,
		"is_error": response.IsError, "stop_turn": response.StopTurn,
	}
	if response.Metadata != "" {
		result["metadata"] = response.Metadata
	}
	if response.MediaType != "" {
		result["media_type"] = response.MediaType
	}
	if len(response.Data) > 0 {
		result["data_base64"] = base64.StdEncoding.EncodeToString(response.Data)
	}
	return result, nil
}

// UltraFabricAuthorizer enforces Ultra's session-scoped permission profile
// before a nested native tool executes.
type UltraFabricAuthorizer struct{ Permissions permission.Service }

func (a UltraFabricAuthorizer) Authorize(
	ctx context.Context,
	request fabric.AuthorizationRequest,
) (fabric.AuthorizationDecision, error) {
	if a.Permissions == nil {
		return fabric.AuthorizationDecision{Reason: "Ultra permission service is unavailable"}, nil
	}
	action := strings.TrimPrefix(request.Ref, ultraFabricProviderName+".")
	_, known := toolmeta.Lookup(action)
	mode, scoped := permission.SessionMode(a.Permissions, request.Context.SessionID)

	// Built-ins keep their native operation-specific permission flow for
	// interactive sessions. Dynamic tools (notably MCP tools) lack static
	// metadata, so Fabric must perform a per-call permission request.
	if known && (!scoped || mode == permission.ModeAsk || mode == permission.ModeYolo) {
		return fabric.AuthorizationDecision{Allowed: true}, nil
	}
	if !known {
		allowed, err := a.Permissions.Request(ctx, permission.CreatePermissionRequest{
			SessionID: request.Context.SessionID, ToolCallID: request.Context.NestedToolCallID,
			ToolName: action, Description: request.Descriptor.Description,
			Action: request.Ref, Params: request.Args, Path: request.Context.CWD,
		})
		if err != nil {
			return fabric.AuthorizationDecision{Reason: err.Error()}, nil
		}
		if allowed {
			request.Context.SetState(ultraFabricPermissionStateKey, true)
			return fabric.AuthorizationDecision{Allowed: true}, nil
		}
		return fabric.AuthorizationDecision{Reason: "dynamic tool permission denied"}, nil
	}
	if permission.ToolAllowed(mode, action) {
		return fabric.AuthorizationDecision{Allowed: true}, nil
	}
	return fabric.AuthorizationDecision{
		Reason: fmt.Sprintf("tool %q is blocked by %s permission mode", action, mode),
	}, nil
}

// UltraFabricApprovals delegates interactive prompting to native tools. The
// registry authorizer has already enforced non-interactive policies.
type UltraFabricApprovals struct{}

func (UltraFabricApprovals) Decide(
	context.Context,
	fabric.ApprovalRequest,
) (fabric.ApprovalDecision, error) {
	return fabric.ApprovalDecision{Kind: fabric.ApprovalAllow, Scope: fabric.ApprovalOnce}, nil
}

func mergeFabricHookMetadata(existing string, result hooks.AggregateResult) string {
	if result.HookCount == 0 {
		return existing
	}
	metadata := hooks.HookMetadata{
		HookCount: result.HookCount, Decision: result.Decision.String(), Halt: result.Halt,
		Reason: result.Reason, InputRewrite: result.UpdatedInput != "", Hooks: result.Hooks,
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return existing
	}
	if existing == "" {
		existing = "{}"
	}
	merged, err := sjson.SetRaw(existing, "hook", string(encoded))
	if err != nil {
		return existing
	}
	return merged
}

func ultraToolSchema(info fantasy.ToolInfo) (json.RawMessage, error) {
	schema := map[string]any{
		"type": "object", "properties": info.Parameters, "additionalProperties": false,
	}
	if len(info.Required) > 0 {
		schema["required"] = info.Required
	}
	encoded, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshal Ultra tool %q schema: %w", info.Name, err)
	}
	return encoded, nil
}

func ultraToolDescriptor(info fantasy.ToolInfo, schema json.RawMessage) fabric.ActionDescriptor {
	metadata, known := toolmeta.Lookup(info.Name)
	if !known {
		return fabric.ActionDescriptor{
			Name: info.Name, Description: info.Description, InputSchema: schema,
			Risk: fabric.RiskExecute, Effect: &fabric.EffectDescriptor{Kind: fabric.EffectUnknown},
		}
	}
	return fabric.ActionDescriptor{
		Name: info.Name, Description: info.Description, InputSchema: schema,
		Risk: fabricRisk(metadata), Effect: fabricEffect(metadata),
		Annotations: &fabric.ActionAnnotations{ReadOnly: metadata.Effects == toolmeta.EffectRead},
	}
}

func fabricRisk(metadata toolmeta.Descriptor) fabric.RiskClass {
	switch {
	case metadata.Group == "agent":
		return fabric.RiskAgent
	case metadata.Effects&toolmeta.EffectExec != 0:
		return fabric.RiskExecute
	case metadata.Effects&toolmeta.EffectNetwork != 0:
		return fabric.RiskNetwork
	case metadata.Effects&toolmeta.EffectWrite != 0:
		return fabric.RiskWrite
	default:
		return fabric.RiskRead
	}
}

func fabricEffect(metadata toolmeta.Descriptor) *fabric.EffectDescriptor {
	if metadata.Effects == toolmeta.EffectRead {
		return &fabric.EffectDescriptor{Kind: fabric.EffectNone, Commutative: true}
	}
	if metadata.Effects == toolmeta.EffectWrite {
		return &fabric.EffectDescriptor{Kind: fabric.EffectTransactional, Resources: []string{"workspace"}}
	}
	return &fabric.EffectDescriptor{Kind: fabric.EffectUnknown}
}
