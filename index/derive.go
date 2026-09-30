package index

import (
	"fmt"
	"sort"
)

// constraint 是单列上所有条件取交后的归一化形式。
type constraint struct {
	empty bool // 交集为空（矛盾）
	any   bool // 无条件

	isPoint bool
	point   Value // isPoint 时有效；Kind 可为 KindNull（来自 IS NULL）

	isSet bool
	set   []Value // 升序去重，长度 >= 2（长度为 1 时归约为 point）

	isRange bool
	lo      Value
	loIncl  bool
	hasLo   bool
	hi      Value
	hiIncl  bool
	hasHi   bool
}

// validate 整体校验查询：未知列、类型不符、空集合、L 非正。
func (ix *Index) validate(q Query, limit int) error {
	if limit <= 0 {
		return fmt.Errorf("%w: got %d", ErrInvalidLimit, limit)
	}
	for _, c := range q.Conds {
		colIdx, ok := ix.colIdx[c.Column]
		if !ok {
			return unknownColumnErr(c.Column)
		}
		col := ix.cols[colIdx]
		check := func(v Value) error {
			if v.Kind != col.Type { // 空值只能被 IS NULL 匹配，不能作比较值
				return typeMismatchErr(col, v)
			}
			return nil
		}
		switch c.Op {
		case OpEq, OpLt, OpLe, OpGt, OpGe:
			if len(c.Values) != 1 {
				return fmt.Errorf("index: op %s expects exactly 1 value, got %d", c.Op, len(c.Values))
			}
			if err := check(c.Values[0]); err != nil {
				return err
			}
		case OpIn:
			if len(c.Values) == 0 {
				return fmt.Errorf("%w: column %q", ErrEmptySet, c.Column)
			}
			for _, v := range c.Values {
				if err := check(v); err != nil {
					return err
				}
			}
		case OpIsNull:
			if len(c.Values) != 0 {
				return fmt.Errorf("index: IS NULL takes no value, got %d", len(c.Values))
			}
		default:
			return fmt.Errorf("index: unsupported op %d", int(c.Op))
		}
	}
	return nil
}

func dedupSort(vs []Value) []Value {
	out := append([]Value(nil), vs...)
	sort.Slice(out, func(i, j int) bool { return compare(out[i], out[j]) < 0 })
	n := 0
	for _, v := range out {
		if n == 0 || compare(out[n-1], v) != 0 {
			out[n] = v
			n++
		}
	}
	return out[:n]
}

func memberOf(v Value, set []Value) bool {
	i := sort.Search(len(set), func(i int) bool { return compare(set[i], v) >= 0 })
	return i < len(set) && compare(set[i], v) == 0
}

// inRange 报告非空值 v 是否落在范围边界内；边界值本身永不为空。
func inRange(v Value, lo Value, hasLo, loIncl bool, hi Value, hasHi, hiIncl bool) bool {
	if hasLo {
		c := compare(v, lo)
		if c < 0 || (c == 0 && !loIncl) {
			return false
		}
	}
	if hasHi {
		c := compare(v, hi)
		if c > 0 || (c == 0 && !hiIncl) {
			return false
		}
	}
	return true
}

// intersectConds 把单列上的所有条件取交，归一化为 constraint。
func intersectConds(conds []Cond) constraint {
	if len(conds) == 0 {
		return constraint{any: true}
	}
	var (
		nullOnly       bool
		hasSet         bool
		set            []Value
		lo, hi         Value
		loIncl, hiIncl = true, true
		hasLo, hasHi   bool
	)
	intersectSet := func(vs []Value) bool { // 返回是否矛盾
		if !hasSet {
			hasSet, set = true, dedupSort(vs)
		} else {
			keep := dedupSort(vs)
			n := 0
			for _, v := range set {
				if memberOf(v, keep) {
					set[n] = v
					n++
				}
			}
			set = set[:n]
		}
		return len(set) == 0
	}
	for _, c := range conds {
		switch c.Op {
		case OpIsNull:
			nullOnly = true
		case OpEq:
			if intersectSet([]Value{c.Values[0]}) {
				return constraint{empty: true}
			}
		case OpIn:
			if intersectSet(c.Values) {
				return constraint{empty: true}
			}
		case OpLt, OpLe:
			v, incl := c.Values[0], c.Op == OpLe
			if !hasHi || compare(v, hi) < 0 || (compare(v, hi) == 0 && !incl) {
				hi, hiIncl, hasHi = v, incl, true
			}
		case OpGt, OpGe:
			v, incl := c.Values[0], c.Op == OpGe
			if !hasLo || compare(v, lo) > 0 || (compare(v, lo) == 0 && !incl) {
				lo, loIncl, hasLo = v, incl, true
			}
		}
	}
	// 比较条件对空值永不成立：IS NULL 与任何比较/集合条件相交即矛盾。
	if nullOnly {
		if hasSet || hasLo || hasHi {
			return constraint{empty: true}
		}
		return constraint{isPoint: true, point: Null()}
	}
	// 集合与范围取交后仍为集合。
	if hasSet && (hasLo || hasHi) {
		n := 0
		for _, v := range set {
			if inRange(v, lo, hasLo, loIncl, hi, hasHi, hiIncl) {
				set[n] = v
				n++
			}
		}
		set = set[:n]
		if len(set) == 0 {
			return constraint{empty: true}
		}
	}
	if hasSet {
		if len(set) == 1 { // 取交后只剩单点视为等值
			return constraint{isPoint: true, point: set[0]}
		}
		return constraint{isSet: true, set: set}
	}
	if hasLo || hasHi {
		if hasLo && hasHi {
			c := compare(lo, hi)
			if c > 0 || (c == 0 && !(loIncl && hiIncl)) {
				return constraint{empty: true}
			}
			if c == 0 { // 范围收敛为单点，视为等值
				return constraint{isPoint: true, point: lo}
			}
		}
		return constraint{isRange: true, lo: lo, loIncl: loIncl, hasLo: hasLo, hi: hi, hiIncl: hiIncl, hasHi: hasHi}
	}
	return constraint{any: true}
}

