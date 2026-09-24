package zone

const (
	KindInt64 = iota + 1
	KindString
)

const (
	OpEq = iota + 1
	OpLT
	OpLE
	OpGT
	OpGE
	OpIn
	OpNull
	OpNotNull
	OpAnd
)

type Int64Stats struct {
	Rows, Nulls int
	Min, Max    int64
	Has         bool
}

type StringStats struct {
	Rows, Nulls int
	Min, Max    string
	Has         bool
}

type Predicate struct {
	Kind int
	Op   int
	Ints []int64
	Strs []string
	Kids []*Predicate
}

func EqInt64(v int64) *Predicate       { return &Predicate{Kind: KindInt64, Op: OpEq, Ints: []int64{v}} }
func LTInt64(v int64) *Predicate       { return &Predicate{Kind: KindInt64, Op: OpLT, Ints: []int64{v}} }
func LEInt64(v int64) *Predicate       { return &Predicate{Kind: KindInt64, Op: OpLE, Ints: []int64{v}} }
func GTInt64(v int64) *Predicate       { return &Predicate{Kind: KindInt64, Op: OpGT, Ints: []int64{v}} }
func GEInt64(v int64) *Predicate       { return &Predicate{Kind: KindInt64, Op: OpGE, Ints: []int64{v}} }
func InInt64(vs ...int64) *Predicate   { return &Predicate{Kind: KindInt64, Op: OpIn, Ints: vs} }
func EqString(v string) *Predicate     { return &Predicate{Kind: KindString, Op: OpEq, Strs: []string{v}} }
func InString(vs ...string) *Predicate { return &Predicate{Kind: KindString, Op: OpIn, Strs: vs} }
func IsNull(kind int) *Predicate       { return &Predicate{Kind: kind, Op: OpNull} }
func IsNotNull(kind int) *Predicate    { return &Predicate{Kind: kind, Op: OpNotNull} }

func And(kids ...*Predicate) *Predicate {
	kind := KindInt64
	if len(kids) > 0 {
		kind = kids[0].Kind
	}
	return &Predicate{Kind: kind, Op: OpAnd, Kids: kids}
}

func (p *Predicate) KeepInt64(s Int64Stats) bool {
	switch p.Op {
	case OpEq:
		return s.Has && s.Min <= p.Ints[0] && p.Ints[0] <= s.Max
	case OpLT:
		return s.Has && s.Min < p.Ints[0]
	case OpLE:
		return s.Has && s.Min <= p.Ints[0]
	case OpGT:
		return s.Has && s.Max > p.Ints[0]
	case OpGE:
		return s.Has && s.Max >= p.Ints[0]
	case OpIn:
		for _, v := range p.Ints {
			if s.Has && s.Min <= v && v <= s.Max {
				return true
			}
		}
		return false
	case OpNull:
		return s.Nulls > 0
	case OpNotNull:
		return s.Rows-s.Nulls > 0
	case OpAnd:
		for _, kid := range p.Kids {
			if !kid.KeepInt64(s) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func (p *Predicate) MatchInt64(v int64) bool {
	switch p.Op {
	case OpEq:
		return v == p.Ints[0]
	case OpLT:
		return v < p.Ints[0]
	case OpLE:
		return v <= p.Ints[0]
	case OpGT:
		return v > p.Ints[0]
	case OpGE:
		return v >= p.Ints[0]
	case OpIn:
		for _, want := range p.Ints {
			if v == want {
				return true
			}
		}
	case OpAnd:
		for _, kid := range p.Kids {
			if !kid.MatchInt64(v) {
				return false
			}
		}
		return true
	}
	return false
}

func (p *Predicate) KeepString(s StringStats) bool {
	switch p.Op {
	case OpEq:
		return s.Has && s.Min <= p.Strs[0] && p.Strs[0] <= s.Max
	case OpIn:
		for _, v := range p.Strs {
			if s.Has && s.Min <= v && v <= s.Max {
				return true
			}
		}
	case OpNull:
		return s.Nulls > 0
	case OpNotNull:
		return s.Rows-s.Nulls > 0
	case OpAnd:
		for _, kid := range p.Kids {
			if !kid.KeepString(s) {
				return false
			}
		}
		return true
	}
	return false
}

func (p *Predicate) MatchString(v string) bool {
	switch p.Op {
	case OpEq:
		return v == p.Strs[0]
	case OpIn:
		for _, want := range p.Strs {
			if v == want {
				return true
			}
		}
	case OpAnd:
		for _, kid := range p.Kids {
			if !kid.MatchString(v) {
				return false
			}
		}
		return true
	}
	return false
}
