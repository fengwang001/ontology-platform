// Package aggregation implements a two-phase local-to-global pre-aggregation
// mechanism: the local phase folds each arriving batch of rows into per-group
// partial aggregates; the global phase accepts only partial aggregates and
// merges them commutatively, so any legal batching of the same serial yields
// identical results.
package aggregation

import "fmt"

// Op is the operation type of an arriving row.
type Op int

const (
	// OpAdd appends a row.
	OpAdd Op = iota
	// OpRetract retracts a row, matched exactly by group and value.
	OpRetract
)

// Valid reports whether the operation type is legal.
func (o Op) Valid() bool {
	return o == OpAdd || o == OpRetract
}

// RowOp is one arriving row together with its operation.
type RowOp struct {
	Op    Op
	Group string
	Value float64
}

// PartialAgg is the local-phase partial aggregate of a single group.
// Sum and Count are decomposable; Values keeps value -> net row count
// so the non-decomposable distinct count can be pushed down per value.
type PartialAgg struct {
	Sum    float64
	Count  int64
	Values map[float64]int64
}

// IsZero reports whether the partial aggregate is all-zero and must not
// be sent to the global phase.
func (p PartialAgg) IsZero() bool {
	return p.Sum == 0 && p.Count == 0 && len(p.Values) == 0
}

// Metrics is the set of four indicators exposed by the global phase.
type Metrics struct {
	Sum          float64
	Count        int64
	Avg          float64
	DistinctVals int64
}

// ErrCode distinguishes categories of illegal input; values are unique.
type ErrCode int

const (
	// ErrCodeInvalidOp indicates an illegal operation type.
	ErrCodeInvalidOp ErrCode = iota + 1
	// ErrCodeEmptyGroup indicates an empty group name.
	ErrCodeEmptyGroup
	// ErrCodeRetractMissingRow indicates retracting a row that does not exist.
	ErrCodeRetractMissingRow
	// ErrCodeTooManyGroups indicates the merged group count exceeds the limit.
	ErrCodeTooManyGroups
)

// Error is a distinguishable rejection reason carrying a category code.
type Error struct {
	Code ErrCode
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("aggregation: [%d] %s", e.Code, e.Msg)
}
