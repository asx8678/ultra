package chat

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/asx8678/ultra/internal/agent/tools"
	"github.com/asx8678/ultra/internal/fabric"
	"github.com/asx8678/ultra/internal/message"
	"github.com/asx8678/ultra/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
)

const (
	fabricDefaultTimeout     = 2 * time.Minute
	fabricDefaultMemoryBytes = int64(64 << 20)
	fabricDefaultAgentBudget = 4
	fabricVisibleCalls       = 8
)

// FabricToolMessageItem renders the programmable Fabric execution tool.
type FabricToolMessageItem struct {
	*baseToolMessageItem

	phase           string
	capabilityView  string
	capabilityCount int
	inputSchemas    int
	outputSchemas   int
	riskCounts      map[fabric.RiskClass]int
	providers       []string
	activities      []fabric.ExecutionActivity
}

var _ ToolMessageItem = (*FabricToolMessageItem)(nil)

// NewFabricToolMessageItem creates a Fabric execution message item.
func NewFabricToolMessageItem(
	sty *styles.Styles,
	toolCall message.ToolCall,
	result *message.ToolResult,
	canceled bool,
) *FabricToolMessageItem {
	item := &FabricToolMessageItem{}
	item.baseToolMessageItem = newBaseToolMessageItem(
		sty,
		toolCall,
		result,
		&FabricToolRenderContext{item: item},
		canceled,
	)
	return item
}

// AddActivity applies a best-effort live Fabric update. Terminal result traces
// remain authoritative if an update is dropped.
func (f *FabricToolMessageItem) AddActivity(activity fabric.ExecutionActivity) {
	if activity.Kind == fabric.ActivityPhase {
		f.phase = activity.Phase
		if activity.CapabilityViewID != "" {
			f.capabilityView = activity.CapabilityViewID
			f.capabilityCount = activity.CapabilityCount
			f.inputSchemas = activity.InputSchemas
			f.outputSchemas = activity.OutputSchemas
			f.riskCounts = cloneFabricRiskCounts(activity.RiskCounts)
			f.providers = slices.Clone(activity.Providers)
		}
		f.clearCache()
		f.Bump()
		return
	}
	for i := range f.activities {
		if f.activities[i].Sequence == activity.Sequence && activity.Sequence != 0 {
			f.activities[i] = activity
			f.clearCache()
			f.Bump()
			return
		}
	}
	const maxLiveActivities = 64
	f.activities = append(f.activities, activity)
	if len(f.activities) > maxLiveActivities {
		f.activities = append([]fabric.ExecutionActivity(nil), f.activities[len(f.activities)-maxLiveActivities:]...)
	}
	f.clearCache()
	f.Bump()
}

// FabricToolRenderContext renders Fabric runtime state and its nested trace.
type FabricToolRenderContext struct {
	item *FabricToolMessageItem
}

// RenderTool implements the ToolRenderer interface.
func (r *FabricToolRenderContext) RenderTool(
	sty *styles.Styles,
	width int,
	opts *ToolRenderOpts,
) string {
	cappedWidth := cappedMessageWidth(width)
	params, paramsErr := decodeFabricParams(opts.ToolCall.Input)
	activity := cmp.Or(params.Display.Title, fabricProgramSummary(params.Code))

	badge := sty.Tool.FabricBadge.Render("CODE MODE · FABRIC")
	header := toolHeader(sty, opts.Status, "Fabric", cappedWidth, opts, activity)
	if opts.Compact {
		return badge + " " + header
	}
	header = badge + "\n" + header

	lines := []string{fabricSectionLine(sty, "RUNTIME")}
	lines = append(lines, fabricRuntimeLines(sty, params, r.item)...)
	if paramsErr != nil {
		lines = append(lines, fabricDetailLine(sty, "Input", "invalid parameters"))
	}

	if opts.IsPending() {
		lines = append(lines, fabricSectionLine(sty, "LIVE EXECUTION"))
		lines = append(lines, r.liveActivityLines(sty)...)
		if r.item == nil || r.item.phase == "" {
			lines = append(lines, fabricDetailLine(sty, "Status", "starting runtime"))
		}
		body := renderFabricPanel(sty, lines, cappedWidth)
		if opts.Anim != nil {
			body += "\n" + opts.Anim.Render()
		}
		return joinToolParts(header, body)
	}

	if opts.IsCanceled() && !opts.HasResult() {
		lines = append(lines, fabricDetailLine(sty, "Outcome", "cancelled"))
		return joinToolParts(header, renderFabricPanel(sty, lines, cappedWidth))
	}
	if !opts.HasResult() || opts.Result.Content == "" {
		return joinToolParts(header, renderFabricPanel(sty, lines, cappedWidth))
	}

	var result fabric.FabricExecResult
	if err := json.Unmarshal([]byte(opts.Result.Content), &result); err != nil {
		lines = append(lines, fabricDetailLine(sty, "Result", "unrecognized Fabric response"))
		return joinToolParts(header, renderFabricPanel(sty, lines, cappedWidth))
	}

	lines = append(lines, fabricSectionLine(sty, "EXECUTION REPORT"))
	lines = append(lines, fabricResultLines(sty, result, len(opts.Result.Content), opts.ExpandedContent)...)
	body := renderFabricPanel(sty, lines, cappedWidth)
	if value := fabricResultValue(result); value != "" {
		body += "\n" + sty.Tool.Body.Render(toolOutputPlainContent(
			sty,
			"Result: "+value,
			max(1, cappedWidth-toolBodyLeftPaddingTotal),
			opts.ExpandedContent,
		))
	}
	return joinToolParts(header, body)
}

