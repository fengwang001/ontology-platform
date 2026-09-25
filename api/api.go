// Package api 是保活探测半开连接判定的对外入口，依赖 probe 执行器。
package api

import (
	"errors"
	"fmt"

	"ontology/keep"
	"ontology/probe"
)

// Keeper 对外的保活判定器；所有方法可被多个 goroutine 并发调用。
type Keeper struct {
	r *probe.Runner
}

// New 创建判定器；idleTimeout/probeInterval/maxProbes 任一非正返回
// keep.ErrInvalidParam，且不留任何状态。
func New(idleTimeout, probeInterval, maxProbes int64) (*Keeper, error) {
	r, err := probe.New(idleTimeout, probeInterval, maxProbes)
	if err != nil {
		return nil, err
	}
	return &Keeper{r: r}, nil
}

// Activity 对端有响应。已死返回 keep.ErrDead，时钟回退返回 keep.ErrClockBack；
// 被拒操作在任何状态写入之前失败，状态整体不变。
func (k *Keeper) Activity(now int64) error { return k.r.Activity(now) }

// Tick 推进时钟并判定：probeSent=本次是否发出探测，
// becameDead=本次是否刚刚判死（同一拍不会既发探测又判死）。
func (k *Keeper) Tick(now int64) (probeSent bool, becameDead bool, err error) {
	return k.r.Tick(now)
}

// Probes 返回连续无响应的探测次数。
func (k *Keeper) Probes() int { return k.r.Probes() }

// LastActive 返回最后一次活动时刻。
func (k *Keeper) LastActive() int64 { return k.r.LastActive() }

// Dead 返回连接是否已被判死。
func (k *Keeper) Dead() bool { return k.r.Dead() }

// SelfCheck 用内置时钟序列核验四条不变量（含八步推导序列）、三类哨兵
// 错误与 O(1) 检查边界，全部成立返回 nil。不依赖接收者状态，nil 亦可调用。
func (k *Keeper) SelfCheck() error {
	// 第三节八步序列：sent/became 为本步返回，d/la/probes 为操作后外露状态。
	type exp struct {
		isAct           bool
		t               int64
		sent, became, d bool
		la              int64
		probes          int
	}
	steps := []exp{
		{true, 0, false, false, false, 0, 0},
		{false, 100, true, false, false, 0, 1},
		{false, 130, true, false, false, 0, 2},
		{true, 145, false, false, false, 145, 0},
		{false, 245, true, false, false, 145, 1},
		{false, 275, true, false, false, 145, 2},
		{false, 305, true, false, false, 145, 3},
		{false, 335, false, true, true, 145, 3},
	}
	c, err := New(100, 30, 3)
	if err != nil {
		return err
	}
	for i, s := range steps {
		var sent, became bool
		var e error
		if s.isAct {
			e = c.Activity(s.t)
		} else {
			sent, became, e = c.Tick(s.t)
		}
		if e != nil || sent != s.sent || became != s.became ||
			c.Probes() != s.probes || c.Dead() != s.d || c.LastActive() != s.la {
			return fmt.Errorf("selfcheck: step %d mismatch", i+1)
		}
	}

	// 失败不留痕：判死后 Activity 被拒且状态不变；时钟回退被拒且状态不变。
	if e := c.Activity(336); !errors.Is(e, keep.ErrDead) || !c.Dead() || c.Probes() != 3 {
		return fmt.Errorf("selfcheck: dead activity trace")
	}
	c2, _ := New(100, 30, 3)
	_ = c2.Activity(10)
	if _, _, e := c2.Tick(9); !errors.Is(e, keep.ErrClockBack) ||
		c2.LastActive() != 10 || c2.Probes() != 0 {
		return fmt.Errorf("selfcheck: clock-back trace")
	}
	if _, e := New(0, 1, 1); !errors.Is(e, keep.ErrInvalidParam) {
		return fmt.Errorf("selfcheck: invalid param")
	}
	if _, e := New(1, 0, 1); !errors.Is(e, keep.ErrInvalidParam) {
		return fmt.Errorf("selfcheck: invalid param")
	}
	if _, e := New(1, 1, -1); !errors.Is(e, keep.ErrInvalidParam) {
		return fmt.Errorf("selfcheck: invalid param")
	}
	// O(1)：连续 m 次探测，每次 Tick 检查记录数 <= 1。
	for _, m := range []int{100, 1000, 10000} {
		if !keep.InspectionBoundHolds(m) {
			return fmt.Errorf("selfcheck: inspection bound m=%d", m)
		}
	}
	return nil
}
