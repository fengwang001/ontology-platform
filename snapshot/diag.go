package snapshot

import "fmt"

// DiagCategory classifies recovery anomalies. The numeric order IS the
// fixed decision priority required by the recovery contract:
//
//  1. CatStructural       — section-level structural corruption
//  2. CatCountMismatch    — declared vs. actual record count mismatch
//  3. CatCrossRefMissing  — cross-section reference missing
//  4. CatActionDepsMissing— action record unusable due to missing deps
//
// The order is a data-dependency order, not an arbitrary ranking: count
// comparison is only meaningful for a section whose records parsed cleanly;
// reference checks only make sense for records that survived integrity
// checks; action usability depends on the outcome of reference checks.
// Lower-priority diagnostics never suppress higher-priority ones and vice
// versa: every category is collected and reported independently.
type DiagCategory int

const (
	CatStructural DiagCategory = iota + 1
	CatCountMismatch
	CatCrossRefMissing
	CatActionDepsMissing
)

func (c DiagCategory) String() string {
	switch c {
	case CatStructural:
		return "structural_corruption"
	case CatCountMismatch:
		return "declared_count_mismatch"
	case CatCrossRefMissing:
		return "cross_section_reference_missing"
	case CatActionDepsMissing:
		return "action_dependencies_missing"
	default:
		return fmt.Sprintf("unknown(%d)", int(c))
	}
}

// MismatchDirection distinguishes the two count-mismatch anomalies.
type MismatchDirection int

const (
	MismatchNone MismatchDirection = iota
	// DeclaredGreaterThanActual: the section claims more records than could
	// actually be parsed from its bytes.
	DeclaredGreaterThanActual
	// DeclaredLessThanActual: the section contains more parseable records
	// than it declares.
	DeclaredLessThanActual
)

func (d MismatchDirection) String() string {
	switch d {
	case DeclaredGreaterThanActual:
		return "declared_greater_than_actual"
	case DeclaredLessThanActual:
		return "declared_less_than_actual"
	default:
		return "none"
	}
}

// Diagnostic is one recovery anomaly. Diagnostics are emitted in a
// deterministic order: by category priority, then by canonical section
// order, then by record index.
type Diagnostic struct {
	Category DiagCategory
	// Section is the zero value (0) only for file-level structural failures.
	Section SectionType
	// RecordIndex is the index within the section of the offending record,
	// or -1 when not applicable (file level, section header, count checks).
	RecordIndex int
	// Direction is set only for CatCountMismatch.
	Direction MismatchDirection
	// Declared/Actual are set only for CatCountMismatch.
	Declared int
	Actual   int
	// MissingRefs lists the referenced object/link ids that could not be
	// found, for CatCrossRefMissing and CatActionDepsMissing.
	MissingRefs []string
	// Detail carries the underlying parse/integrity error for CatStructural.
	Detail string
}

func (d Diagnostic) String() string {
	switch d.Category {
	case CatStructural:
		if d.Section == 0 {
			return fmt.Sprintf("file: structural corruption: %s", d.Detail)
		}
		if d.RecordIndex < 0 {
			return fmt.Sprintf("%s: structural corruption: %s", d.Section, d.Detail)
		}
		return fmt.Sprintf("%s: structural corruption at record %d: %s", d.Section, d.RecordIndex, d.Detail)
	case CatCountMismatch:
		return fmt.Sprintf("%s: declared %d records, actual %d (%s)",
			d.Section, d.Declared, d.Actual, d.Direction)
	case CatCrossRefMissing:
		return fmt.Sprintf("%s: record %d references missing %v", d.Section, d.RecordIndex, d.MissingRefs)
	case CatActionDepsMissing:
		return fmt.Sprintf("%s: record %d dropped, missing dependencies %v", d.Section, d.RecordIndex, d.MissingRefs)
	default:
		return d.Category.String()
	}
}
