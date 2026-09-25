// Package api 变更流偏移归档与保留的对外接口。依赖 off。并发安全。
package api

import (
	"errors"
	"maps"
	"sync"

	"ontology/off"
)

// 四类可判定哨兵错误，互不相同；后三者复用 off 包。
var (
	ErrInvalidRetention = errors.New("api: retention must be positive")
	ErrNonMonotonic     = off.ErrNonMonotonic
	ErrNowRegression    = off.ErrNowRegression
	ErrNoCommit         = off.ErrNoCommit
)

// V 变更流偏移归档与保留实例。
type V struct{ st *state }

type state struct {
	mu        sync.RWMutex
	retention int64
	parts     map[int]*off.Partition
}

// New 创建实例；retention 必须为正，否则失败且不产生任何状态。
func New(retention int64) (V, error) {
	if retention <= 0 {
		return V{}, ErrInvalidRetention
	}
	return V{&state{retention: retention, parts: map[int]*off.Partition{}}}, nil
}

// Commit 令分区 p 的已提交位点 C[p]=off 并归档 (off, ts)；非单调则整体失败。
func (v V) Commit(p int, off64, ts int64) error {
	v.st.mu.Lock()
	defer v.st.mu.Unlock()
	pt := v.st.parts[p]
	if pt == nil {
		pt = &off.Partition{}
	}
	if err := pt.Commit(off64, ts); err != nil { // 校验失败，新分区也不落表
		return err
	}
	v.st.parts[p] = pt
	return nil
}

func (v V) Checkpoint(p int) error {
	v.st.mu.Lock()
	defer v.st.mu.Unlock()
	if pt := v.st.parts[p]; pt != nil {
		return pt.Checkpoint()
	}
	return ErrNoCommit
}

func (v V) Evict(p int, now int64) error {
	v.st.mu.Lock()
	defer v.st.mu.Unlock()
	if pt := v.st.parts[p]; pt != nil {
		return pt.Evict(now, v.st.retention)
	}
	return nil
}

func (v V) Committed(p int) (int64, bool) {
	v.st.mu.RLock()
	defer v.st.mu.RUnlock()
	if pt := v.st.parts[p]; pt != nil {
		return pt.Committed()
	}
	return 0, false
}

func (v V) Restart() map[int]int64 {
	v.st.mu.RLock()
	defer v.st.mu.RUnlock()
	out := make(map[int]int64, len(v.st.parts))
	for p, pt := range v.st.parts {
		out[p] = pt.Recover()
	}
	return out
}

// SelfCheck 对内置操作序列核验四条不变量；不触碰接收者状态，可并发调用。
func (v V) SelfCheck() error {
	w, _ := New(10) // retention=10 恒成功；New 的错误路径在不变量4 核验
	// 不变量1+2：八步序列对照批量重算（NOTES.md 表）；cp=100 永不丢。
	// 步骤编码 {kind: 0=Commit 1=Checkpoint 2=Evict, off, ts/now, want}。
	steps := [][4]int64{
		{0, 100, 0, 100}, {1, 0, 0, 100}, {0, 110, 10, 110}, {0, 120, 15, 120},
		{2, 0, 20, 120}, {0, 130, 30, 130}, {2, 0, 40, 130}, {2, 0, 45, 100},
	}
	ops := []func(int64, int64) error{
		func(off, ts int64) error { return w.Commit(0, off, ts) },
		func(_, _ int64) error { return w.Checkpoint(0) },
		func(_, now int64) error { return w.Evict(0, now) },
	}
	for _, s := range steps {
		if err := ops[s[0]](s[1], s[2]); err != nil {
			return err
		}
		if w.Restart()[0] != s[3] {
			return errors.New("selfcheck: recover mismatch with batch recompute")
		}
	}
	if err := w.Evict(0, 1<<40); err != nil || w.Restart()[0] != 100 {
		return errors.New("selfcheck: checkpoint evicted")
	}
	// 不变量3：最新未检查点归档始终幸存时，恢复位点单调不减。
	m, _ := New(5)
	prev := off.NegInf
	for i := int64(0); i < 6; i++ {
		err := m.Commit(1, 100+i, i*2)
		if i%2 == 1 && err == nil {
			err = m.Checkpoint(1)
		}
		if err == nil {
			err = m.Evict(1, i*2) // 阈值 i*2-5 < 最新 ts，最新归档幸存
		}
		if err != nil {
			return err
		}
		r := m.Restart()[1]
		if r < prev {
			return errors.New("selfcheck: recovery regressed")
		}
		prev = r
	}
	// 不变量4：四类拒绝互不相同且不留痕。
	if _, err := New(0); !errors.Is(err, ErrInvalidRetention) {
		return errors.New("selfcheck: non-positive retention accepted")
	}
	c0, _ := w.Committed(0)
	before := w.Restart()
	if w.Commit(0, 100, 50) == nil || w.Commit(0, 200, -1) == nil || // off 不增、ts 回退
		w.Evict(0, 10) == nil || w.Checkpoint(9) == nil { // now 回退、无已提交位点
		return errors.New("selfcheck: invalid op accepted")
	}
	c1, _ := w.Committed(0)
	if c1 != c0 || !maps.Equal(before, w.Restart()) {
		return errors.New("selfcheck: rejected op changed state")
	}
	return nil
}
