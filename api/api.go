// Package api 是 Marzullo 时钟同步的对外接口，依赖方向：api -> csync（-> marz）。
package api

import (
	"fmt"
	"sort"

	"ontology/csync"
)

// 四类互不相同的可判定哨兵错误（直接复用 csync 同一组值，errors.Is 可判）。
var (
	ErrNegativeError = csync.ErrNegativeError // error<0，区间倒置
	ErrDuplicateID   = csync.ErrDuplicateID   // 重复时钟 ID
	ErrInvalidF      = csync.ErrInvalidF      // f<0 或 f>=K
	ErrNoConsensus   = csync.ErrNoConsensus   // 时钟不足/无共识区间
	ErrEmptyID       = csync.ErrEmptyID       // 空 ID
)

// API 是进程内存态的时钟同步器。
type API struct {
	set *csync.Set
}

// New 以允许坏钟数 f 创建同步器；f<0 立即返回 ErrInvalidF，且无对象产生。
func New(f int) (*API, error) {
	s, err := csync.NewSet(f)
	if err != nil {
		return nil, err
	}
	return &API{set: s}, nil
}

// Add 加入一台时钟；被拒时整体失败且不留痕（校验全部在 csync 内状态修改之前）。
func (a *API) Add(id string, offset, errValue int64) error {
	return a.set.Add(csync.Clock{ID: id, Offset: offset, Err: errValue})
}

// Consensus 返回最短共识闭区间 [lo,hi]；时钟不足或 f>=K 返回可判定错误。
func (a *API) Consensus() (lo, hi int64, err error) { return a.set.Consensus() }

// CountAt 返回闭区间包含时刻 t 的时钟个数。
func (a *API) CountAt(t int64) int { return a.set.CountAt(t) }

// naiveConsensus 取快照时钟全量重跑一遍扫描线（不变量 1 的独立朴素参照，不依赖 marz）。
func naiveConsensus(cs []csync.Clock, need int) (lo, hi int64, ok bool) {
	type ev struct {
		p int64
		d int
	}
	es := make([]ev, 0, 2*len(cs))
	for _, c := range cs {
		es = append(es, ev{c.Offset - c.Err, 1}, ev{c.Offset + c.Err, -1})
	}
	sort.SliceStable(es, func(i, j int) bool {
		return es[i].p < es[j].p || es[i].p == es[j].p && es[i].d > es[j].d
	})
	n, in := 0, false
	var cl int64
	for _, e := range es {
		prev := n
		n += e.d
		if !in && prev < need && n >= need {
			cl, in = e.p, true
		} else if in && n < need {
			if !ok || e.p-cl < hi-lo {
				lo, hi = cl, e.p
			}
			ok, in = true, false
		}
	}
	return
}

func naiveCountAt(cs []csync.Clock, t int64) int {
	n := 0
	for _, c := range cs {
		if c.Offset-c.Err <= t && t <= c.Offset+c.Err {
			n++
		}
	}
	return n
}

// SelfCheck 对一组内置时钟序列核验四条不变量；任一不成立返回带细节的错误，过程不改 a 的状态。
func (a *API) SelfCheck() error {
	s, _ := csync.NewSet(1)
	type clock struct {
		id     string
		off, e int64
	}
	seq := []clock{{"A", 10, 2}, {"B", 11, 1}, {"C", 20, 1}}
	for _, c := range seq {
		if err := s.Add(csync.Clock{ID: c.id, Offset: c.off, Err: c.e}); err != nil {
			return err
		}
	}
	k := s.Len()
	lo, hi, err := s.Consensus()
	if err != nil || lo != 10 || hi != 12 {
		return fmt.Errorf("selfcheck: consensus = [%d,%d] %v, want [10,12]", lo, hi, err)
	}
	cs := s.Snapshot()
	// 不变量 1：与朴素重算一致。
	nlo, nhi, ok := naiveConsensus(cs, k-s.F())
	if !ok || nlo != lo || nhi != hi {
		return fmt.Errorf("selfcheck: invariant1 naive [%d,%d] ok=%v", nlo, nhi, ok)
	}
	// 不变量 2：两端点各来自某台时钟误差边界，且端点上计数达到 need（闭区间）。
	bound := map[int64]bool{}
	for _, c := range cs {
		bound[c.Offset-c.Err] = true
		bound[c.Offset+c.Err] = true
	}
	need := k - s.F()
	if !bound[lo] || !bound[hi] || s.CountAt(lo) < need || s.CountAt(hi) < need {
		return fmt.Errorf("selfcheck: invariant2 boundary closed failed")
	}
	// 不变量 3：扫描覆盖端点内外多个时刻，CountAt 等于朴素遍历。
	for _, t := range []int64{7, 8, 9, 10, 11, 12, 13, 18, 19, 20, 21, 22} {
		if got, want := s.CountAt(t), naiveCountAt(cs, t); got != want {
			return fmt.Errorf("selfcheck: invariant3 CountAt(%d)=%d want %d", t, got, want)
		}
	}
	// 不变量 4：负 error 与重复 ID 被拒后状态不变、仍可共识。
	if err := s.Add(csync.Clock{ID: "D", Offset: 0, Err: -1}); err != ErrNegativeError {
		return fmt.Errorf("selfcheck: invariant4 negative error not rejected")
	}
	if err := s.Add(csync.Clock{ID: "A", Offset: 0, Err: 0}); err != ErrDuplicateID {
		return fmt.Errorf("selfcheck: invariant4 duplicate id not rejected")
	}
	if s.Len() != k {
		return fmt.Errorf("selfcheck: invariant4 state mutated by rejected add")
	}
	if lo2, hi2, err := s.Consensus(); err != nil || lo2 != 10 || hi2 != 12 {
		return fmt.Errorf("selfcheck: invariant4 consensus changed after rejection")
	}
	return nil
}
