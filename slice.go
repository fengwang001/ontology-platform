package ontology

import "reflect"

// coerceSlice converts each element independently to elemKind.
//
// Nil and empty slices stay distinct: a nil slice yields Slice==nil
// with NilSlice==true; an empty slice yields a non-nil zero-length
// slice; neither is ever "missing".
//
// Elements are always validated with strict semantics. In strict mode
// the first bad element fails the whole conversion and the returned
// error carries the element index plus the element's own category. In
// lenient mode bad elements are skipped (never replaced with defaults)
// and each skip is recorded with the same category it would have
// errored with, so per-element category sets match across modes.
func coerceSlice(raw any, elemKind Kind, mode Mode) (Outcome, error) {
	values, nilSlice, err := sliceValues(raw)
	if err != nil {
		return Outcome{Status: StatusPresent}, err
	}
	if nilSlice {
		return Outcome{Status: StatusPresent, Slice: nil, NilSlice: true}, nil
	}

	result := make([]any, 0, len(values))
	var records []Record
	for i, elem := range values {
		if elem == nil {
			e := elementErr(i, newErr(CatNull, "element is explicitly null"))
			if mode == Strict {
				return Outcome{Status: StatusPresent}, e
			}
			records = append(records, Record{
				Category: CatNull, From: nil, To: nil, Index: i,
				Detail: "element skipped; no default inserted",
			})
			continue
		}
		// Classify the element with strict semantics so strict errors
		// and lenient records stay category-identical.
		converted, convErr := coerceScalar(elem, elemKind, Strict)
		if convErr != nil {
			inner := AsCoerceError(convErr)
			e := elementErr(i, inner)
			if mode == Strict {
				return Outcome{Status: StatusPresent}, e
			}
			records = append(records, Record{
				Category: inner.Category, From: elem, To: nil, Index: i,
				Detail: "element skipped; no default inserted",
			})
			continue
		}
		result = append(result, scalarField(converted, elemKind))
	}
	return Outcome{Status: StatusPresent, Slice: result, NilSlice: false, Records: records}, nil
}

func sliceValues(raw any) ([]any, bool, error) {
	if s, ok := raw.([]any); ok {
		if s == nil {
			return nil, true, nil
		}
		return s, false, nil
	}
	rv := reflect.ValueOf(raw)
	if rv.Kind() != reflect.Slice {
		return nil, false, newErr(CatTypeMismatch, "cannot coerce %s to slice", typeName(raw))
	}
	if rv.IsNil() {
		return nil, true, nil
	}
	s := make([]any, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		s[i] = rv.Index(i).Interface()
	}
	return s, false, nil
}

func scalarField(o Outcome, k Kind) any {
	switch k {
	case String:
		return o.String
	case Int64:
		return o.Int64
	case Float64:
		return o.Float64
	case Bool:
		return o.Bool
	default:
		return nil
	}
}
