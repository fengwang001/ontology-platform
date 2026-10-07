package ontology

// ActionID identifies an action declared generically on the ontology.
type ActionID string

// TypeID identifies a concrete object type.
type TypeID string

// Implementation is concrete execution logic registered by one object type
// for one action. Implementations are immutable: replacing logic means
// registering a new generation, never mutating an existing one.
type Implementation interface {
	// Pre decides whether the implementation accepts input. A false result
	// means this input would be rejected by the implementation.
	Pre(input any) bool
	// Execute runs the logic. It is invoked only after Pre passes.
	Execute(ctx *ExecContext, input any) (any, error)
	// Post validates the produced output.
	Post(input any, output any) bool
}

// ExecContext exposes the object and generation timing to an implementation.
type ExecContext struct {
	Object    *Instance
	StartedAt uint64
}

// Action declares only the interface shape of an action: input/output types
// and pre/post conditions. It never carries execution logic itself.
type Action struct {
	ID         ActionID
	InputType  string
	OutputType string
	// Precondition is the interface-level precondition shared by every
	// implementation; implementations may additionally accept more inputs
	// (relaxation) but must declare such relaxation on registration.
	Precondition func(input any) bool
	// Postcondition is the interface-level postcondition checked after an
	// implementation returns.
	Postcondition func(input any, output any) bool
}
