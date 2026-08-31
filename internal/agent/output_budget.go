package agent

import (
	"context"
	"errors"
	"sync"

	"charm.land/fantasy"
)

var errOutputTokenBudgetExhausted = errors.New("output-token budget exhausted")

type outputWorkerBudgetKey struct{}

type outputWorkerBudget struct {
	mu        sync.Mutex
	remaining int64
	attempts  int
	usage     fantasy.Usage
}

func newOutputWorkerBudget(limit int64) *outputWorkerBudget {
	if limit < 0 {
		limit = 0
	}
	return &outputWorkerBudget{remaining: limit}
}

func withOutputWorkerBudget(ctx context.Context, budget *outputWorkerBudget) context.Context {
	return context.WithValue(ctx, outputWorkerBudgetKey{}, budget)
}

func outputWorkerBudgetFromContext(ctx context.Context) *outputWorkerBudget {
	budget, _ := ctx.Value(outputWorkerBudgetKey{}).(*outputWorkerBudget)
	return budget
}

func (b *outputWorkerBudget) reserve(requested int64) (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.remaining <= 0 {
		return 0, errOutputTokenBudgetExhausted
	}
	grant := b.remaining
	if requested > 0 && requested < grant {
		grant = requested
	}
	b.remaining -= grant
	b.attempts++
	return grant, nil
}

func (b *outputWorkerBudget) settle(grant int64, usage fantasy.Usage, known bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if known {
		used := usage.OutputTokens
		if used < 0 {
			used = 0
		}
		if used < grant {
			b.remaining += grant - used
		}
		b.usage.InputTokens += usage.InputTokens
		b.usage.OutputTokens += usage.OutputTokens
		b.usage.TotalTokens += usage.TotalTokens
		b.usage.ReasoningTokens += usage.ReasoningTokens
		b.usage.CacheCreationTokens += usage.CacheCreationTokens
		b.usage.CacheReadTokens += usage.CacheReadTokens
	}
	// Unknown/error streams conservatively retain the whole reservation.
}

func (b *outputWorkerBudget) observeFallback(usage fantasy.Usage, failed bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.attempts != 0 {
		return
	}
	used := usage.OutputTokens
	if failed && used == 0 {
		used = b.remaining
	}
	if used > b.remaining {
		used = b.remaining
	}
	b.remaining -= used
	b.usage = usage
}

func (b *outputWorkerBudget) observedUsage() fantasy.Usage {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.usage
}

type outputBudgetLanguageModel struct {
	fantasy.LanguageModel
	budget *outputWorkerBudget
}

func withOutputBudgetModel(model fantasy.LanguageModel, budget *outputWorkerBudget) fantasy.LanguageModel {
	if model == nil || budget == nil {
		return model
	}
	return &outputBudgetLanguageModel{LanguageModel: model, budget: budget}
}

func (m *outputBudgetLanguageModel) Generate(ctx context.Context, call fantasy.Call) (*fantasy.Response, error) {
	requested := int64(0)
	if call.MaxOutputTokens != nil {
		requested = *call.MaxOutputTokens
	}
	grant, err := m.budget.reserve(requested)
	if err != nil {
		return nil, err
	}
	call.MaxOutputTokens = &grant
	response, err := m.LanguageModel.Generate(ctx, call)
	if err != nil || response == nil {
		m.budget.settle(grant, fantasy.Usage{}, false)
		return response, err
	}
	m.budget.settle(grant, response.Usage, true)
	return response, nil
}

func (m *outputBudgetLanguageModel) Stream(ctx context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	requested := int64(0)
	if call.MaxOutputTokens != nil {
		requested = *call.MaxOutputTokens
	}
	grant, err := m.budget.reserve(requested)
	if err != nil {
		return nil, err
	}
	call.MaxOutputTokens = &grant
	stream, err := m.LanguageModel.Stream(ctx, call)
	if err != nil {
		m.budget.settle(grant, fantasy.Usage{}, false)
		return nil, err
	}
	return func(yield func(fantasy.StreamPart) bool) {
		settled := false
		defer func() {
			if !settled {
				m.budget.settle(grant, fantasy.Usage{}, false)
			}
		}()
		stream(func(part fantasy.StreamPart) bool {
			if part.Type == fantasy.StreamPartTypeFinish {
				m.budget.settle(grant, part.Usage, true)
				settled = true
			} else if part.Type == fantasy.StreamPartTypeError {
				m.budget.settle(grant, fantasy.Usage{}, false)
				settled = true
			}
			return yield(part)
		})
	}, nil
}
