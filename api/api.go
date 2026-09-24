// Package api 是对外门面：New / Observe / Counts / SelfCheck。依赖 wm。
package api

import (
	"errors"
	"fmt"

	"ontology/drift"
	"ontology/wm"
)

// 对外暴露的分类类型与三个可判定哨兵错误（与 wm 同源，可用 errors.Is 判定）。
type Class = drift.Class

const (
	Normal   = drift.Normal
	Drift    = drift.Drift
	Reorder  = drift.Reorder
	Rollback = drift.Rollback
)

var (
	ErrInvalidThreshold = wm.ErrInvalidThreshold
	ErrEmptySource      = wm.ErrEmptySource
	ErrNegativeMark     = wm.ErrNegativeMark
)

// Detector 是水位线漂移/回拨检测器，可并发使用。
type Detector struct{ m *wm.Manager }

// New 构造检测器；driftThreshold<=0 或 rollbackTolerance<0 时整体失败。
func New(driftThreshold, rollbackTolerance int64) (*Detector, error) {
	m, err := wm.New(driftThreshold, rollbackTolerance)
	if err != nil {
		return nil, err
	}
	return &Detector{m: m}, nil
}

// Observe 观测一条水位线并返回分类；source 为空或 w<0 时被拒且不留痕。
func (d *Detector) Observe(source string, w int64) (Class, error) {
	return d.m.Observe(source, w)
}

// Counts 返回 (drift, reorder, rollback) 三个全局计数器。
func (d *Detector) Counts() (int64, int64, int64) { return d.m.Counts() }

// Last 返回某源当前最后接受的水位线；无基线时 ok=false。
func (d *Detector) Last(source string) (int64, bool) { return d.m.Last(source) }

// CheckConstantReads 验证大 m 下单次判定读取历史个数恒为 1（只给布尔结论）。
func (d *Detector) CheckConstantReads(steps int) bool { return d.m.CheckConstantReads(steps) }

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
// 只在内部新建实例上运行，不改变接收者状态。
func (d *Detector) SelfCheck() error {
	seq := []int64{100, 105, 120, 118, 117, 116, 130, 141, 5, 5, 200, 199, 0, 42}
	det, err := New(10, 3)
	if err != nil {
		return err
	}
	// 不变量 1+2：逐条观测，last 单调不减，分类与朴素重算逐条一致。
	naive := naiveClassify(seq, 10, 3)
	prev := int64(-1)
	tally := map[Class]int64{}
	for i, w := range seq {
		cls, err := det.Observe("s", w)
		if err != nil {
			return err
		}
		if cls != naive[i] {
			return fmt.Errorf("selfcheck: step %d class %v != naive %v", i, cls, naive[i])
		}
		last, _ := det.Last("s")
		if last < prev {
			return fmt.Errorf("selfcheck: last decreased at step %d", i)
		}
		prev = last
		tally[cls]++
	}
	// 不变量 3：计数守恒。
	dn, rn, rb := det.Counts()
	if dn != tally[Drift] || rn != tally[Reorder] || rb != tally[Rollback] ||
		dn+rn+rb != tally[Drift]+tally[Reorder]+tally[Rollback] {
		return errors.New("selfcheck: counts not conserved")
	}
	// 不变量 4：三类拒绝互不相同且不留痕。
	before1, before2, before3 := det.Counts()
	lastBefore, _ := det.Last("s")
	_, e1 := det.Observe("", 5)
	_, e3 := det.Observe("s", -1)
	if _, e4 := New(0, 1); e4 == nil {
		return errors.New("selfcheck: bad threshold accepted")
	} else if !errors.Is(e4, ErrInvalidThreshold) {
		return errors.New("selfcheck: wrong threshold error")
	}
	if !errors.Is(e1, ErrEmptySource) || !errors.Is(e3, ErrNegativeMark) || e1 == e3 {
		return errors.New("selfcheck: reject errors not distinct")
	}
	a1, a2, a3 := det.Counts()
	lastAfter, _ := det.Last("s")
	if before1 != a1 || before2 != a2 || before3 != a3 || lastBefore != lastAfter {
		return errors.New("selfcheck: rejected op mutated state")
	}
	return nil
}

// naiveClassify 是独立的朴素单遍重算，用于对拍。
func naiveClassify(seq []int64, thr, tol int64) []Class {
	out := make([]Class, len(seq))
	var last int64
	has := false
	for i, w := range seq {
		switch {
		case !has:
			has, last, out[i] = true, w, Normal
		case w > last+thr:
			last, out[i] = w, Drift
		case w >= last:
			last, out[i] = w, Normal
		case last-w <= tol:
			out[i] = Reorder
		default:
			out[i] = Rollback
		}
	}
	return out
}
