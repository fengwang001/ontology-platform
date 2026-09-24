// Package union 维护按起点有序的最大不相交区间集合，支持 Add/Withdraw
// 并产出变更日志。依赖 seg；定位用二分 + 局部前向扫描，不整表扫描。
package union

import (
	"errors"
	"slices"
	"sort"
	"sync"

	"ontology/seg"
)

// Interval 是并集视图中的一段 [S,E)。
type Interval struct{ S, E int64 }

// Change 是一条变更日志：Del=true 表示撤回 -[S,E)，否则为新增 +[S,E)。
type Change struct {
	Del bool
	I   Interval
}

var (
	ErrInvalid  = errors.New("union: invalid interval (s >= e)")
	ErrTooMany  = errors.New("union: interval count exceeds maxIntervals")
	ErrNotFound = errors.New("union: withdraw matches no existing interval")
)

// Set 是按起点有序的最大不相交区间集合。
type Set struct {
	mu      sync.RWMutex
	segs    []seg.Seg
	max     int
	checked int // 最近一次 Add/Withdraw 为定位而线性检查的区间个数（非导出）
}

// New 创建空并集，段数上限 maxIntervals（仅约束 Add）。
func New(maxIntervals int) *Set { return &Set{max: maxIntervals} }

// locate 找到首个终点满足 cond 的下标，随后前向扫描并计数，返回 [i,j) 为命中段。
func (u *Set) locate(cond func(seg.Seg) bool, stop func(seg.Seg) bool) (int, int) {
	i := sort.Search(len(u.segs), func(k int) bool { return cond(u.segs[k]) })
	j := i
	u.checked = 0
	for j < len(u.segs) {
		u.checked++
		if stop(u.segs[j]) {
			break
		}
		j++
	}
	return i, j
}

// Add 把 [s,e) 并入并集，返回变更日志；失败时不改任何状态。
func (u *Set) Add(s, e int64) ([]Change, error) {
	if s >= e {
		return nil, ErrInvalid
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	i, j := u.locate(
		func(g seg.Seg) bool { return g.E >= s },
		func(g seg.Seg) bool { return g.S > e })
	t := u.segs[i:j]
	ns, ne := s, e
	if len(t) > 0 {
		ns, ne = min(s, t[0].S), max(e, t[len(t)-1].E)
	}
	if len(t) == 1 && t[0].S == ns && t[0].E == ne {
		return nil, nil // 完全落在现有区间内部，并集不变
	}
	if len(u.segs)-len(t)+1 > u.max {
		return nil, ErrTooMany
	}
	var out []Change
	for _, g := range t {
		out = append(out, Change{Del: true, I: Interval{g.S, g.E}})
	}
	out = append(out, Change{I: Interval{ns, ne}})
	u.segs = slices.Concat(u.segs[:i:i], []seg.Seg{{S: ns, E: ne}}, u.segs[j:])
	return out, nil
}

// Withdraw 从并集中撤掉 [s,e)，返回变更日志；无严格重叠时报 ErrNotFound。
func (u *Set) Withdraw(s, e int64) ([]Change, error) {
	if s >= e {
		return nil, ErrInvalid
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	i, j := u.locate(
		func(g seg.Seg) bool { return g.E > s },
		func(g seg.Seg) bool { return g.S >= e })
	if i == j {
		return nil, ErrNotFound
	}
	var out []Change
	mid := make([]seg.Seg, 0, 2*(j-i))
	for _, g := range u.segs[i:j] {
		out = append(out, Change{Del: true, I: Interval{g.S, g.E}})
		l, r, hl, hr := seg.Split(g, s, e)
		for _, x := range []struct {
			g seg.Seg
			b bool
		}{{l, hl}, {r, hr}} {
			if x.b {
				mid = append(mid, x.g)
				out = append(out, Change{I: Interval{x.g.S, x.g.E}})
			}
		}
	}
	u.segs = slices.Concat(u.segs[:i:i], mid, u.segs[j:])
	return out, nil
}

// View 返回当前并集分段的副本，可并发调用。
func (u *Set) View() []Interval {
	u.mu.RLock()
	defer u.mu.RUnlock()
	out := make([]Interval, len(u.segs))
	for k, g := range u.segs {
		out[k] = Interval{g.S, g.E}
	}
	return out
}

// SelfCheck 验证定位复杂度：造 m 段互不相接区间后撤回只命中一段的区间，
// 线性检查个数必须是不随 m 增长的小常数。不导出计数器数值，只给结论。
func SelfCheck() error {
	for _, m := range []int{100, 1000, 10000} {
		u := New(m + 1)
		for k := 0; k < m; k++ {
			if _, err := u.Add(int64(10*k), int64(10*k+5)); err != nil {
				return err
			}
		}
		k := m / 2
		if _, err := u.Withdraw(int64(10*k+2), int64(10*k+3)); err != nil {
			return err
		}
		if u.checked > 4 { // 命中 1 段 + 扫描终止段，与 m 无关
			return errors.New("union: locate cost grows with m")
		}
	}
	return nil
}
