// Package fabric contains Ultra's host-neutral programmable capability kernel.
package fabric

import (
	"context"
	"encoding/json"
	"sync"
)

// JSONValue is a value that must satisfy ValidateJSON before crossing the
// sandbox boundary.
type JSONValue = any

// JSONObject is the only object shape accepted across the sandbox boundary.
type JSONObject = map[string]JSONValue

// RiskClass describes the broad authority required by an action.
type RiskClass string

const (
	RiskRead    RiskClass = "read"
	RiskWrite   RiskClass = "write"
	RiskExecute RiskClass = "execute"
	RiskNetwork RiskClass = "network"
	RiskAgent   RiskClass = "agent"
)

// EffectKind describes how an action changes external state.
type EffectKind string

const (
	EffectNone          EffectKind = "none"
	EffectEmission      EffectKind = "emission"
	EffectTransactional EffectKind = "transactional"
	EffectUnknown       EffectKind = "unknown"
)

// EffectDescriptor describes the resources and ordering of an action.
type EffectDescriptor struct {
	Kind        EffectKind `json:"kind"`
	Resources   []string   `json:"resources,omitempty"`
	Commutative bool       `json:"commutative,omitempty"`
}

// ActionAnnotations carry provider hints. Policy must not trust annotations
// more than the authoritative risk and effect fields.
type ActionAnnotations struct {
	ReadOnly    bool `json:"read_only,omitempty"`
	Destructive bool `json:"destructive,omitempty"`
	Idempotent  bool `json:"idempotent,omitempty"`
}

// ActionDescriptor is an execution-free action contract.
type ActionDescriptor struct {
	Name         string             `json:"name"`
	Description  string             `json:"description"`
	InputSchema  json.RawMessage    `json:"input_schema"`
	OutputSchema json.RawMessage    `json:"output_schema,omitempty"`
	Risk         RiskClass          `json:"risk"`
	Effect       *EffectDescriptor  `json:"effect,omitempty"`
	Annotations  *ActionAnnotations `json:"annotations,omitempty"`
}

// ListActionsRequest bounds provider discovery.
type ListActionsRequest struct {
	Namespace string
	Query     string
	Limit     int
}

// DiscoveryContext is the host context available during catalog discovery.
type DiscoveryContext struct {
	CWD string
}

// ActivityUpdate is a bounded progress update from a nested invocation.
type ActivityUpdate struct {
	Kind    string     `json:"kind"`
	Message string     `json:"message,omitempty"`
	Data    JSONObject `json:"data,omitempty"`
}

// InvocationContext is the authority-free metadata passed to providers.
// Cancellation and deadlines are carried by Context.
type InvocationContext struct {
	Context          context.Context
	CWD              string
	ExecutionID      string
	ParentToolCallID string
	NestedToolCallID string
	SessionID        string
	AgentID          string
	CapabilityViewID string
	Update           func(ActivityUpdate)
	state            *sync.Map
}

func (c *InvocationContext) ensureState() {
	if c.state == nil {
		c.state = &sync.Map{}
	}
}

// SetState stores invocation-local provider lifecycle data. It is not exposed
// to guest code and lives only for this nested call.
func (c *InvocationContext) SetState(key string, value any) {
	if c.state == nil {
		c.state = &sync.Map{}
	}
	c.state.Store(key, value)
}

// State returns invocation-local provider lifecycle data.
func (c InvocationContext) State(key string) (any, bool) {
	if c.state == nil {
		return nil, false
	}
	return c.state.Load(key)
}