func (r *FabricToolRenderContext) liveActivityLines(sty *styles.Styles) []string {
	if r.item == nil {
		return nil
	}
	lines := make([]string, 0, len(r.item.activities)+2)
	if r.item.phase != "" {
		lines = append(lines, fabricDetailLine(sty, "Pipeline", fabricPhaseTimeline(r.item.phase)))
	}
	if len(r.item.activities) > 0 {
		started := 0
		completed := 0
		for _, activity := range r.item.activities {
			switch activity.Kind {
			case fabric.ActivityCallStarted:
				started++
			case fabric.ActivityCallCompleted:
				completed++
			}
		}
		lines = append(lines, fabricDetailLine(sty, "Live calls", fmt.Sprintf(
			"%d observed · %d active · %d completed",
			started+completed, started, completed,
		)))
	}
	start := max(0, len(r.item.activities)-fabricVisibleCalls)
	for _, activity := range r.item.activities[start:] {
		lines = append(lines, fabricActivityLine(sty, activity))
	}
	return lines
}

func decodeFabricParams(input string) (tools.FabricExecParams, error) {
	var params tools.FabricExecParams
	err := json.Unmarshal([]byte(input), &params)
	return params, err
}

func fabricProgramSummary(code string) string {
	for line := range strings.SplitSeq(code, "\n") {
		if line = strings.Join(strings.Fields(line), " "); line != "" {
			return line
		}
	}
	return "TypeScript program"
}

func fabricRuntimeLines(
	sty *styles.Styles,
	params tools.FabricExecParams,
	item *FabricToolMessageItem,
) []string {
	timeout := time.Duration(params.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = fabricDefaultTimeout
	}
	memory := params.MemoryLimitBytes
	if memory <= 0 {
		memory = fabricDefaultMemoryBytes
	}
	agents := params.AgentBudget
	if agents <= 0 {
		agents = fabricDefaultAgentBudget
	}
	resultLimit := params.ResultMaxBytes
	if resultLimit <= 0 {
		resultLimit = fabric.DefaultJSONLimits().MaxBytes
	}
	view := params.CapabilityViewID
	if view == "" && item != nil {
		view = item.capabilityView
	}
	view = cmp.Or(view, "live pinned snapshot")

	lines := []string{
		fabricDetailLine(sty, "Engine", "TypeScript → esbuild → isolated Goja → registry"),
		fabricDetailLine(sty, "Program", fmt.Sprintf(
			"%s · %s source · %d named strings",
			formatFabricLineCount(fabricSourceLines(params.Code)),
			formatFabricBytes(int64(len(params.Code))),
			len(params.Strings),
		)),
		fabricDetailLine(sty, "View", view),
	}
	if item != nil && item.capabilityCount > 0 {
		capabilities := fmt.Sprintf("%d actions", item.capabilityCount)
		if len(item.providers) > 0 {
			capabilities += " · providers=" + strings.Join(item.providers, ",")
		}
		lines = append(lines, fabricDetailLine(sty, "Capabilities", capabilities))
		lines = append(lines, fabricDetailLine(sty, "Schemas", fmt.Sprintf(
			"%d input validated · %d output validated",
			item.inputSchemas, item.outputSchemas,
		)))
		if risks := formatFabricRiskCounts(item.riskCounts); risks != "" {
			lines = append(lines, fabricDetailLine(sty, "Authority", risks))
		}
	} else {
		lines = append(lines, fabricDetailLine(
			sty,
			"Schemas",
			"authoritative input + output JSON Schema validation",
		))
	}
	lines = append(lines,
		fabricDetailLine(sty, "Policy", "fail-closed · session permissions · PreToolUse hooks"),
		fabricDetailLine(sty, "Budgets", fmt.Sprintf(
			"timeout=%s · memory=%s · calls=%d · agents=%d · result=%s",
			formatFabricDuration(timeout), formatFabricBytes(memory), fabric.MaxNestedCalls, agents,
			formatFabricBytes(int64(resultLimit)),
		)),
		fabricDetailLine(sty, "Mesh", "not active · capability registry only"),
	)
	return lines
}

