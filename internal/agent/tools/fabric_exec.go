package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"time"

	"charm.land/fantasy"
	"github.com/asx8678/ultra/internal/fabric"
)

const FabricExecToolName = "fabric_exec"

//go:embed fabric_exec.md
var fabricExecDescription string

// FabricExecDisplay is the only nested presentation object in the otherwise
// flat model-facing schema.
type FabricExecDisplay struct {
	Title   string `json:"title,omitempty" description:"Short activity title"`
	Compact bool   `json:"compact,omitempty" description:"Prefer compact activity rendering"`
}

// FabricExecParams is the flat model-facing Fabric request.
type FabricExecParams struct {
	Code             string            `json:"code" description:"TypeScript program to check and execute"`
	Strings          map[string]string `json:"strings,omitempty" description:"Named literal strings exposed read-only to the guest"`
	TimeoutMS        int64             `json:"timeout_ms,omitempty" description:"Execution timeout in milliseconds"`
	MemoryLimitBytes int64             `json:"memory_limit_bytes,omitempty" description:"Sandbox memory ceiling in bytes"`
	TokenBudget      int64             `json:"token_budget,omitempty" description:"Nested model token budget"`
	AgentBudget      int               `json:"agent_budget,omitempty" description:"Nested agent-call budget"`
	CapabilityViewID string            `json:"capability_view_id,omitempty" description:"Committed capability view"`
	IdempotencyKey   string            `json:"idempotency_key,omitempty" description:"Caller idempotency key"`
	ResultMaxBytes   int               `json:"result_max_bytes,omitempty" description:"Maximum result bytes"`
	Display          FabricExecDisplay `json:"display,omitempty" description:"Optional display hints"`
}

// FabricExecutor is the execution-service contract consumed by the outer tool.
type FabricExecutor interface {
	Execute(context.Context, fabric.FabricExecRequest, fabric.OuterInvocationContext) fabric.FabricExecResult
}

// NewFabricExecTool creates the model-facing programmable tool. Callers must
// not register it until a certified compiler and sandbox are configured.
func NewFabricExecTool(executor FabricExecutor, hostID, cwd string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		FabricExecToolName,
		fabricExecDescription,
		func(ctx context.Context, params FabricExecParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if executor == nil {
				return fantasy.NewTextErrorResponse("Fabric runtime is unavailable"), nil
			}
			result := executor.Execute(ctx, fabric.FabricExecRequest{
				Code: params.Code, Strings: params.Strings,
				Timeout:          time.Duration(params.TimeoutMS) * time.Millisecond,
				MemoryLimitBytes: params.MemoryLimitBytes,
				TokenBudget:      params.TokenBudget, AgentBudget: params.AgentBudget,
				CapabilityViewID: params.CapabilityViewID, IdempotencyKey: params.IdempotencyKey,
				ResultMaxBytes: params.ResultMaxBytes,
				DisplayTitle:   params.Display.Title, DisplayCompact: params.Display.Compact,
			}, fabric.OuterInvocationContext{
				ExecutionID: call.ID, ParentToolCallID: call.ID,
				SessionID: GetSessionFromContext(ctx), CWD: cwd, HostID: hostID,
			})
			encoded, err := json.Marshal(result)
			if err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("marshal Fabric result: %w", err)
			}
			if result.Outcome != fabric.OutcomeSucceeded {
				return fantasy.NewTextErrorResponse(string(encoded)), nil
			}
			return fantasy.NewTextResponse(string(encoded)), nil
		},
	)
}
