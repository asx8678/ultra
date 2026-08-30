package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"charm.land/fantasy"
	"github.com/asx8678/ultra/internal/fabric"
	"github.com/asx8678/ultra/internal/permission"
	"github.com/asx8678/ultra/internal/toolmeta"
)

const ultraFabricProviderName = "host"

// UltraFabricProvider exposes a fixed Ultra tool catalog through Fabric. The
// input should already contain Ultra's hook and policy wrappers.
type UltraFabricProvider struct {
	tools       map[string]fantasy.AgentTool
	descriptors map[string]fabric.ActionDescriptor
}

// NewUltraFabricProvider snapshots executable tools and their schemas.
func NewUltraFabricProvider(agentTools []fantasy.AgentTool) (*UltraFabricProvider, error) {
	provider := &UltraFabricProvider{
		tools:       make(map[string]fantasy.AgentTool, len(agentTools)),
		descriptors: make(map[string]fabric.ActionDescriptor, len(agentTools)),
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
	response, err := tool.Run(ctx, fantasy.ToolCall{
		ID: invocation.NestedToolCallID, Name: action, Input: string(input),
	})
	if err != nil {
		return nil, err
	}
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
	_ context.Context,
	request fabric.AuthorizationRequest,
) (fabric.AuthorizationDecision, error) {
	if a.Permissions == nil {
		return fabric.AuthorizationDecision{Reason: "Ultra permission service is unavailable"}, nil
	}
	action := strings.TrimPrefix(request.Ref, ultraFabricProviderName+".")
	if _, known := toolmeta.Lookup(action); !known {
		return fabric.AuthorizationDecision{Reason: "tool has no authoritative Ultra metadata"}, nil
	}
	mode, scoped := permission.SessionMode(a.Permissions, request.Context.SessionID)
	if !scoped || mode == permission.ModeAsk || mode == permission.ModeYolo {
		return fabric.AuthorizationDecision{Allowed: true}, nil
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
