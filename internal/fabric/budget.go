package fabric

import (
	"fmt"
	"sync"
)

// MaxNestedCalls bounds native capability calls issued by one execution.
const MaxNestedCalls = 128

// ExecutionBudget bounds one execution independently of provider behavior.
type ExecutionBudget struct {
	mu             sync.Mutex
	parent         BudgetLedger
	maxNestedCalls int
	maxAgents      int
	nestedCalls    int
	agents         int
}

func newExecutionBudget(maxAgents int, parent BudgetLedger) *ExecutionBudget {
	if maxAgents <= 0 {
		maxAgents = 4
	}
	return &ExecutionBudget{
		parent: parent, maxNestedCalls: MaxNestedCalls, maxAgents: maxAgents,
	}
}

// ChargeNestedCall reserves capacity before provider preparation or effects.
func (b *ExecutionBudget) ChargeNestedCall(ref string, descriptor ActionDescriptor) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.nestedCalls >= b.maxNestedCalls {
		return fmt.Errorf("nested call limit %d reached", b.maxNestedCalls)
	}
	if descriptor.Risk == RiskAgent && b.agents >= b.maxAgents {
		return fmt.Errorf("agent call limit %d reached", b.maxAgents)
	}
	if b.parent != nil {
		if err := b.parent.ChargeNestedCall(ref, descriptor); err != nil {
			return err
		}
	}
	b.nestedCalls++
	if descriptor.Risk == RiskAgent {
		b.agents++
	}
	return nil
}