func fabricResultLines(
	sty *styles.Styles,
	result fabric.FabricExecResult,
	resultBytes int,
	expanded bool,
) []string {
	lines := []string{
		fabricDetailLine(sty, "Execution", cmp.Or(result.ExecutionID, "unknown")),
		fabricDetailLine(sty, "Outcome", string(result.Outcome)),
		fabricDetailLine(sty, "Envelope", formatFabricBytes(int64(resultBytes))),
	}
	if len(result.Trace.Phases) > 0 {
		lines = append(lines, fabricDetailLine(sty, "Phases", strings.Join(result.Trace.Phases, " → ")))
	}

	providers := fabricProviders(result.Trace.Operations)
	if len(providers) > 0 {
		lines = append(lines, fabricDetailLine(sty, "Providers", strings.Join(providers, ", ")))
	}

	succeeded, failed := fabricCallCounts(result.Trace.Operations)
	lines = append(lines, fabricDetailLine(sty, "Calls", fmt.Sprintf(
		"%d total · %d succeeded · %d failed", len(result.Trace.Operations), succeeded, failed,
	)))

	limit := fabricVisibleCalls
	if expanded {
		limit = len(result.Trace.Operations)
	}
	for i, operation := range result.Trace.Operations {
		if i >= limit {
			lines = append(lines, fabricDetailLine(
				sty,
				"Trace",
				fmt.Sprintf("…and %d more calls", len(result.Trace.Operations)-limit),
			))
			break
		}
		lines = append(lines, fabricOperationLine(sty, operation))
	}

	counts := result.Trace.Counts
	if counts.DroppedValues+counts.TruncatedValues+counts.RedactedValues+counts.DroppedOperations > 0 {
		lines = append(lines, fabricDetailLine(sty, "Trace", fmt.Sprintf(
			"redacted=%d · truncated=%d · dropped=%d",
			counts.RedactedValues,
			counts.TruncatedValues,
			counts.DroppedValues+counts.DroppedOperations,
		)))
	}
	if len(result.Logs) > 0 {
		lines = append(lines, fabricDetailLine(sty, "Logs", fmt.Sprintf("%d sandbox entries", len(result.Logs))))
	}
	if len(result.Diagnostics) > 0 {
		diagnostic := result.Diagnostics[0]
		lines = append(lines, fabricDetailLine(sty, "Diagnostic", diagnostic.Message))
		if len(result.Diagnostics) > 1 {
			lines = append(lines, fabricDetailLine(
				sty,
				"Diagnostics",
				fmt.Sprintf("%d additional", len(result.Diagnostics)-1),
			))
		}
	}
	if result.Error != "" {
		lines = append(lines, fabricDetailLine(sty, "Error", result.Error))
	}
	return lines
}

func fabricProviders(operations []fabric.TraceOperation) []string {
	set := make(map[string]struct{})
	for _, operation := range operations {
		provider := operation.Provider
		if provider == "" {
			provider, _, _ = strings.Cut(operation.Ref, ".")
		}
		if provider != "" {
			set[provider] = struct{}{}
		}
	}
	providers := make([]string, 0, len(set))
	for provider := range set {
		providers = append(providers, provider)
	}
	slices.Sort(providers)
	return providers
}

func cloneFabricRiskCounts(counts map[fabric.RiskClass]int) map[fabric.RiskClass]int {
	if len(counts) == 0 {
		return nil
	}
	clone := make(map[fabric.RiskClass]int, len(counts))
	for risk, count := range counts {
		clone[risk] = count
	}
	return clone
}

func formatFabricRiskCounts(counts map[fabric.RiskClass]int) string {
	ordered := []fabric.RiskClass{
		fabric.RiskRead,
		fabric.RiskWrite,
		fabric.RiskExecute,
		fabric.RiskNetwork,
		fabric.RiskAgent,
	}
	parts := make([]string, 0, len(ordered))
	for _, risk := range ordered {
		if count := counts[risk]; count > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", risk, count))
		}
	}
	return strings.Join(parts, " · ")
}

