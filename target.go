package coercion

// Target declares the desired Go type and nullability of one property.
type Target struct {
	// Kind is the scalar or slice target.
	Kind Kind
	// Nullable allows an explicit nil to produce StateNull.
	Nullable bool
}

// Converter is stateless and safe for concurrent use.
type Converter struct {
	mode Mode
}

// New returns an immutable converter using the requested mode.
func New(mode Mode) *Converter {
	return &Converter{mode: mode}
}

// FromMap converts properties[key]. A missing key is distinguishable from nil.
func (c *Converter) FromMap(properties map[string]any, key string, target Target) (Result, error) {
	value, ok := properties[key]
	if !ok {
		if target.Nullable {
			return Result{State: StateMissing, Degradations: []Degradation{}}, nil
		}
		return Result{State: StateMissing, Degradations: []Degradation{}}, missingError(target)
	}
	return c.Convert(value, target)
}

// Convert converts an explicitly supplied value.
func (c *Converter) Convert(value any, target Target) (Result, error) {
	ctx := &context{mode: c.mode, target: target}

	if value == nil {
		if target.Nullable {
			return Result{State: StateNull, Degradations: []Degradation{}}, nil
		}
		ctx.report(&Error{
			Category: Null,
			Target:   target.Kind,
			Original: nil,
			Detail:   "non-nullable target received explicit null",
		})
		return nullResult(), ctx.err()
	}

	if target.Kind.isSlice() {
		value, err := ctx.convertSlice(value)
		state := StatePresent
		if ctx.sliceNil {
			state = StateNull
		}
		result := ctx.result(state, value)
		if state == StateNull {
			result.Value = nil
		}
		return result, err
	}

	converted := ctx.convertScalar(value, target.Kind)
	return ctx.result(StatePresent, converted), ctx.err()
}

func nullResult() Result {
	return Result{State: StateNull, Degradations: []Degradation{}}
}

func missingError(target Target) error {
	return Errors{{
		Category: Missing,
		Target:   target.Kind,
		Detail:   "key is absent",
	}}
}
