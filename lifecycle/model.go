package lifecycle

import "time"

// Clock returns the current instant. Injected so tests drive time
// deterministically; production usage supplies time.Now.
type Clock func() time.Time

// Trigger selects how a transition is initiated.
type Trigger int

const (
	TriggerExpiry Trigger = iota
	TriggerAction
	TriggerForced
)

// Guard evaluates a non-temporal precondition (property or link based).
type Guard interface {
	Allowed(ctx *EvalContext) bool
}

// GuardFunc adapts a function to Guard.
type GuardFunc func(ctx *EvalContext) bool

func (f GuardFunc) Allowed(ctx *EvalContext) bool { return f(ctx) }

// EvalContext is handed to guards during settlement.
type EvalContext struct {
	Engine   *Engine
	Instance string
	Now      time.Time
}

// Property reads a property of the guarded instance. It is intended for
// guards, which already run under the engine lock.
func (c *EvalContext) Property(name string) (string, bool) {
	v, ok := c.Engine.insts[c.Instance].props[name]
	return v, ok
}

// Link reads a link target of the guarded instance (lock-held context).
func (c *EvalContext) Link(name string) (string, bool) {
	v, ok := c.Engine.insts[c.Instance].links[name]
	return v, ok
}

// TransitionDef declares a single lifecycle transition of an object type.
type TransitionDef struct {
	Name     string
	From     string
	To       string
	Trigger  Trigger
	Duration time.Duration // TriggerExpiry only
	Actions  []string      // TriggerAction: action names invoking it
	Guard    Guard         // optional non-temporal precondition
	Chain    []ChainEffect // cross-instance effects (expiry/forced only)
}

// ChainEffect forces a named transition on the instance reached via Link.
type ChainEffect struct {
	Link           string
	TargetFrom     string
	TransitionName string
	Guard          Guard
}

// Property is a named attribute value of an instance.
type Property struct {
	Name  string
	Value string
}

// Snapshot is an immutable read view of an instance at a moment.
type Snapshot struct {
	Instance  string
	State     string
	EnteredAt time.Time
	Version   int64
	Now       time.Time
}
