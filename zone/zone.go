// Package zone holds row-group statistics (min/max/null count) and the
// predicate-pruning decision used to skip whole row groups.
package zone

// Kind identifies the physical value type of a row group.
type Kind uint8

const (
	KindInt64 Kind = iota
	KindString
)

// Value is the tri-state read result: Null, Int64, or String. Null, zero and
// empty string are mutually distinguishable.
type Value struct {
	Null bool
	Kind Kind
	Int  int64
	Str  string
}

// IntValue builds a non-null int64 value.
func IntValue(v int64) Value { return Value{Kind: KindInt64, Int: v} }

// StrValue builds a non-null string value.
func StrValue(s string) Value { return Value{Kind: KindString, Str: s} }

// NullValue builds a null value.
func NullValue() Value { return Value{Null: true} }

// Equals compares two values; null equals only null.
func (v Value) Equals(o Value) bool {
	if v.Null || o.Null {
		return v.Null && o.Null
	}
	if v.Kind != o.Kind {
		return false
	}
	if v.Kind == KindInt64 {
		return v.Int == o.Int
	}
	return v.Str == o.Str
}

// Stats are the min/max/null statistics of one row group.
type Stats struct {
	Kind   Kind
	Rows   int
	Nulls  int
	Has    bool
	MinInt int64
	MaxInt int64
	MinStr string
	MaxStr string
}

// Add folds one non-null value into the statistics.
func (s *Stats) Add(v Value) {
	if v.Null {
		return
	}
	if !s.Has {
		s.Kind = v.Kind
		s.Has = true
		if v.Kind == KindInt64 {
			s.MinInt, s.MaxInt = v.Int, v.Int
		} else {
			s.MinStr, s.MaxStr = v.Str, v.Str
		}
		return
	}
	if v.Kind == KindInt64 && s.Kind == KindInt64 {
		if v.Int < s.MinInt {
			s.MinInt = v.Int
		}
		if v.Int > s.MaxInt {
			s.MaxInt = v.Int
		}
	}
	if v.Kind == KindString && s.Kind == KindString {
		if v.Str < s.MinStr {
			s.MinStr = v.Str
		}
		if v.Str > s.MaxStr {
			s.MaxStr = v.Str
		}
	}
}

// NonNull is the number of present values.
func (s *Stats) NonNull() int { return s.Rows - s.Nulls }

// Op enumerates supported leaf predicates.
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

// Predicate is one leaf predicate. In is used only for OpIn.
type Predicate struct {
	Op   Op
	Kind Kind
	Int  int64
	Str  string
	InI  []int64
	InS  []string
	And  []Predicate
	Leaf bool
}

// IntLeaf builds an int64 comparison predicate.
func IntLeaf(op Op, v int64) Predicate { return Predicate{Op: op, Kind: KindInt64, Int: v, Leaf: true} }

// StrLeaf builds a string comparison predicate.
func StrLeaf(op Op, v string) Predicate {
	return Predicate{Op: op, Kind: KindString, Str: v, Leaf: true}
}

// IntIn builds an IN predicate over int64.
func IntIn(vs ...int64) Predicate { return Predicate{Op: OpIn, Kind: KindInt64, InI: vs, Leaf: true} }

// StrIn builds an IN predicate over strings.
func StrIn(vs ...string) Predicate { return Predicate{Op: OpIn, Kind: KindString, InS: vs, Leaf: true} }

// NullPred builds IS NULL (true) or IS NOT NULL (false).
func NullPred(isNull bool) Predicate {
	if isNull {
		return Predicate{Op: OpIsNull, Leaf: true}
	}
	return Predicate{Op: OpIsNotNull, Leaf: true}
}

// All combines predicates with AND.
func All(ps ...Predicate) Predicate { return Predicate{And: ps} }

// CanSkip reports whether the row group is provably empty for p.
// It may conservatively return false; it never returns true on a possible hit.
func CanSkip(s *Stats, p Predicate) bool {
	if !p.Leaf {
		for _, c := range p.And {
			if CanSkip(s, c) {
				return true
			}
		}
		return false
	}
	switch p.Op {
	case OpIsNull:
		return s.Nulls == 0
	case OpIsNotNull:
		return s.NonNull() == 0
	}
	if !s.Has || s.Kind != p.Kind {
		return true // all-null group cannot match any value predicate
	}
	if p.Kind == KindInt64 {
		switch p.Op {
		case OpEq:
			return p.Int < s.MinInt || p.Int > s.MaxInt
		case OpLt:
			return s.MinInt >= p.Int
		case OpLe:
			return s.MinInt > p.Int
		case OpGt:
			return s.MaxInt <= p.Int
		case OpGe:
			return s.MaxInt < p.Int
		case OpIn:
			for _, v := range p.InI {
				if v >= s.MinInt && v <= s.MaxInt {
					return false
				}
			}
			return true
		}
	}
	switch p.Op {
	case OpEq:
		return p.Str < s.MinStr || p.Str > s.MaxStr
	case OpLt:
		return s.MinStr >= p.Str
	case OpLe:
		return s.MinStr > p.Str
	case OpGt:
		return s.MaxStr <= p.Str
	case OpGe:
		return s.MaxStr < p.Str
	case OpIn:
		for _, v := range p.InS {
			if v >= s.MinStr && v <= s.MaxStr {
				return false
			}
		}
		return true
	}
	return true
}

// Eval applies the predicate to a single value (SQL three-valued logic:
// NULL never satisfies a comparison, only IS NULL does).
func Eval(p Predicate, v Value) bool {
	if !p.Leaf {
		for _, c := range p.And {
			if !Eval(c, v) {
				return false
			}
		}
		return true
	}
	switch p.Op {
	case OpIsNull:
		return v.Null
	case OpIsNotNull:
		return !v.Null
	}
	if v.Null || v.Kind != p.Kind {
		return false
	}
	if v.Kind == KindInt64 {
		switch p.Op {
		case OpEq:
			return v.Int == p.Int
		case OpLt:
			return v.Int < p.Int
		case OpLe:
			return v.Int <= p.Int
		case OpGt:
			return v.Int > p.Int
		case OpGe:
			return v.Int >= p.Int
		case OpIn:
			for _, x := range p.InI {
				if v.Int == x {
					return true
				}
			}
			return false
		}
	}
	switch p.Op {
	case OpEq:
		return v.Str == p.Str
	case OpLt:
		return v.Str < p.Str
	case OpLe:
		return v.Str <= p.Str
	case OpGt:
		return v.Str > p.Str
	case OpGe:
		return v.Str >= p.Str
	case OpIn:
		for _, x := range p.InS {
			if v.Str == x {
				return true
			}
		}
	}
	return false
}
