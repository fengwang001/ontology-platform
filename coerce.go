package ontology

// Converter performs type coercion. It is stateless and safe for
// concurrent use by any number of goroutines; every Coerce call
// allocates its own outcome, records, and result slices.
type Converter struct {
	mode Mode
}

// NewConverter returns a converter running in the given mode.
func NewConverter(mode Mode) *Converter {
	return &Converter{mode: mode}
}

// Coerce looks key up in input and coerces the found value to t.
//
// The three "absent" situations stay strictly distinguishable:
//   - key absent: StatusMissing; non-nullable targets also return an
//     error with category CatMissing, nullable targets return no error.
//   - key present with nil: StatusExplicitNull; non-nullable targets
//     return CatNull, nullable targets return no error.
//   - key present with ""/0/false: StatusPresent, zero-valued fields,
//     never an error and never a record.
func (c *Converter) Coerce(input map[string]any, key string, t Target) (Outcome, error) {
	raw, present := input[key]
	if !present {
		out := Outcome{Status: StatusMissing, Slice: nil}
		if !t.Nullable {
			return out, newErr(CatMissing, "property %q is missing", key)
		}
		return out, nil
	}
	if raw == nil {
		out := Outcome{Status: StatusExplicitNull, Slice: nil}
		if !t.Nullable {
			return out, newErr(CatNull, "property %q is explicitly null", key)
		}
		return out, nil
	}
	return c.coercePresent(raw, t)
}

// CoerceValue coerces a standalone non-nil value. It is the same logic
// Coerce applies once presence and nullness have been established.
func (c *Converter) CoerceValue(raw any, t Target) (Outcome, error) {
	if raw == nil {
		out := Outcome{Status: StatusExplicitNull, Slice: nil}
		if !t.Nullable {
			return out, newErr(CatNull, "value is explicitly null")
		}
		return out, nil
	}
	return c.coercePresent(raw, t)
}

func (c *Converter) coercePresent(raw any, t Target) (Outcome, error) {
	if t.Kind.IsScalar() {
		return coerceScalar(raw, t.Kind, c.mode)
	}
	return coerceSlice(raw, t.Kind.ElementKind(), c.mode)
}

// settle turns a per-value distortion into either a strict-mode error
// or a lenient-mode degradation record. It is the single point that
// guarantees strict errors and lenient records share one category set.
func settle(mode Mode, err *CoerceError, rec Record, out *Outcome) error {
	if err == nil {
		return nil
	}
	if mode == Strict {
		return err
	}
	if rec.Category == "" {
		rec = Record{Category: err.Category, Detail: err.Message, Index: -1}
	}
	out.Records = append(out.Records, rec)
	return nil
}
