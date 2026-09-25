// Package swin 按 Key 维护会话集合与水位线：合并/闭合/丢弃/开放上限，会话按 start 有序、合并集二分定位。
package swin

import (
	"errors"
	"ontology/sess"
	"sort"
)

var ErrBadGap = errors.New("swin: gap must be positive")
var ErrEmptyKey = errors.New("swin: event key must not be empty")
var ErrTooManyOpen = errors.New("swin: open session limit exceeded")

type Event struct {
	Key string
	TS  int64
}
type keyState struct{ open, closed []sess.Session } // open/closed 均按 start 升序，两会话 gap 邻域互不相交
type Window struct {
	gap, wm, dropped int64
	maxOpen, openN   int
	keys             map[string]*keyState
	haveWm           bool
	lastCmp          int64 // 最近一次 Feed 中为找合并集/判闭合比较过的会话数；刻意非导出，不进入公开接口
}

func NewWindow(gap int64, maxOpen int) (*Window, error) {
	if gap <= 0 {
		return nil, ErrBadGap
	}
	return &Window{gap: gap, maxOpen: maxOpen, keys: map[string]*keyState{}}, nil
}

// Feed 原子喂入：先整体校验空 Key，再逐条应用；任一失败则快照整体回滚（失败不留痕），返回值与事件一一对应。
func (w *Window) Feed(evs []Event) ([]sess.Session, error) {
	for _, e := range evs {
		if e.Key == "" {
			return nil, ErrEmptyKey
		}
	}
	w.lastCmp = 0
	cp := map[string]*keyState{}
	for k, v := range w.keys {
		cp[k] = &keyState{append([]sess.Session(nil), v.open...), append([]sess.Session(nil), v.closed...)}
	}
	w0, d0, h0, n0 := w.wm, w.dropped, w.haveWm, w.openN
	out := make([]sess.Session, 0, len(evs))
	for _, e := range evs {
		r, err := w.apply(e)
		if err != nil {
			w.wm, w.dropped, w.haveWm, w.openN, w.keys = w0, d0, h0, n0, cp
			w.lastCmp = 0
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}
func (w *Window) advance(ks *keyState) { // 冻结 wm>end+gap 的闭合前缀（恰为前缀）；closed 只增不改，merge/drop 永不触及
	n := sort.Search(len(ks.open), func(i int) bool {
		w.lastCmp++
		return !ks.open[i].ShouldClose(w.wm, w.gap)
	})
	for i := range ks.open[:n] {
		ks.open[i].Close()
		ks.closed = append(ks.closed, ks.open[i])
	}
	ks.open = ks.open[n:]
	w.openN -= n
}
func hitOne(s []sess.Session, ts, gap int64, cmp *int64) int { // 超界仅常数次守卫比较即落空，否则二分，不整表扫描
	if len(s) == 0 {
		return -1
	}
	if *cmp++; ts+gap < s[0].Start {
		return -1
	}
	if *cmp++; ts-gap > s[len(s)-1].End {
		return -1
	}
	j := sort.Search(len(s), func(i int) bool { *cmp++; return s[i].Start > ts+gap }) - 1
	if s[j].Hit(ts, gap) {
		return j
	}
	return -1
}
func insertAt(s []sess.Session, p int, v sess.Session) []sess.Session {
	s = append(s, sess.Session{})
	copy(s[p+1:], s[p:])
	s[p] = v
	return s
}
func (w *Window) apply(ev Event) (sess.Session, error) {
	if !w.haveWm || ev.TS > w.wm {
		w.wm, w.haveWm = ev.TS, true // 规则：先推进水位线，再判定
	}
	ks := w.keys[ev.Key]
	if ks == nil {
		ks = &keyState{}
		w.keys[ev.Key] = ks
	}
	w.advance(ks)
	if hitOne(ks.closed, ev.TS, w.gap, &w.lastCmp) >= 0 {
		w.dropped++ // 丢弃分支：M 含已闭合会话，不改任何会话
		return sess.Session{}, nil
	}
	if j := hitOne(ks.open, ev.TS, w.gap, &w.lastCmp); j >= 0 {
		ks.open[j].Absorb(ev.TS) // 合并集分支：含迟到事件的反向扩展
		return ks.open[j], nil
	}
	r := sess.New(ev.TS)
	if r.ShouldClose(w.wm, w.gap) { // 出生即闭合：高水位线后到达的孤立事件
		r.Close()
		p := sort.Search(len(ks.closed), func(i int) bool { return ks.closed[i].Start >= ev.TS })
		ks.closed = insertAt(ks.closed, p, r)
		return r, nil
	}
	if w.openN+1 > w.maxOpen {
		return sess.Session{}, ErrTooManyOpen
	}
	p := sort.Search(len(ks.open), func(i int) bool { return ks.open[i].Start >= ev.TS })
	ks.open = insertAt(ks.open, p, r)
	w.openN++
	return r, nil
}
func (w *Window) View() map[string][]sess.Session { // 纯读拼接（闭合在前、开放在后，按 start 升序），可在 RLock 下并发
	out := make(map[string][]sess.Session, len(w.keys))
	for k, ks := range w.keys {
		v := make([]sess.Session, 0, len(ks.closed)+len(ks.open))
		out[k] = append(append(v, ks.closed...), ks.open...)
	}
	return out
}
func (w *Window) Dropped() int64 { return w.dropped }
func SelfCheck() error { // 只回成败不泄露计数：各档 m 下远低首 start 事件的比较数须为与 m 无关的常数
	for _, m := range []int{100, 1000, 10000} {
		w, _ := NewWindow(3, m+1)
		ev := make([]Event, m)
		for i := range ev {
			ev[i] = Event{Key: "K", TS: int64(6 * i)}
		}
		if _, err := w.Feed(ev); err != nil {
			return err
		}
		if _, err := w.Feed([]Event{{Key: "K", TS: -1000000}}); err != nil || w.lastCmp > 3 {
			return errors.New("swin: self-check comparison count unbounded")
		}
	}
	return nil
}
