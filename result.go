package coercion

// Mode controls whether degradation is an error or a recorded warning.
type Mode int

const (
	// Strict rejects every overflow, precision loss, or invalid value.
	Strict Mode = iota + 1
	// Lenient applies the documented degraded conversion and records it.
	Lenient
)

// State distinguishes present values, explicit null, and missing keys.
type State int

const (
	// StatePresent is a non-nil value, including zero values.
	StatePresent State = iota + 1
	// StateNull is an explicitly present nil.
	StateNull
	// StateMissing means the key did not exist.
	StateMissing
)

// Degradation describes one accepted loss in lenient mode.
type Degradation struct {
	// Category is the same ErrorCategory the strict mode would return.
	Category ErrorCategory
	// Index is >= 0 for a slice element degradation.
	Index    int
	hasIndex bool
	// Original is the value before conversion.
	Original any
	// Converted is the value after conversion.
	Converted any
	// Detail identifies what information was lost.
	Detail string
	// Skipped marks a slice element with no target-typed fallback value.
	Skipped bool
}

// Result is the outcome for one property conversion.
type Result struct {
	// State is always meaningful, even when Err is non-nil.
	State State
	// Value holds a scalar or a freshly allocated target slice.
	Value any
	// Degradations belong to this call only and never alias another call.
	Degradations []Degradation
}

// IsMissing reports whether the key was absent.
func (r Result) IsMissing() bool { return r.State == StateMissing }

// IsNull reports whether the source explicitly supplied nil.
func (r Result) IsNull() bool { return r.State == StateNull }

// IsPresent reports a non-nil value, including "", 0, false, and empty slices.
func (r Result) IsPresent() bool { return r.State == StatePresent }

// Categories returns ordered degradation categories for strict/lenient comparison.
func (r Result) Categories() []ErrorCategory {
	out := make([]ErrorCategory, len(r.Degradations))
	for i := range r.Degradations {
		out[i] = r.Degradations[i].Category
	}
	return out
}
