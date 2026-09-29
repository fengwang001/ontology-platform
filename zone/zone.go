// Package zone holds row-group statistics (min/max/null count) and the
// predicates used for zone-map pruning. It depends on no other package.
package zone

// Kind tags a cell value so that NULL, the integer 0 and the empty string are
// three independently decidable read results.
type Kind uint8

const (
	Null Kind = iota
	Int
	Str
)

// Val is a tagged cell value.
type Val struct {
	Kind Kind
	I    int64
	S    string
}

// NullVal, IntVal and StrVal construct the three cell states.
func NullVal() Val                  { return Val{Kind: Null} }
func IntVal(v int64) Val            { return Val{Kind: Int, I: v} }
func StrVal(s string) Val           { return Val{Kind: Str, S: s} }
func (v Val) IsNull() bool          { return v.Kind == Null }
func (v Val) AsInt() (int64, bool)  { return v.I, v.Kind == Int }
func (v Val) AsStr() (string, bool) { return v.S, v.Kind == Str }

// Stat is a row-group zone map for an integer column.
type Stat struct {
	HasMinMax bool
	Min       int64
	Max       int64
	NullCount int
	Rows      int
}

// Build computes statistics over a tagged column slice. NULLs never enter
// min/max; an all-null group has HasMinMax == false ("none", never zero).
func Build(vals []Val) Stat {
	s := Stat{Rows: len(vals)}
	for _, v := range vals {
		switch v.Kind {
		case Null:
			s.NullCount++
		case Int:
			if !s.HasMinMax {
				s.Min, s.Max, s.HasMinMax = v.I, v.I, true
			} else {
				if v.I < s.Min {
					s.Min = v.I
				}
				if v.I > s.Max {
					s.Max = v.I
				}
			}
		}
	}
	return s
}

// NonNull reports whether the group contains at least one value.
func (s Stat) NonNull() bool { return s.Rows-s.NullCount > 0 }

// Op enumerates the supported scalar comparisons.
type Op uint8

const (
	OpEq Op = iota
	OpLt
	OpLe
	OpGt
	OpGe
	OpIn
	OpIsNull
	OpIsNotNull
)

// Predicate is one push-down term. List is used only by OpIn.
type Predicate struct {
	Op   Op
	Val  int64
	List []int64
}

// Eq, Lt, Le, Gt, Gt, In, IsNull and IsNotNull are constructors.
func Eq(x int64) Predicate { return Predicate{Op: OpEq, Val: x} }
func Lt(x int64) Predicate { return Predicate{Op: OpLt, Val: x} }
func Le(x int64) Predicate { return Predicate{Op: OpLe, Val: x} }
func Gt(x int64) Predicate { return Predicate{Op: OpGt, Val: x} }
func Ge(x int64) Predicate { return Predicate{Op: OpGe, Val: x} }
func In(xs []int64) Predicate {
	return Predicate{Op: OpIn, List: xs}
}
func IsNull() Predicate    { return Predicate{Op: OpIsNull} }
func IsNotNull() Predicate { return Predicate{Op: OpIsNotNull} }

// And combines terms with logical AND.
type Filter struct{ Terms []Predicate }

// All matches everything; And combines terms.
func All() Filter { return Filter{} }

// And returns a filter that requires every term to hold.
func And(ps ...Predicate) Filter { return Filter{Terms: ps} }

// CouldHit implements the pruning decision: true means the group MAY contain
// matches and must be decoded; false proves it contains none. It never falsely
// excludes a group because every false branch implies zero matching rows.
func (p Predicate) CouldHit(s Stat) bool {
	switch p.Op {
	case OpEq:
		return s.HasMinMax && s.Min <= p.Val && p.Val <= s.Max
	case OpLt:
		return s.HasMinMax && s.Min < p.Val
	case OpLe:
		return s.HasMinMax && s.Min <= p.Val
	case OpGt:
		return s.HasMinMax && s.Max > p.Val
	case OpGe:
		return s.HasMinMax && s.Max >= p.Val
	case OpIn:
		if !s.HasMinMax {
			return false
		}
		for _, x := range p.List {
			if s.Min <= x && x <= s.Max {
				return true
			}
		}
		return false
	case OpIsNull:
		return s.NullCount > 0
	case OpIsNotNull:
		return s.NonNull()
	}
	return true
}

// CouldHit is the AND combination: a group is kept only if every term keeps it.
func (f Filter) CouldHit(s Stat) bool {
	for _, p := range f.Terms {
		if !p.CouldHit(s) {
			return false
		}
	}
	return true
}

// Match evaluates the filter against one tagged value. NULL yields neither
// true nor false for every numeric term; only IsNull matches NULL.
func (f Filter) Match(v Val) bool {
	if v.Kind != Int {
		for _, p := range f.Terms {
			switch p.Op {
			case OpIsNull:
				if v.Kind != Null {
					return false
				}
			case OpIsNotNull:
				if v.Kind == Null {
					return false
				}
			default:
				if v.Kind == Null {
					return false
				}
				return false // non-int, non-null cannot satisfy a numeric term
			}
		}
		return true
	}
	x := v.I
	for _, p := range f.Terms {
		switch p.Op {
		case OpEq:
			if !(x == p.Val) {
				return false
			}
		case OpLt:
			if !(x < p.Val) {
				return false
			}
		case OpLe:
			if !(x <= p.Val) {
				return false
			}
		case OpGt:
			if !(x > p.Val) {
				return false
			}
		case OpGe:
			if !(x >= p.Val) {
				return false
			}
		case OpIn:
			found := false
			for _, y := range p.List {
				if x == y {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		case OpIsNull:
			return false
		case OpIsNotNull:
			// always true for an int
		}
	}
	return true
}
