package layout

import "errors"

// Error classes produced by the subsystem. Every rejected operation returns
// one of these sentinel errors (possibly wrapped) and leaves all state
// untouched.
var (
	// ErrUndefinedType is returned when a direct (embedded) field refers to a
	// type that has not been registered, or when a query names an unknown
	// type.
	ErrUndefinedType = errors.New("undefined type")
	// ErrInvalidAlignment is returned when an alignment value is not a member
	// of the configured allowed-alignment set.
	ErrInvalidAlignment = errors.New("alignment value not allowed")
	// ErrDuplicateField is returned when one composite type declares two
	// fields with the same name.
	ErrDuplicateField = errors.New("duplicate field name")
	// ErrEmbeddingCycle is returned when direct embedding forms a cycle
	// (a type directly or transitively embeds itself).
	ErrEmbeddingCycle = errors.New("direct embedding cycle")
	// ErrSizeExceeded is returned when a (re)computed total size exceeds
	// MaxSize.
	ErrSizeExceeded = errors.New("type size exceeds configured limit")
	// ErrDependentOversize is returned when modifying a type would force a
	// dependent type beyond MaxSize; the whole modification is rejected.
	ErrDependentOversize = errors.New("modification would oversize a dependent")
	// ErrDuplicateType is returned when registering a type whose name is
	// already taken.
	ErrDuplicateType = errors.New("type already registered")
	// ErrEmptyName is returned for empty type or field names.
	ErrEmptyName = errors.New("empty name")
)

// Config holds the fixed parameters of the layout machine.
//
//   - PtrSize/PtrAlign fix the size and alignment of every indirect-reference
//     field, independently of the referenced type.
//   - EmptySize/EmptyAlign fix the size and alignment of a field-less
//     composite type.
//   - MaxSize is the inclusive upper bound for any type's total size.
//   - AllowedAlignments is the set of legal alignment values; it must contain
//     every alignment a computation can produce (PtrAlign, EmptyAlign and all
//     basic-type alignments), otherwise registration fails with
//     ErrInvalidAlignment.
type Config struct {
	PtrSize           int
	PtrAlign          int
	EmptySize         int
	EmptyAlign        int
	MaxSize           int
	AllowedAlignments map[int]bool
}

// DefaultConfig returns the common 64-bit configuration: 8-byte pointers,
// zero-sized empty composites sized 1/1, a 16&nbsp;MiB size cap and the
// power-of-two alignments 1,2,4,8,16,32,64.
func DefaultConfig() Config {
	return Config{
		PtrSize:           8,
		PtrAlign:          8,
		EmptySize:         1,
		EmptyAlign:        1,
		MaxSize:           16 << 20,
		AllowedAlignments: map[int]bool{1: true, 2: true, 4: true, 8: true, 16: true, 32: true, 64: true},
	}
}

func (c Config) valid() bool {
	return c.PtrSize > 0 && c.PtrAlign > 0 && c.EmptySize > 0 && c.EmptyAlign > 0 &&
		c.MaxSize > 0 && len(c.AllowedAlignments) > 0 &&
		c.allowed(c.PtrAlign) && c.allowed(c.EmptyAlign)
}

func (c Config) allowed(align int) bool {
	return c.AllowedAlignments != nil && c.AllowedAlignments[align]
}

// minAllowed returns the smallest permitted alignment, used for types whose
// fields contribute no alignment (only compact fields).
func (c Config) minAllowed() int {
	m := 0
	for a := range c.AllowedAlignments {
		if m == 0 || a < m {
			m = a
		}
	}
	return m
}
