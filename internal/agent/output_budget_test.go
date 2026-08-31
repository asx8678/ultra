package agent

import (
	"context"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

type outputBudgetProbeModel struct {
	fantasy.LanguageModel
	calls    []int64
	usages   []fantasy.Usage
	noFinish bool
}

func (m *outputBudgetProbeModel) Provider() string { return "probe" }
func (m *outputBudgetProbeModel) Model() string    { return "probe" }
func (m *outputBudgetProbeModel) Stream(_ context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	m.calls = append(m.calls, *call.MaxOutputTokens)
	usage := m.usages[len(m.calls)-1]
	return func(yield func(fantasy.StreamPart) bool) {
		if m.noFinish {
			yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, Delta: "partial"})
			return
		}
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, Usage: usage})
	}, nil
}

func TestOutputBudgetModelClampsEveryStepToRemainingBudget(t *testing.T) {
	probe := &outputBudgetProbeModel{usages: []fantasy.Usage{{OutputTokens: 4}, {OutputTokens: 1}}}
	budget := newOutputWorkerBudget(5)
	model := withOutputBudgetModel(probe, budget)
	requested := int64(5)
	for range 2 {
		stream, err := model.Stream(t.Context(), fantasy.Call{MaxOutputTokens: &requested})
		require.NoError(t, err)
		stream(func(fantasy.StreamPart) bool { return true })
	}
	require.Equal(t, []int64{5, 1}, probe.calls)
	_, err := model.Stream(t.Context(), fantasy.Call{MaxOutputTokens: &requested})
	require.ErrorIs(t, err, errOutputTokenBudgetExhausted)
}

func TestOutputBudgetModelChargesUnknownFailure(t *testing.T) {
	probe := &outputBudgetProbeModel{usages: []fantasy.Usage{{}}, noFinish: true}
	budget := newOutputWorkerBudget(5)
	model := withOutputBudgetModel(probe, budget)
	requested := int64(5)
	stream, err := model.Stream(t.Context(), fantasy.Call{MaxOutputTokens: &requested})
	require.NoError(t, err)
	// Stop consuming before a finish record; the full grant remains charged.
	stream(func(fantasy.StreamPart) bool { return false })
	_, err = model.Stream(t.Context(), fantasy.Call{MaxOutputTokens: &requested})
	require.ErrorIs(t, err, errOutputTokenBudgetExhausted)
}
