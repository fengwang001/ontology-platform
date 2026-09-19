package ontology

import "fmt"

// Category classifies every way a coercion can be rejected (strict
// mode) or degraded (lenient mode). Strict-mode errors and lenient-mode
// degradation records always use the same set of categories, so callers
// can assert the same condition against either.
type Category string

const (
	// CatMissing: the input map has no such key.
	CatMissing Category = "missing"
	// CatNull: the key exists but its value is nil.
	CatNull Category = "explicit_null"
	// CatTypeMismatch: the value's dynamic type cannot be converted to
	// the target type at all (e.g. a map into an int64).
	CatTypeMismatch Category = "type_mismatch"
	// CatOverflow: a numeric conversion leaves the representable range
	// (string too large for int64, float beyond int64, Inf/NaN).
	CatOverflow Category = "overflow"
	// CatFractionLost: converting float64 to int64 discards a
	// non-zero fractional part.
	CatFractionLost Category = "fraction_lost"
	// CatPrecisionLost: a value cannot be represented exactly by the
	// target numeric type (int64 -> float64 beyond 2^53, or an integral
	// float beyond 2^53 read as int64).
	CatPrecisionLost Category = "precision_lost"
	// CatInvalidBool: a value is not in the closed boolean acceptance
	// set {true,false,1,0} and their string forms.
	CatInvalidBool Category = "invalid_bool"
	// CatMalformedNumber: a string is not a syntactically valid number.
	CatMalformedNumber Category = "malformed_number"
	// CatElementSkipped: a slice element failed conversion in lenient
	// mode and was skipped.
	CatElementSkipped Category = "element_skipped"
)

// CoerceError is returned for every failed coercion in strict mode and
// for failed slice elements. Its Category is stable and machine
// readable; Index is >= 0 only for slice-element failures.
type CoerceError struct {
	Category Category
	Message  string
	Index    int
	// ElementCategory is set when the error wraps an element failure;
	// Index names the position and ElementCategory names the element's
	// own distortion.
	ElementCategory Category
}

func (e *CoerceError) Error() string {
	if e.Index >= 0 {
		return fmt.Sprintf("coercion error at index %d: %s", e.Index, e.Message)
	}
	return "coercion error: " + e.Message
}

// AsCoerceError extracts a *CoerceError from err, or returns nil.
func AsCoerceError(err error) *CoerceError {
	if err == nil {
		return nil
	}
	if ce, ok := err.(*CoerceError); ok {
		return ce
	}
	return nil
}

func newErr(cat Category, format string, args ...any) *CoerceError {
	return &CoerceError{Category: cat, Message: fmt.Sprintf(format, args...), Index: -1}
}

func elementErr(index int, inner *CoerceError) *CoerceError {
	return &CoerceError{
		Category:        inner.Category,
		Message:         fmt.Sprintf("element %d: %s", index, inner.Message),
		Index:           index,
		ElementCategory: inner.Category,
	}
}
