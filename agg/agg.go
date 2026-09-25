// Package agg 是聚合驱动：按事件流应用内存阈值与 LRU 溢写/回载规则。
// 它依赖 spill，做参数/溢出校验与计数；本身非并发安全，并发由 api 层串行化。
package agg

import (
	"errors"
	"math"

	"ontology/spill"
)

// 三类哨兵错误中的两类；ErrInvalidLimit 由 api 层定义（构造期拒绝）。
var (
	ErrEmptyKey = errors.New("agg: empty key")
	ErrOverflow = errors.New("agg: sum overflows int64")
)

// Event 是上游的一个 (Key, Val) 事件。
type Event struct {
	Key string
	Val int64
}

// Aggregator 驱动一个 spill.Store，维护常驻上限与溢写/回载计数。
type Aggregator struct {
	st     *spill.Store
	limit  int
	spills int
	loads  int
}

// New 创建常驻上限为 limit 的聚合器；limit 非正由上层（api.New）拒绝。
func New(limit int) *Aggregator {
	return &Aggregator{st: spill.New(), limit: limit}
}

// add64 返回 cur+delta，越界（超出 int64）时 ok=false。
func add64(cur, delta int64) (sum int64, ok bool) {
	if delta > 0 && cur > math.MaxInt64-delta {
		return 0, false
	}
	if delta < 0 && cur < math.MinInt64-delta {
		return 0, false
	}
	return cur + delta, true
}

// validate 在影子状态上按每个 Key 自身的事件顺序模拟累加；起点必须取该 Key
// 当前的常驻值或溢写值（跨批次已累积的部分），否则漏检「已有值+本批」溢出。
// 整批合法才放行：任一事件非法即整体拒绝（失败不留痕）。
func (a *Aggregator) validate(evs []Event) error {
	sums := make(map[string]int64, len(evs))
	for _, ev := range evs {
		if ev.Key == "" {
			return ErrEmptyKey
		}
		cur, seen := sums[ev.Key]
		if !seen {
			if st, v := a.st.Lookup(ev.Key); st != spill.Absent {
				cur = v
			}
		}
		next, ok := add64(cur, ev.Val)
		if !ok {
			return ErrOverflow
		}
		sums[ev.Key] = next
	}
	return nil
}

// apply 应用单个已校验事件，超员时立即溢写一个 LRU 组；返回被溢写者。
func (a *Aggregator) apply(ev Event) (evictedKey string, evicted bool) {
	switch st, cur := a.st.Lookup(ev.Key); st {
	case spill.Resident:
		a.st.PutResident(ev.Key, cur+ev.Val) // 影子已保证不溢出
	case spill.Spilled:
		a.st.LoadIn(ev.Key, ev.Val) // 部分和+新值，入常驻并删除溢写条目
		a.loads++
	default:
		a.st.PutResident(ev.Key, ev.Val)
	}
	if a.st.ResidentLen() > a.limit {
		k, _ := a.st.Evict()
		a.spills++
		return k, true
	}
	return "", false
}

// Feed 应用一批事件；任一条被拒则整批不生效，实例之后仍可正常使用。
func (a *Aggregator) Feed(evs []Event) error {
	if err := a.validate(evs); err != nil {
		return err
	}
	for _, ev := range evs {
		a.apply(ev)
	}
	return nil
}

// View 返回每个 Key 的最终累加和（常驻值或溢写值，取其一，永不相加）。
func (a *Aggregator) View() map[string]int64 {
	r, sp := a.st.Snapshot()
	for k, v := range sp {
		r[k] = v // 任一 Key 至多存在一处，直接填入即覆盖语义
	}
	return r
}

// Loads 返回累计回载次数。
func (a *Aggregator) Loads() int { return a.loads }

// Spills 返回累计溢写次数。
func (a *Aggregator) Spills() int { return a.spills }

// StepStat 是分步轨迹中一步应用后的状态快照。
type StepStat struct {
	Kind       string // insert | update | reload
	Resident   map[string]int64
	Spilled    map[string]int64
	EvictedKey string
	Evicted    bool
	Spills     int
	Loads      int
}

// StepTrace 在独立副本上逐步复演，供测试与 demo 逐步钉住规则。
func StepTrace(limit int, evs []Event) []StepStat {
	a := New(limit)
	out := make([]StepStat, 0, len(evs))
	for _, ev := range evs {
		kind := "insert"
		if st, _ := a.st.Lookup(ev.Key); st == spill.Resident {
			kind = "update"
		} else if st == spill.Spilled {
			kind = "reload"
		}
		k, did := a.apply(ev)
		r, sp := a.st.Snapshot()
		out = append(out, StepStat{
			Kind: kind, Resident: r, Spilled: sp,
			EvictedKey: k, Evicted: did, Spills: a.spills, Loads: a.loads,
		})
	}
	return out
}