func fabricSourceLines(source string) int {
	if source == "" {
		return 0
	}
	return strings.Count(source, "\n") + 1
}

func formatFabricLineCount(count int) string {
	if count == 1 {
		return "1 line"
	}
	return fmt.Sprintf("%d lines", count)
}

func fabricPhaseTimeline(phase string) string {
	switch phase {
	case "compile":
		return "● compile → ○ execute"
	case "execute":
		return "✓ compile → ● execute"
	default:
		return phase
	}
}

func fabricCallCounts(operations []fabric.TraceOperation) (succeeded, failed int) {
	for _, operation := range operations {
		if operation.Outcome == fabric.OutcomeSucceeded {
			succeeded++
		} else if operation.Outcome != "" {
			failed++
		}
	}
	return succeeded, failed
}

func fabricActivityLine(sty *styles.Styles, activity fabric.ExecutionActivity) string {
	if activity.Kind == fabric.ActivityProgress {
		message := cmp.Or(activity.Message, "progress update")
		return toolIcon(sty, ToolStatusRunning) + " " + sty.Tool.ParamMain.Render("progress") + " " + sty.Tool.ParamKey.Render(message)
	}
	status := ToolStatusRunning
	detail := "running"
	if activity.Kind == fabric.ActivityCallCompleted {
		detail = string(activity.Outcome)
		switch activity.Outcome {
		case fabric.OutcomeSucceeded:
			status = ToolStatusSuccess
		case fabric.OutcomeAborted:
			status = ToolStatusCanceled
		default:
			status = ToolStatusError
		}
		if activity.FailureStage != "" {
			detail += " at " + string(activity.FailureStage)
		}
		if activity.Error != "" {
			detail += ": " + activity.Error
		}
	}
	return toolIcon(sty, status) + " " + sty.Tool.ParamMain.Render(activity.Ref) + " " + sty.Tool.ParamKey.Render(detail)
}

func fabricOperationLine(sty *styles.Styles, operation fabric.TraceOperation) string {
	status := ToolStatusRunning
	switch operation.Outcome {
	case fabric.OutcomeSucceeded:
		status = ToolStatusSuccess
	case fabric.OutcomeAborted:
		status = ToolStatusCanceled
	case fabric.OutcomeFailed, fabric.OutcomeTimedOut, fabric.OutcomeIndeterminate:
		status = ToolStatusError
	}
	ref := operation.Ref
	if ref == "" {
		ref = strings.TrimPrefix(operation.Provider+"."+operation.Action, ".")
	}
	detail := string(operation.Outcome)
	if operation.FailureStage != "" {
		detail += " at " + string(operation.FailureStage)
	}
	if operation.Error != "" {
		detail += ": " + operation.Error
	}
	return toolIcon(sty, status) + " " + sty.Tool.ParamMain.Render(ref) + " " + sty.Tool.ParamKey.Render(detail)
}

func fabricDetailLine(sty *styles.Styles, label, value string) string {
	return sty.Tool.ParamKey.Render(label+":") + " " + sty.Tool.FabricValue.Render(value)
}

func fabricSectionLine(sty *styles.Styles, title string) string {
	return sty.Tool.FabricSection.Render("▸ " + title)
}

func renderFabricPanel(sty *styles.Styles, lines []string, width int) string {
	const panelFrameWidth = 4 // Two border cells and one padding cell per side.
	bodyWidth := max(1, width-panelFrameWidth)
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, bodyWidth, "…")
	}
	return sty.Tool.FabricPanel.Width(bodyWidth).Render(strings.Join(lines, "\n"))
}

func fabricResultValue(result fabric.FabricExecResult) string {
	if result.Value == nil {
		return ""
	}
	encoded, err := json.Marshal(result.Value)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func formatFabricDuration(value time.Duration) string {
	if value%time.Second == 0 {
		return value.String()
	}
	return value.Round(time.Millisecond).String()
}

func formatFabricBytes(value int64) string {
	const (
		kibibyte = int64(1 << 10)
		mebibyte = int64(1 << 20)
	)
	if value >= mebibyte && value%mebibyte == 0 {
		return fmt.Sprintf("%d MiB", value/mebibyte)
	}
	if value >= kibibyte && value%kibibyte == 0 {
		return fmt.Sprintf("%d KiB", value/kibibyte)
	}
	return fmt.Sprintf("%d bytes", value)
}
