package chat

import (
	"encoding/json"
	"testing"

	"github.com/asx8678/ultra/internal/agent/tools"
	"github.com/asx8678/ultra/internal/fabric"
	"github.com/asx8678/ultra/internal/message"
	"github.com/asx8678/ultra/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestFabricToolPendingShowsRuntimeAndRequestedLimits(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	item := NewFabricToolMessageItem(&sty, message.ToolCall{
		ID:   "fabric-1",
		Name: tools.FabricExecToolName,
		Input: `{
			"code":"return await host.view({file_path:'README.md'})",
			"timeout_ms":30000,
			"memory_limit_bytes":33554432,
			"agent_budget":2,
			"capability_view_id":"view:7",
			"display":{"title":"Inspect README"}
		}`,
	}, nil, false)

	plain := ansi.Strip(item.Render(160))
	require.Contains(t, plain, "Fabric Inspect README")
	require.Contains(t, plain, "TypeScript → esbuild → isolated Goja → registry")
	require.Contains(t, plain, "View: view:7")
	require.Contains(t, plain, "timeout=30s · memory=32 MiB · calls=128 · agents=2")
	require.Contains(t, plain, "Mesh: not active · capability registry only")
	require.Contains(t, plain, "Status: starting runtime")
}

func TestFabricToolPendingShowsLivePhaseAndNestedCalls(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	item := NewFabricToolMessageItem(&sty, message.ToolCall{
		ID: "fabric-live", Name: tools.FabricExecToolName,
		Input: `{"code":"return await host.view({file_path:'README.md'})"}`,
	}, nil, false)
	item.AddActivity(fabric.ExecutionActivity{
		Kind: fabric.ActivityPhase, Phase: "compile", CapabilityViewID: "view:9",
		CapabilityCount: 12, Providers: []string{"host", "mcp"},
	})
	item.AddActivity(fabric.ExecutionActivity{
		Kind: fabric.ActivityCallStarted, Sequence: 1, Ref: "host.view",
	})

	plain := ansi.Strip(item.Render(160))
	require.Contains(t, plain, "View: view:9")
	require.Contains(t, plain, "Capabilities: 12 actions · providers=host,mcp")
	require.Contains(t, plain, "Phase: compile")
	require.Contains(t, plain, "Live calls: 1 observed · 1 active · 0 completed")
	require.Contains(t, plain, "host.view running")

	item.AddActivity(fabric.ExecutionActivity{
		Kind: fabric.ActivityCallCompleted, Sequence: 1, Ref: "host.view",
		Outcome: fabric.OutcomeSucceeded,
	})
	plain = ansi.Strip(item.Render(160))
	require.Contains(t, plain, "Live calls: 1 observed · 0 active · 1 completed")
	require.Contains(t, plain, "host.view succeeded")
}

func TestFabricToolCompletedShowsTraceOperationsAndResult(t *testing.T) {
	t.Parallel()

	result := fabric.FabricExecResult{
		ExecutionID: "fabric-2",
		Outcome:     fabric.OutcomeSucceeded,
		Value:       fabric.JSONObject{"files": 2},
		Logs:        []string{"checked files"},
		Trace: fabric.ExecutionTrace{
			Kind:    "ultra.fabric.execution",
			Version: 1,
			Outcome: fabric.OutcomeSucceeded,
			Phases:  []string{"compile", "execute"},
			Operations: []fabric.TraceOperation{
				{
					Type: "call", Sequence: 1, Ref: "host.glob", Provider: "host", Action: "glob",
					Outcome: fabric.OutcomeSucceeded,
				},
				{
					Type: "call", Sequence: 2, Ref: "mcp.search", Provider: "mcp", Action: "search",
					Outcome: fabric.OutcomeFailed, FailureStage: fabric.FailureAuthorize,
					Error: "network denied",
				},
			},
			Counts: fabric.TraceCounts{RedactedValues: 3, TruncatedValues: 1},
		},
	}
	encoded, err := json.Marshal(result)
	require.NoError(t, err)

	sty := styles.CharmtonePantera()
	item := NewFabricToolMessageItem(
		&sty,
		message.ToolCall{ID: "fabric-2", Name: tools.FabricExecToolName, Input: `{"code":"return 2"}`, Finished: true},
		&message.ToolResult{ToolCallID: "fabric-2", Name: tools.FabricExecToolName, Content: string(encoded)},
		false,
	)
	item.SetStatus(ToolStatusSuccess)

	plain := ansi.Strip(item.Render(160))
	require.Contains(t, plain, "Execution: fabric-2")
	require.Contains(t, plain, "Phases: compile → execute")
	require.Contains(t, plain, "Providers: host, mcp")
	require.Contains(t, plain, "Calls: 2 total · 1 succeeded · 1 failed")
	require.Contains(t, plain, "host.glob succeeded")
	require.Contains(t, plain, "mcp.search failed at authorize: network denied")
	require.Contains(t, plain, "Trace: redacted=3 · truncated=1 · dropped=0")
	require.Contains(t, plain, "Logs: 1 sandbox entries")
	require.Contains(t, plain, `Result: {"files":2}`)
}

func TestToolFactoryUsesDedicatedFabricRenderer(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	item := NewToolMessageItem(
		&sty,
		"message-1",
		message.ToolCall{ID: "fabric-3", Name: tools.FabricExecToolName, Input: `{"code":"return true"}`},
		nil,
		false,
		"",
	)
	require.IsType(t, &FabricToolMessageItem{}, item)
}
