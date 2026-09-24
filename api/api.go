// Package api 是全局窗口周期性触发的对外门面：仅 New/Feed/Snapshots/Totals/SelfCheck。
// 依赖方向 api -> gwin -> gw，不反向依赖。
package api

import (
	"errors"
	"fmt"

	"ontology/gwin"
)

// Window 是一个全局窗口实例；状态只在进程内存里。
type Window struct {
	t *gwin.Table
}

// New 创建窗口：period 为正整数，maxSnap 为每个 Key 允许的最大快照数。
func New(period int64, maxSnap int) (*Window, error) {
	t, err := gwin.New(period, maxSnap)
	if err != nil {
		return nil, err
	}
	return &Window{t: t}, nil
}

// Feed 原子地喂入一批元素；任一被拒则整批不生效，错误为哨兵错误。
func (w *Window) Feed(evs []gwin.Event) error { return w.t.Feed(evs) }

// Snapshots 返回该 Key 快照列表的副本，按触发顺序、cnt 严格递增。
func (w *Window) Snapshots(key string) []gwin.Snapshot { return w.t.Snapshots(key) }

// Totals 返回该 Key 截至目前的累计和与累计个数（全局累计，从不重置）。
func (w *Window) Totals(key string) (sum, cnt int64) { return w.t.Totals(key) }

// SelfCheck 用内置序列（period=3，值 10..80）核验四条不变量，
// 在一个全新的内部窗口上演算，不影响接收者状态；可被测试直接调用。
func (w *Window) SelfCheck() error {
	t, err := gwin.New(3, 100)
	if err != nil {
		return err
	}
	vals := []int64{10, 20, 30, 40, 50, 60, 70, 80}
	evs := make([]gwin.Event, len(vals))
	for i, v := range vals {
		evs[i] = gwin.Event{Key: "selfcheck", Val: v}
	}
	if err := t.Feed(evs); err != nil {
		return err
	}
	snaps := t.Snapshots("selfcheck")
	// 不变量 1/3：第 m 次快照 cnt=m*period、sum=前 m*period 个元素之和，cnt 严格递增。
	if len(snaps) != 2 {
		return fmt.Errorf("selfcheck: want 2 snapshots, got %d", len(snaps))
	}
	for i, s := range snaps {
		var sumPrefix int64
		for j := int64(0); j < (int64(i)+1)*3; j++ {
			sumPrefix += vals[j]
		}
		if s.Cnt != (int64(i)+1)*3 || s.Sum != sumPrefix {
			return fmt.Errorf("selfcheck: snapshot %d mismatch: (%d,%d)", i, s.Sum, s.Cnt)
		}
		if i > 0 && s.Cnt <= snaps[i-1].Cnt {
			return errors.New("selfcheck: snapshots not strictly increasing")
		}
	}
	// 不变量 2：触发不重置，Totals 仍是全量 (360,8)。
	if sum, cnt := t.Totals("selfcheck"); sum != 360 || cnt != 8 {
		return fmt.Errorf("selfcheck: totals after triggers = (%d,%d), want (360,8)", sum, cnt)
	}
	// 不变量 4：空 Key 整批被拒且不留痕（“ok” 也不得出现）。
	before := len(snaps)
	if err := t.Feed([]gwin.Event{{Key: "ok", Val: 1}, {Key: "", Val: 1}}); !errors.Is(err, gwin.ErrEmptyKey) {
		return fmt.Errorf("selfcheck: want ErrEmptyKey, got %v", err)
	}
	if sum, cnt := t.Totals("ok"); sum != 0 || cnt != 0 || len(t.Snapshots("selfcheck")) != before {
		return errors.New("selfcheck: rejected batch left a trace")
	}
	return nil
}
