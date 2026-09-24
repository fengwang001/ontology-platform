// Package api 是会话窗口对外门面：New/Feed/Snapshot/SelfCheck。
// 仅依赖 sess（sess 再依赖 evt），依赖方向单向。
package api

import (
	"fmt"
	"math/rand"
	"sort"
	"sync"

	"ontology/evt"
	"ontology/sess"
)

// Session 是对外快照中的一个会话（闭区间 [Start,End] 加计数 N）。
type Session = sess.Session

// 三类可判定哨兵错误（别名到下层定义，保证全局唯一、互不相同）。
var (
	ErrBadGap       = sess.ErrBadGap
	ErrTooMany      = sess.ErrTooMany
	ErrInvalidEvent = evt.ErrInvalidEvent
)

// API 是并发安全的会话窗口服务。
type API struct {
	mu  sync.RWMutex
	set *sess.Set
	gap int64
	max int
}

// New 构造服务：gap 必须为正；maxSessions<=0 表示不限制每 Key 会话数。
func New(gap int64, maxSessions int) (*API, error) {
	s, err := sess.NewSet(gap, maxSessions)
	if err != nil {
		return nil, err
	}
	return &API{set: s, gap: gap, max: maxSessions}, nil
}

// Feed 原子喂入一批事件：任一非法或超限则整批失败、不留任何痕迹。
func (a *API) Feed(evs []evt.Event) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.set.Feed(evs)
}

// Snapshot 返回某 Key 当前的规范会话序列副本，可被多 goroutine 并发只读调用。
func (a *API) Snapshot(key string) []Session {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.set.Sessions(key)
}

// builtin 是自检用的内置事件序列（含乱序、重复时间戳、桥接点）。
var builtin = []int64{100, 105, 130, 135, 118, 100, 200, 190, 190, 3, 12}

// SelfCheck 对一组内置事件序列核验四条不变量，全部成立返回 nil，否则返回可判定描述。
func (a *API) SelfCheck() error {
	a.mu.RLock()
	gap := a.gap
	a.mu.RUnlock()
	es := make([]evt.Event, len(builtin))
	for i, t := range builtin {
		es[i] = evt.Event{Key: "selfcheck", TS: t}
	}
	ref := recompute(builtin, gap)
	s, _ := sess.NewSet(gap, 0)
	if err := s.Feed(es); err != nil {
		return err
	}
	got := s.Sessions("selfcheck")
	if !sess.Equal(got, ref) { // 不变量 1：与全量重算一致
		return fmt.Errorf("selfcheck: mismatch with recompute: %v != %v", got, ref)
	}
	r := rand.New(rand.NewSource(42)) // 不变量 2：多种打乱顺序结果相同
	for round := 0; round < 8; round++ {
		p := r.Perm(len(es))
		z, _ := sess.NewSet(gap, 0)
		sh := make([]evt.Event, len(es))
		for i, q := range p {
			sh[i] = es[q]
		}
		if err := z.Feed(sh); err != nil || !sess.Equal(z.Sessions("selfcheck"), ref) {
			return fmt.Errorf("selfcheck: order-dependent result on round %d", round)
		}
	}
	if err := canonical(got, gap); err != nil { // 不变量 3：规范形
		return err
	}
	capped, _ := sess.NewSet(gap, 2) // 不变量 4：拒绝不留痕
	for _, t := range []int64{0, 100} {
		if err := capped.Add(evt.Event{Key: "selfcheck", TS: t}); err != nil {
			return err
		}
	}
	before := capped.Sessions("selfcheck")
	if err := capped.Add(evt.Event{Key: "selfcheck", TS: 999}); err != ErrTooMany {
		return fmt.Errorf("selfcheck: expected ErrTooMany, got %v", err)
	}
	if err := capped.Add(evt.Event{Key: "", TS: 1}); err != ErrInvalidEvent {
		return fmt.Errorf("selfcheck: expected ErrInvalidEvent, got %v", err)
	}
	if !sess.Equal(capped.Sessions("selfcheck"), before) {
		return fmt.Errorf("selfcheck: state changed after rejection")
	}
	return nil
}

// canonical 校验规范形：N>0、Start<=End、start 严格升序、相邻间隔严格大于 gap。
func canonical(ss []Session, gap int64) error {
	for i, s := range ss {
		if s.N <= 0 || s.Start > s.End {
			return fmt.Errorf("canonical: bad session %v", s)
		}
		if i > 0 {
			if s.Start <= ss[i-1].Start {
				return fmt.Errorf("canonical: starts not strictly increasing")
			}
			if s.Start-ss[i-1].End <= gap {
				return fmt.Errorf("canonical: adjacent gap not > gap")
			}
		}
	}
	return nil
}

// recompute 是题面指定的参考实现：全部事件排序后从头扫一遍分会话。
func recompute(ts []int64, gap int64) []Session {
	cp := append([]int64(nil), ts...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	var out []Session
	for _, t := range cp {
		if n := len(out); n > 0 && t-out[n-1].End <= gap {
			out[n-1].End, out[n-1].N = t, out[n-1].N+1
		} else {
			out = append(out, Session{Start: t, End: t, N: 1})
		}
	}
	return out
}
