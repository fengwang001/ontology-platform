package ontology

// Kind identifies a supported target type.
type Kind int

const (
	String Kind = iota
	Int64
	Float64
	Bool
	Strings
	Int64s
	Float64s
	Bools
)

// Mode selects strict or lenient conversion behavior.
type Mode int

const (
	Strict Mode = iota
	Lenient
)

// Status distinguishes the three top-level outcomes of a coercion.
type Status int

const (
	// StatusPresent means the key existed and held a non-nil value.
	StatusPresent Status = iota
	// StatusExplicitNull means the key existed but its value was nil.
	StatusExplicitNull
	// StatusMissing means the input map had no such key at all.
	StatusMissing
)

// IsScalar reports whether k is a scalar (non-slice) target kind.
func (k Kind) IsScalar() bool {
	return k == String || k == Int64 || k == Float64 || k == Bool
}

// ElementKind returns the scalar element kind of a slice kind.
// For scalar kinds it returns the kind itself.
func (k Kind) ElementKind() Kind {
	switch k {
	case Strings:
		return String
	case Int64s:
		return Int64
	case Float64s:
		return Float64
	case Bools:
		return Bool
	default:
		return k
	}
}

// Target declares the target type and nullability of a property.
type Target struct {
	Kind     Kind
	Nullable bool
}

// Outcome is the result of a coercion. Exactly one of the semantic
// fields (String, Int64, Float64, Bool, Slice) is meaningful according
// to Target.Kind; use Status to tell missing / explicit-null apart.
type Outcome struct {
	// Status is the top-level classification.
	Status Status

	// Scalar results.
	String  string
	Int64   int64
	Float64 float64
	Bool    bool

	// Slice results. Slice is nil when the input slice was nil; a
	// non-nil zero-length slice means the input was an empty slice.
	Slice []any
	// NilSlice is true when the input was an explicitly nil slice.
	NilSlice bool

	// Records holds one entry per degradation that occurred in lenient
	// mode. It is always empty in strict mode and on clean conversions.
	Records []Record
}

// IsMissing reports whether the property key was absent.
func (o Outcome) IsMissing() bool { return o.Status == StatusMissing }

// IsExplicitNull reports whether the key was present with a nil value.
func (o Outcome) IsExplicitNull() bool { return o.Status == StatusExplicitNull }

// IsZeroValue reports whether a present value coerced to k's zero
// value without degradation ("", 0, 0.0, false, or an empty slice).
// Missing and explicit-null outcomes always report false, so the three
// cases stay separable.
func (o Outcome) IsZeroValue(k Kind) bool {
	if o.Status != StatusPresent {
		return false
	}
	switch k {
	case String:
		return o.String == ""
	case Int64:
		return o.Int64 == 0
	case Float64:
		return o.Float64 == 0
	case Bool:
		return !o.Bool
	case Strings, Int64s, Float64s, Bools:
		return o.Slice != nil && len(o.Slice) == 0
	default:
		return false
	}
}