// comparePrefix 只比较 key 的前 len(bound) 列（bound 长度 <= key 长度）。
func comparePrefix(key, bound []Value) int {
	for i := 0; i < len(bound); i++ {
		if c := compare(key[i], bound[i]); c != 0 {
			return c
		}
	}
	return 0
}

// belowLo / aboveHi 判定 key 是否落在区间边界之外。
func belowLo(key []Value, b Bound) bool {
	if b.Unbounded {
		return false
	}
	c := comparePrefix(key, b.Key)
	return c < 0 || (c == 0 && !b.Inclusive)
}

func aboveHi(key []Value, b Bound) bool {
	if b.Unbounded {
		return false
	}
	c := comparePrefix(key, b.Key)
	return c > 0 || (c == 0 && !b.Inclusive)
}

func prefixBound(combo []Value) Bound {
	if len(combo) == 0 {
		return Bound{Unbounded: true}
	}
	return Bound{Key: append([]Value(nil), combo...), Inclusive: true}
}

// Derive 把查询推导为最少的键区间与残余过滤条件。
// limit 是前缀组合数上限 L，必须为正。
func (ix *Index) Derive(q Query, limit int) (Plan, error) {
	if err := ix.validate(q, limit); err != nil {
		return Plan{}, err
	}
	byCol := make([][]Cond, len(ix.cols))
	for _, c := range q.Conds {
		i := ix.colIdx[c.Column]
		byCol[i] = append(byCol[i], c)
	}
	cons := make([]constraint, len(ix.cols))
	for i := range ix.cols {
		cons[i] = intersectConds(byCol[i])
		if cons[i].empty { // 矛盾条件：空区间集合，考察零条
			return Plan{Empty: true}, nil
		}
	}

	consumed := make([]bool, len(ix.cols))
	combos := [][]Value{{}}
	var intervals []Interval

	closePrefix := func() { // 每个前缀组合闭合成覆盖其全部后缀的区间
		for _, combo := range combos {
			intervals = append(intervals, Interval{Lo: prefixBound(combo), Hi: prefixBound(combo)})
		}
	}

loop:
	for i := 0; i < len(ix.cols); i++ {
		c := cons[i]
		switch {
		case c.any:
			closePrefix()
			break loop
		case c.isPoint || c.isSet:
			vals := c.set
			if c.isPoint {
				vals = []Value{c.point}
			}
			if len(combos)*len(vals) > limit {
				// 组合数首次超限：从该列起改作残余过滤
				closePrefix()
				break loop
			}
			next := make([][]Value, 0, len(combos)*len(vals))
			for _, combo := range combos {
				for _, v := range vals {
					next = append(next, append(append([]Value(nil), combo...), v))
				}
			}
			combos = next
			consumed[i] = true
		case c.isRange:
			for _, combo := range combos {
				lo, hi := prefixBound(combo), prefixBound(combo)
				if c.hasLo {
					lo = Bound{Key: append(append([]Value(nil), combo...), c.lo), Inclusive: c.loIncl}
				} else {
					// 比较条件对空值永不成立：下界排除本列空值（空值排在最前）。
					lo = Bound{Key: append(append([]Value(nil), combo...), Null()), Inclusive: false}
				}
				if c.hasHi {
					hi = Bound{Key: append(append([]Value(nil), combo...), c.hi), Inclusive: c.hiIncl}
				}
				intervals = append(intervals, Interval{Lo: lo, Hi: hi})
			}
			consumed[i] = true
			break loop
		}
		if i == len(ix.cols)-1 {
			// 所有列都是等值/集合：每个组合是一个点区间。
			for _, combo := range combos {
				b := Bound{Key: combo, Inclusive: true}
				intervals = append(intervals, Interval{Lo: b, Hi: b})
			}
		}
	}
	var residual []Cond
	for _, c := range q.Conds {
		if !consumed[ix.colIdx[c.Column]] {
			residual = append(residual, c)
		}
	}
	return Plan{Intervals: intervals, Residual: residual}, nil
}
