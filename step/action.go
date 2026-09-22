package step

import "context"

// Action is one unit of workflow work. Implementations must be idempotent in
// the sense that being invoked at most once per successful outcome is enough:
// the orchestrator never re-invokes a Completed step after recovery, because
// the journal, not memory, is the source of truth.
type Action interface {
	// Run performs the step. It may block; the orchestrator honours ctx.
	Run(ctx context.Context) error
	// Compensate undoes a previously successful Run.
	Compensate(ctx context.Context) error
}

// ActionFunc adapts a pair of functions to Action.
type ActionFunc struct {
	Do    func(ctx context.Context) error
	Undo  func(ctx context.Context) error
}

// Run calls Do.
func (a ActionFunc) Run(ctx context.Context) error { return a.Do(ctx) }

// Compensate calls Undo. A nil Undo always succeeds.
func (a ActionFunc) Compensate(ctx context.Context) error {
	if a.Undo == nil {
		return nil
	}
	return a.Undo(ctx)
}

// Registry maps step IDs to their actions.
type Registry struct {
	actions map[string]Action
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{actions: map[string]Action{}}
}

// Register binds an action to a step ID.
func (r *Registry) Register(id string, a Action) {
	r.actions[id] = a
}

// Get returns the action for id.
func (r *Registry) Get(id string) (Action, bool) {
	a, ok := r.actions[id]
	return a, ok
}
