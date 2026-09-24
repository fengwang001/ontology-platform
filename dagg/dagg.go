// Package dagg 管理多组多重集：批内顺序校验应用、批前批后 distinct 比较产日志、条目上限检查。
package dagg

import (
	"errors"
	"sort"
	"sync"

	"ontology/mset"
)

var (
	ErrWithdraw = errors.New("dagg: withdraw of a value that is not present") // 撤回 mult==0 的 (Group,Val)
	ErrInvalid  = errors.New("dagg: invalid change")                          // Sign 非 ±1 或 Group/Val 为空
	ErrLimit    = errors.New("dagg: distinct entry count exceeds limit")      // 条目数超 maxEntries
)

// Change 是一条变更：Sign=+1 插入一次，Sign=-1 撤回一次。
type Change struct {
	Group string
	Val   string
	Sign  int
}

// Out 是变更日志一条：Sign=+1 为 +(Group,N)，-1 为 -(Group,N)。
type Out struct {
	Group string
	N     int
	Sign  int
}

// Agg 是多组增量 COUNT DISTINCT 聚合器。
type Agg struct {
	mu         sync.Mutex
	maxEntries int
	groups     map[string]*mset.Set
	entries    int // 全局 mult>0 的 (Group,Val) 条目数
	checked    int // 最近一次 Feed 为判定 distinct 变化检查过的条目数（非导出）
}

// New 创建上限为 maxEntries 的聚合器（maxEntries<=0 表示不限）。
func New(maxEntries int) *Agg {
	return &Agg{maxEntries: maxEntries, groups: make(map[string]*mset.Set)}
}

// Feed 按批内顺序校验并应用一批变更，返回该批变更日志。
// 任一条被拒则整批回滚、不生效，返回哨兵错误且 outs 为 nil。
func (a *Agg) Feed(batch []Change) (outs []Out, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.checked = 0
	old := map[string]int{} // 组 → 批开始前 distinct 数
	var touched, fresh []string
	type undo struct {
		g, v string
		add  bool
	}
	var applied []undo // 已成功应用的变更，失败时逆序回滚
	rollback := func() {
		for i := len(applied) - 1; i >= 0; i-- {
			u := applied[i]
			s := a.groups[u.g]
			if u.add {
				if x, _ := s.Remove(u.v); x {
					a.entries--
				}
			} else if s.Add(u.v) {
				a.entries++
			}
		}
		for _, g := range fresh { // 批内新建且回滚后为空的组删除
			if a.groups[g].Distinct() == 0 {
				delete(a.groups, g)
			}
		}
	}
	set := func(g string) *mset.Set {
		if _, ok := old[g]; !ok {
			s := a.groups[g]
			if s != nil {
				old[g] = s.Distinct()
			} else {
				old[g] = 0
			}
			touched = append(touched, g)
		}
		s := a.groups[g]
		if s == nil {
			s = mset.New()
			a.groups[g] = s
			fresh = append(fresh, g)
		}
		return s
	}
	for _, c := range batch {
		if c.Group == "" || c.Val == "" || (c.Sign != 1 && c.Sign != -1) {
			rollback()
			return nil, ErrInvalid
		}
		s := set(c.Group)
		a.checked++ // 每条变更只检查这一个 (Group,Val) 条目即可判定 0↔1 跨越
		if c.Sign == 1 {
			if s.Add(c.Val) {
				a.entries++
			}
			applied = append(applied, undo{c.Group, c.Val, true})
		} else {
			x, e := s.Remove(c.Val)
			if e != nil {
				rollback()
				return nil, ErrWithdraw
			}
			if x {
				a.entries--
			}
			applied = append(applied, undo{c.Group, c.Val, false})
		}
		if a.maxEntries > 0 && a.entries > a.maxEntries {
			rollback()
			return nil, ErrLimit
		}
	}
	sort.Strings(touched) // 多组输出按 Group 字典序
	for _, g := range touched {
		o, n := old[g], a.groups[g].Distinct()
		switch {
		case o == n: // 批前后相同：不输出
		case o > 0 && n > 0:
			outs = append(outs, Out{g, o, -1}, Out{g, n, 1})
		case n > 0:
			outs = append(outs, Out{g, n, 1})
		default:
			outs = append(outs, Out{g, o, -1})
		}
	}
	return outs, nil
}

// View 返回各组当前 distinct 数（为 0 的组不出现），是某完整批次后的快照。
func (a *Agg) View() map[string]int {
	a.mu.Lock()
	defer a.mu.Unlock()
	v := make(map[string]int, len(a.groups))
	for g, s := range a.groups {
		if d := s.Distinct(); d > 0 {
			v[g] = d
		}
	}
	return v
}
