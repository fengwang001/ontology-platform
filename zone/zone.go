// Package zone holds row-group min/max/null statistics and the conservative
// predicate-pruning decision. It depends on no other package.
package zone

// Kind discriminates the three readable states: NULL, integer, string.
type Kind uint8

const (
	KindNull Kind = iota
	KindInt
	KindStr
)

// Value is a tri-state cell: KindNull, an int64 or a string (which may be "").
type Value struct {
	Kind Kind
	I    int64
	S    string
}

// Null returns a NULL value distinct from both 0 and "".
func Null() Value { return Value{Kind: KindNull} }

// Int returns an integer value.
func Int(v int64) Value { return Value{Kind: KindInt, I: v} }

// Str returns a string value; "" is present, not NULL.
func Str(v string) Value { return Value{Kind: KindStr, S: v} }

// Stats summarizes one row group. NULLs never enter min/max.
type Stats struct {
	N         int
	NullCount int
	HasMinMax bool // false for present-count == 0
	IsString  bool
	MinI      int64
	MaxI      int64
	MinS      string
	MaxS      string
}

// Build computes statistics. Only present values update min/max.
func Build(vals []Value) Stats {
	s := Stats{N: len(vals)}
	for _, v := range vals {
		if v.Kind == KindNull {
			s.NullCount++
			continue
		}
		switch v.Kind {
		case KindInt:
			if !s.HasMinMax || s.IsString {
				s.HasMinMax, s.IsString = true, false
				s.MinI, s.MaxI = v.I, v.I
			} else if v.I < s.MinI {
				s.MinI = v.I
			} else if v.I > s.MaxI {
				s.MaxI = v.I
			}
		case KindStr:
			if !s.HasMinMax || !s.IsString {
				s.HasMinMax, s.IsString = true, true
				s.MinS, s.MaxS = v.S, v.S
			} else if v.S < s.MinS {
				s.MinS = v.S
			} else if v.S > s.MaxS {
				s.MaxS = v.S
			}
		}
	}
	return s
}

// Op identifies an atomic predicate.
type Op uint8

const (
	OpEq Op = iota
	OpLt
	OpLe
	OpGt
	OpGe
	OpIn
	OpIsNull
	OpNotNull
)

// Cond is one atomic predicate. A Filter is the AND of its Conds.
type Cond struct {
	Op    Op
	IsStr bool
	I     int64
	S     string
	InI   []int64
	InS   []string
}

// Filter is a conjunction of conditions.
type Filter []Cond

func atom(s Stats, c Cond) bool {
	switch c.Op {
	case OpIsNull:
		return s.NullCount > 0
	case OpNotNull:
		return s.N-s.NullCount > 0
	}
	if s.NullCount == s.N || !s.HasMinMax {
		return false
	}
	if c.IsStr != s.IsString {
		return false
	}
	if s.IsString {
		return atomStr(s, c)
	}
	return atomInt(s, c)
}

func atomInt(s Stats, c Cond) bool {
	min, max := s.MinI, s.MaxI
	switch c.Op {
	case OpEq:
		return min <= c.I && c.I <= max
	case OpLt:
		return min < c.I
	case OpLe:
		return min <= c.I
	case OpGt:
		return max > c.I
	case OpGe:
		return max >= c.I
	case OpIn:
		for _, x := range c.InI {
			if min <= x && x <= max {
				return true
			}
		}
	}
	return false
}

func atomStr(s Stats, c Cond) bool {
	min, max := s.MinS, s.MaxS
	switch c.Op {
	case OpEq:
		return min <= c.S && c.S <= max
	case OpLt:
		return min < c.S
	case OpLe:
		return min <= c.S
	case OpGt:
		return max > c.S
	case OpGe:
		return max >= c.S
	case OpIn:
		for _, x := range c.InS {
			if min <= x && x <= max {
				return true
			}
		}
	}
	return false
}

// Allows reports whether the group may contain a match. It never rejects a
// group that actually contains one (see DESIGN.md).
func (s Stats) Allows(f Filter) bool {
	for _, c := range f {
		if !atom(s, c) {
			return false
		}
	}
	return true
}

// Match evaluates one value under three-valued logic: NULL satisfies no
// numeric predicate; only IS NULL matches it.
func Match(v Value, f Filter) bool {
	for _, c := range f {
		if !matchOne(v, c) {
			return false
		}
	}
	return true
}

func matchOne(v Value, c Cond) bool {
	if c.Op == OpIsNull {
		return v.Kind == KindNull
	}
	if c.Op == OpNotNull {
		return v.Kind != KindNull
	}
	if v.Kind == KindNull {
		return false
	}
	if c.IsStr {
		return matchStr(v, c)
	}
	return matchInt(v, c)
}

func matchInt(v Value, c Cond) bool {
	if v.Kind != KindInt {
		return false
	}
	switch c.Op {
	case OpEq:
		return v.I == c.I
	case OpLt:
		return v.I < c.I
	case OpLe:
		return v.I <= c.I
	case OpGt:
		return v.I > c.I
	case OpGe:
		return v.I >= c.I
	case OpIn:
		for _, x := range c.InI {
			if v.I == x {
				return true
			}
		}
	}
	return false
}

func matchStr(v Value, c Cond) bool {
	if v.Kind != KindStr {
		return false
	}
	switch c.Op {
	case OpEq:
		return v.S == c.S
	case OpLt:
		return v.S < c.S
	case OpLe:
		return v.S <= c.S
	case OpGt:
		return v.S > c.S
	case OpGe:
		return v.S >= c.S
	case OpIn:
		for _, x := range c.InS {
			if v.S == x {
				return true
			}
		}
	}
	return false
}
