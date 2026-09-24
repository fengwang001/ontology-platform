// Package api 对外接口：New / Feed / Snapshots / Totals / SelfCheck。依赖 gwin。
package api

import (
	"fmt"
	"sync"

	"ontology/gwin"
)

// 三类可判定哨兵错误（与 gwin 同源，互不相同）。
var (
	ErrBadPeriod    = gwin.ErrBadPeriod
	ErrEmptyKey     = gwin.ErrEmptyKey
	ErrTooManySnaps = gwin.ErrTooManySnaps
)

// Event 是一个输入元素 {Key, Val}。
type Event = gwin.Event

// Snapshot 是一次触发输出的累计快照。
type Snapshot = gwin.Snapshot

// API 是并发安全的全局窗口服务。
type API struct {
	mu sync.Mutex
	m  *gwin.Manager
}

// New 创建服务；period <= 0 返回 ErrBadPeriod。
func New(period int64, maxSnap int) (*API, error) {
	m, err := gwin.NewManager(period, maxSnap)
	if err != nil {
		return nil, err
	}
	return &API{m: m}, nil
}

// Feed 原子地喂入一批元素：任一被拒则整批不生效。
func (a *API) Feed(evs []Event) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.m.Apply(evs)
}

// Snapshots 返回该 Key 的快照历史（按触发顺序）。
func (a *API) Snapshots(key string) []Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.m.Snapshots(key)
}

// Totals 返回该 Key 当前累计 sum/cnt。
func (a *API) Totals(key string) (sum, cnt int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.m.Totals(key)
}

// SelfCheck 用内置元素序列核验四条不变量与 O(1) 快照读取，全部通过返回 nil。
// 只使用全新实例，不触碰接收者状态，可并发调用。
func (a *API) SelfCheck() error {
	vals := []int64{10, 20, 30, 40, 50, 60, 70, 80}
	feed := func(ap *API, key string) error {
		for _, v := range vals {
			if err := ap.Feed([]Event{{Key: key, Val: v}}); err != nil {
				return err
			}
		}
		return nil
	}
	// 不变量 1+2+3：快照与批量重算逐字段一致、触发不重置、cnt 严格递增。
	ap, err := New(3, 8)
	if err != nil {
		return err
	}
	if err := feed(ap, "k"); err != nil {
		return err
	}
	var sum int64
	prevCnt := int64(0)
	for i, s := range ap.Snapshots("k") {
		m := int64(i + 1)
		sum = 0
		for j := int64(0); j < m*3; j++ {
			sum += vals[j]
		}
		if s.Cnt != m*3 || s.Sum != sum {
			return fmt.Errorf("selfcheck: snapshot %d = %+v, want (%d,%d)", i, s, sum, m*3)
		}
		if s.Cnt <= prevCnt {
			return fmt.Errorf("selfcheck: snapshot cnt not increasing at %d", i)
		}
		prevCnt = s.Cnt
	}
	if s, c := ap.Totals("k"); s != 360 || c != 8 {
		return fmt.Errorf("selfcheck: totals = (%d,%d), want (360,8)", s, c)
	}
	// 不变量 4：三类错误可判定、互不相同，被拒后状态不变。
	if _, err := New(0, 1); err != ErrBadPeriod {
		return fmt.Errorf("selfcheck: bad period err = %v", err)
	}
	ap2, _ := New(3, 8)
	if err := feed(ap2, "k"); err != nil {
		return err
	}
	beforeS, beforeC := ap2.Totals("k")
	if err := ap2.Feed([]Event{{Key: "k", Val: 1}, {Key: "", Val: 2}}); err != ErrEmptyKey {
		return fmt.Errorf("selfcheck: empty key err = %v", err)
	}
	if s, c := ap2.Totals("k"); s != beforeS || c != beforeC {
		return fmt.Errorf("selfcheck: state changed after rejected feed")
	}
	ap3, _ := New(3, 1)
	if err := ap3.Feed([]Event{{Key: "k", Val: 1}, {Key: "k", Val: 2}, {Key: "k", Val: 3},
		{Key: "k", Val: 4}, {Key: "k", Val: 5}, {Key: "k", Val: 6}}); err != ErrTooManySnaps {
		return fmt.Errorf("selfcheck: maxsnap err = %v", err)
	}
	if s, c := ap3.Totals("k"); s != 0 || c != 0 || len(ap3.Snapshots("k")) != 0 {
		return fmt.Errorf("selfcheck: state changed after maxsnap rejection")
	}
	if ErrBadPeriod == ErrEmptyKey || ErrEmptyKey == ErrTooManySnaps || ErrBadPeriod == ErrTooManySnaps {
		return fmt.Errorf("selfcheck: sentinel errors not distinct")
	}
	// O(1)：period=m+1，累积 m 个不触发，第 m+1 个恰好触发，重读数恒为 0。
	for _, m := range []int64{100, 1000, 10000} {
		g, _ := gwin.NewManager(m+1, 1)
		evs := make([]gwin.Event, m+1)
		for i := range evs {
			evs[i] = gwin.Event{Key: "k", Val: int64(i)}
		}
		if err := g.Apply(evs); err != nil {
			return err
		}
		if !g.SnapshotReadO1("k") {
			return fmt.Errorf("selfcheck: snapshot re-reads accumulated elements at m=%d", m)
		}
	}
	return nil
}
