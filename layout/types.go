package layout

// Field is one declared field of a composite type.
//
// TypeName names the field's type. Indirect fields reference it through a
// pointer: their size/alignment come from the configuration, so the target
// need not be registered. Embedded fields are laid out inline, so the target
// must already be registered and the embedding graph must stay acyclic.
//
// Compact suppresses offset alignment for this field and excludes the field
// from the composite type's own alignment.
type Field struct {
	Name     string
	TypeName string
	Indirect bool
	Compact  bool
}

// CompositeSpec is the immutable declaration of a composite type.
// MaxAlign, when positive, caps the effective alignment of every field to
// min(field alignment, MaxAlign); it must itself be an allowed alignment.
type CompositeSpec struct {
	Name     string
	Fields   []Field
	MaxAlign int // 0 means no cap
}

// FieldLayout is the resolved placement of one field.
type FieldLayout struct {
	Name     string
	TypeName string
	Offset   int
	Size     int
	Align    int
	Indirect bool
	Compact  bool
}

// Layout is a fully resolved composite-type layout snapshot.
type Layout struct {
	Name    string
	Fields  []FieldLayout
	Size    int
	Align   int
	Version int
}

// CompatKind is the result of comparing two successive versions of a type.
type CompatKind int

const (
	// Incompatible: anything not covered below (rename, reorder, alignment
	// change, compact-flag change, shrink, ...).
	Incompatible CompatKind = iota
	// FullyCompatible: every old field keeps offset/size/type, and total size
	// and alignment are unchanged.
	FullyCompatible
	// AppendCompatible: all old fields unchanged, only trailing new fields,
	// size strictly grows, alignment unchanged.
	AppendCompatible
)

func (k CompatKind) String() string {
	switch k {
	case FullyCompatible:
		return "fully-compatible"
	case AppendCompatible:
		return "append-compatible"
	default:
		return "incompatible"
	}
}

// Logger receives one line per public operation: the input, the outcome and
// the reason. A nil logger disables logging; implementations must be safe for
// concurrent use.
type Logger interface {
	Logf(format string, args ...any)
}

// Snapshot is the read-only view of one type; all values are taken at the
// same instant.
type Snapshot struct {
	Name           string
	Version        int
	Size           int
	Align          int
	Dependents     int // number of other types that embed this type directly
	RecomputeCount int // cumulative propagation-driven recomputations
}
