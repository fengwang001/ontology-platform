// Package sm is the deterministic state machine: commands and a pure apply function.
// It must not depend on any other package in this module.
package sm

// Kind identifies a deterministic command.
type Kind int

const (
	KindAdd Kind = iota + 1 // 1-based so the zero Kind is invalid ("empty command").
	KindMul
)

// Cmd is a deterministic command over the integer accumulator state.
type Cmd struct {
	Kind Kind
	K    int
}

// Add returns the command state += k.
func Add(k int) Cmd { return Cmd{Kind: KindAdd, K: k} }

// Mul returns the command state *= k.
func Mul(k int) Cmd { return Cmd{Kind: KindMul, K: k} }

// Valid reports whether c is a recognized command.
func (c Cmd) Valid() bool {
	switch c.Kind {
	case KindAdd, KindMul:
		return true
	default:
		return false
	}
}

// Apply is the pure transition function: given a command and the current state,
// it returns the next state. Applying an unrecognized command is a no-op; repl
// never admits one through Append, so valid inputs always take a real branch.
func Apply(c Cmd, state int) int {
	switch c.Kind {
	case KindAdd:
		return state + c.K
	case KindMul:
		return state * c.K
	default:
		return state
	}
}
