package fabric

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTracePreservesIssueOrder(t *testing.T) {
	t.Parallel()

	recorder := NewTraceRecorder()
	recorder.Phase("running")
	first := recorder.BeginCall("host.first", JSONObject{"value": 1})
	second := recorder.BeginCall("host.second", JSONObject{"value": 2})
	third := recorder.BeginCall("host.third", JSONObject{"value": 3})

	var group sync.WaitGroup
	group.Add(3)
	go func() {
		defer group.Done()
		third.Complete(CallCompletion{Outcome: OutcomeSucceeded, Result: "third"})
	}()
	go func() {
		defer group.Done()
		second.Complete(CallCompletion{Outcome: OutcomeFailed, FailureStage: FailureInvoke, Error: "failed"})
	}()
	go func() {
		defer group.Done()
		first.Complete(CallCompletion{Outcome: OutcomeSucceeded, Result: "first"})
	}()
	group.Wait()

	trace := recorder.Seal(OutcomeFailed, "one call failed")
	require.Equal(t, "ultra.fabric.execution", trace.Kind)
	require.Equal(t, 1, trace.Version)
	require.Equal(t, []string{"running"}, trace.Phases)
	require.Equal(t, []string{"host.first", "host.second", "host.third"}, []string{
		trace.Operations[0].Ref,
		trace.Operations[1].Ref,
		trace.Operations[2].Ref,
	})
	require.Equal(t, []int{1, 2, 3}, []int{
		trace.Operations[0].Sequence,
		trace.Operations[1].Sequence,
		trace.Operations[2].Sequence,
	})
	require.Equal(t, OutcomeSucceeded, trace.Operations[0].Outcome)
	require.Equal(t, OutcomeFailed, trace.Operations[1].Outcome)
	require.Equal(t, OutcomeSucceeded, trace.Operations[2].Outcome)
	require.Nil(t, trace.Operations[0].Args)
	require.Nil(t, trace.Operations[0].Result)
	require.Equal(t, digestTraceValue(JSONObject{"value": 1}), trace.Operations[0].ArgsDigest)
	require.Equal(t, digestTraceValue("first"), trace.Operations[0].ResultDigest)
	require.Equal(t, 5, trace.Counts.RedactedValues)
}
