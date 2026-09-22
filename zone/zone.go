// Package zone holds row-group statistics (min/max/null count) and the
// predicate pruning decision used to skip row groups during scans.
package zone

// Kind identifies a predicate operator.
type Kind int

// Predicate operators supported by the push-down scanner.
const (
	KindInvalid Kind = iota
	KindEq
	KindLt
	KindLe
	KindGt
	KindGe
	KindIn
	KindNull
	KindNotNull
	KindAnd
)

// ValueType tags which typed comparison a predicate carries.
type ValueType int

// Supported comparison value types.
const (
	TypeNone ValueType = iota
	TypeInt
	TypeStr
)

// Stats summarizes a row group. HasMin is false for groups containing no
// non-null values, so nulls can never be confused with the zero value.
type Stats struct {
	HasMin    bool
	MinInt    int64
	MaxInt    int64
	MinStr    string
	MaxStr    string
	NullCount int
	RowCount  int
	ValueType ValueType
}

// Predicate is a scalar or conjunctive predicate.
type Predicate struct {
	kind Kind
}

// Kind returns the predicate operator.
func (p *Predicate) Kind() Kind { return KindInvalid }

// CanSkip reports whether every row in a group with these statistics is
// guaranteed not to match. It never returns true for a group that could
// contain a matching row.
func (s *Stats) CanSkip(p *Predicate) bool { return false }
