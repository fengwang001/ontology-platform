// Package zone holds row-group level statistics (min, max, null count)
// and the zone-map predicates used to skip groups that cannot match.
// It has no dependencies on the other project packages.
package zone

// Op identifies a comparison operator.
type Op uint8

// Supported comparison operators.
const (
	OpEq Op = iota + 1
	OpLt
	OpLe
	OpGt
	OpGe
	OpIn
	OpNull
	OpNotNull
)

// Kind selects the physical type a predicate compares against.
type Kind uint8

// Physical value kinds.
const (
	KindInt Kind = iota + 1
	KindBytes
)

// Term is one atomic predicate. Ints holds the comparison value(s):
// one value for the binary comparisons, any number for OpIn, and none
// for OpNull/OpNotNull. Bytes/BytesList serve the same roles for strings.
type Term struct {
	Op        Op
	Kind      Kind
	Ints      []int64
	Bytes     [][]byte
}

// Predicate is a conjunction (AND) of terms; an empty predicate matches
// every non-null-excluded group, i.e. it never prunes.
type Predicate struct {
	Terms []Term
}

// Eq builds an equality term.
func Eq(v int64) Term { return Term{Op: OpEq, Kind: KindInt, Ints: []int64{v}} }

// Lt builds a less-than term.
func Lt(v int64) Term { return Term{Op: OpLt, Kind: KindInt, Ints: []int64{v}} }

// Le builds a less-than-or-equal term.
func Le(v int64) Term { return Term{Op: OpLe, Kind: KindInt, Ints: []int64{v}} }

// Gt builds a greater-than term.
func Gt(v int64) Term { return Term{Op: OpGt, Kind: KindInt, Ints: []int64{v}} }

// Ge builds a greater-than-or-equal term.
func Ge(v int64) Term { return Term{Op: OpGe, Kind: KindInt, Ints: []int64{v}} }

// In builds a set-membership term.
func In(vs ...int64) Term { return Term{Op: OpIn, Kind: KindInt, Ints: append([]int64(nil), vs...)} }

// IsNull builds a nullness term.
func IsNull() Term { return Term{Op: OpNull} }

// IsNotNull builds a not-nullness term.
func IsNotNull() Term { return Term{Op: OpNotNull} }

// And combines terms with logical AND.
func And(terms ...Term) Predicate { return Predicate{Terms: append([]Term(nil), terms...)} }

// EqBytes builds a string equality term.
func EqBytes(v []byte) Term { return Term{Op: OpEq, Kind: KindBytes, Bytes: [][]byte{append([]byte(nil), v...)}} }

// InBytes builds a string set-membership term.
func InBytes(vs ...[]byte) Term {
	t := Term{Op: OpIn, Kind: KindBytes}
	for _, v := range vs {
		t.Bytes = append(t.Bytes, append([]byte(nil), v...))
	}
	return t
}
