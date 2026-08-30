package fabric

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExecutionBudgetBoundsAgentAndNestedCalls(t *testing.T) {
	t.Parallel()
	budget := newExecutionBudget(1, nil)
	require.NoError(t, budget.ChargeNestedCall("host.view", ActionDescriptor{Risk: RiskRead}))
	require.NoError(t, budget.ChargeNestedCall("host.agent", ActionDescriptor{Risk: RiskAgent}))
	require.ErrorContains(t, budget.ChargeNestedCall("host.agent", ActionDescriptor{Risk: RiskAgent}), "agent call limit")

	nested := newExecutionBudget(1, nil)
	for range MaxNestedCalls {
		require.NoError(t, nested.ChargeNestedCall("host.view", ActionDescriptor{Risk: RiskRead}))
	}
	require.ErrorContains(t, nested.ChargeNestedCall("host.view", ActionDescriptor{Risk: RiskRead}), "nested call limit")
}
