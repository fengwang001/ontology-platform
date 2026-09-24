// Package union 维护按起点有序的最大不相交区间集合，
// 支持 Add/Withdraw 并产出变更日志。依赖 seg。
package union

import (
	"errors"
	"slices"
	"sort"

	"ontology/seg"
)

// 可判定的哨兵错误，三者互不相同。
var (
	ErrInvalidInterval  = errors.New("union: invalid interval (s >= e)")
	ErrTooManyIntervals = errors.New("union: interval count exceeds max")
	ErrNotFound         = errors.New("union: withdraw matches no interval")
)

// Change 是一条变更日志：Del 为 true 表示 -[S,E)，否则 +[S,E)。
type Change struct {
	Del  bool
	S, E int64
}

// Union 是按起点有序的最大不相交区间集合。
type Union struct {
	segs []seg.Interval
	max  int
	log  []Change
	// scanned 记录最近一次 Add/Withdraw 为定位接触/重叠区间
	// 而线性检查的区间个数（二分定位不计）。非导出，不进公开接口。
	scanned int
}

// New 返回空并集，max 为段数上限（仅约束 Add）。
func New(max int) *Union { return &Union{max: max} }

// locate 二分找到首个 E >= s（严格重叠时用 E > s）的下标，
// 再局部前向扫描 [i,j) 为满足 cond 的区间，扫描的元素个数计入 scanned。
func (u *Union) locate(s, e int64, strict bool, cond func(seg.Interval) bool) (i, j int) {
	i = sort.Search(len(u.segs), func(k int) bool {
		if strict {
			return u.segs[k].E > s
		}
		return u.segs[k].E >= s
	})
	j = i
	u.scanned = 0
	for j < len(u.segs) {
		u.scanned++
		if !cond(u.segs[j]) {
			break
		}
		j++
	}
	return i, j
}

// Add 把 [s,e) 并入并集，返回变更日志。
func (u *Union) Add(s, e int64) ([]Change, error) {
	if !seg.Valid(s, e) {
		return nil, ErrInvalidInterval
	}
	i, j := u.locate(s, e, false, func(v seg.Interval) bool { return seg.Touches(v, s, e) })
	if i == j { // 无接触区间
		if len(u.segs)+1 > u.max {
			return nil, ErrTooManyIntervals
		}
		return u.commit(i, j, []seg.Interval{{S: s, E: e}}, []Change{{S: s, E: e}}), nil
	}
	m := seg.Interval{S: s, E: e}
	for k := i; k < j; k++ {
		m = seg.Merge(m, u.segs[k].S, u.segs[k].E)
	}
	if j-i == 1 && m == u.segs[i] { // 完全落在现有区间内部，并集不变
		return nil, nil
	}
	if len(u.segs)-(j-i)+1 > u.max {
		return nil, ErrTooManyIntervals
	}
	var ch []Change
	for k := i; k < j; k++ {
		ch = append(ch, Change{Del: true, S: u.segs[k].S, E: u.segs[k].E})
	}
	ch = append(ch, Change{S: m.S, E: m.E})
	return u.commit(i, j, []seg.Interval{m}, ch), nil
}

// Withdraw 从并集中撤掉 [s,e)，返回变更日志。
func (u *Union) Withdraw(s, e int64) ([]Change, error) {
	if !seg.Valid(s, e) {
		return nil, ErrInvalidInterval
	}
	i, j := u.locate(s, e, true, func(v seg.Interval) bool { return seg.Overlaps(v, s, e) })
	if i == j {
		return nil, ErrNotFound
	}
	var ch []Change
	var repl []seg.Interval
	for k := i; k < j; k++ {
		v := u.segs[k]
		ch = append(ch, Change{Del: true, S: v.S, E: v.E})
		l, r, hl, hr := seg.Split(v, s, e)
		if hl {
			ch = append(ch, Change{S: l.S, E: l.E})
			repl = append(repl, l)
		}
		if hr {
			ch = append(ch, Change{S: r.S, E: r.E})
			repl = append(repl, r)
		}
	}
	return u.commit(i, j, repl, ch), nil
}

// commit 用 repl 替换 segs[i:j) 并把 ch 追加到日志。只在成功路径调用。
func (u *Union) commit(i, j int, repl []seg.Interval, ch []Change) []Change {
	next := make([]seg.Interval, 0, len(u.segs)-(j-i)+len(repl))
	next = append(next, u.segs[:i]...)
	next = append(next, repl...)
	next = append(next, u.segs[j:]...)
	u.segs = next
	u.log = append(u.log, ch...)
	return ch
}

// View 返回当前并集分段的副本。
func (u *Union) View() []seg.Interval {
	return append([]seg.Interval(nil), u.segs...)
}

// Log 返回已输出变更日志的副本。
func (u *Union) Log() []Change {
	return append([]Change(nil), u.log...)
}

// ApplyChange 把一条变更日志应用到影子分段 sh（下游视角）：
// '-' 必须精确命中现有段（端点完全相同），否则报告 false；
// '+' 按起点有序插入。供自检与下游校验变更日志自洽性。
func ApplyChange(sh []seg.Interval, c Change) ([]seg.Interval, bool) {
	if c.Del {
		if i := slices.Index(sh, seg.Interval{S: c.S, E: c.E}); i >= 0 {
			return slices.Delete(sh, i, i+1), true
		}
		return sh, false
	}
	i := sort.Search(len(sh), func(k int) bool { return sh[k].S >= c.S })
	return slices.Insert(sh, i, seg.Interval{S: c.S, E: c.E}), true
}
