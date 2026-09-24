// Package wagg 维护 (Key,窗口) 计数、三档触发变更日志与到期清除。
package wagg

import (
	"errors"
	"math"
	"sync"

	"ontology/win"
)

var (
	ErrInvalidParam = errors.New("wagg: invalid parameter")
	ErrTooManyOpen  = errors.New("wagg: too many open windows")
	ErrInvalidEvent = errors.New("wagg: invalid event")
)

type Agg struct {
	mu                                           sync.RWMutex
	size, delay, late, early, wm, maxTS, dropped int64
	maxOpen, inspected                           int
	wmValid, seen                                bool // wmValid=false 表示水位线为负无穷
	open                                         map[wkey]*winState
	pq                                           endHeap // 未清除窗口，按 end 有序
	log                                          []Change
}

func New(size, delay, lateness, early int64, maxOpen int) (*Agg, error) {
	if size <= 0 || delay < 0 || lateness < 0 || early <= 0 {
		return nil, ErrInvalidParam
	}
	return &Agg{size: size, delay: delay, late: lateness, early: early,
		maxOpen: maxOpen, open: map[wkey]*winState{}}, nil
}

// Feed 原子喂入：任一条非法，或执行中未清除窗口数会超过 maxOpen，整批不生效。
func (a *Agg) Feed(evs []Event) ([]Change, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, e := range evs {
		if e.Key == "" {
			return nil, ErrInvalidEvent
		}
	}
	if a.maxOpen > 0 && a.peakOpen(evs) > a.maxOpen {
		return nil, ErrTooManyOpen
	}
	out := []Change{}
	emit := func(c Change) { out = append(out, c); a.log = append(a.log, c) }
	for _, e := range evs {
		if a.observe(e.TS) {
			a.advance(a.wm, false, emit)
		}
		a.apply(e, emit)
	}
	return out, nil
}

func (a *Agg) peakOpen(evs []Event) int {
	c := &Agg{size: a.size, delay: a.delay, late: a.late, early: a.early,
		wm: a.wm, maxTS: a.maxTS, wmValid: a.wmValid, seen: a.seen,
		open: map[wkey]*winState{}}
	for k, s := range a.open {
		s2 := *s
		c.open[k] = &s2
		pushHeap(&c.pq, &s2)
	}
	peak := len(c.open)
	for _, e := range evs {
		if c.observe(e.TS) {
			c.advance(c.wm, false, func(Change) {})
		}
		c.apply(e, func(Change) {})
		peak = max(peak, len(c.open))
	}
	return peak
}

func (a *Agg) apply(e Event, emit func(Change)) {
	w := win.Assign(e.TS, a.size)
	st := a.open[wkey{e.Key, w.Start}]
	if st == nil {
		if win.PurgeDue(a.wm, a.wmValid, w.End, a.late) {
			a.dropped++
			return
		}
		st = &winState{key: e.Key, start: w.Start, end: w.End, count: 1}
		a.open[wkey{e.Key, w.Start}] = st
		pushHeap(&a.pq, st)
		ontime := win.OnTimeDue(a.wm, a.wmValid, w.End)
		if ontime || win.CanEarly(a.wm, a.wmValid, w.End) && a.early == 1 {
			st.fired = ontime // 补建旧窗只补 on-time 这一次；early==1 时首事件即快照
			emit(Change{true, e.Key, w.Start, w.End, 1})
		}
		return
	}
	if st.fired {
		if a.wmValid && a.wm >= st.end+a.late {
			a.dropped++
			return
		}
		st.count++
		emit(Change{false, e.Key, w.Start, w.End, st.count - 1})
		emit(Change{true, e.Key, w.Start, w.End, st.count})
		return
	}
	st.count++
	if win.CanEarly(a.wm, a.wmValid, st.end) && win.EarlyDue(st.count-1, st.count, a.early) {
		emit(Change{true, e.Key, w.Start, w.End, st.count})
	}
}

func (a *Agg) observe(ts int64) bool {
	if !a.seen {
		a.seen, a.maxTS, a.wm, a.wmValid = true, ts, ts-a.delay, true
		return true
	}
	if ts > a.maxTS {
		a.maxTS, a.wm = ts, ts-a.delay
		return true
	}
	return false
}

func (a *Agg) advance(to int64, flush bool, emit func(Change)) {
	for a.inspected = 0; a.pq.Len() > 0; {
		a.inspected++
		top := a.pq[0]
		if !top.fired && (flush || win.OnTimeDue(to, true, top.end)) {
			top.fired = true
			emit(Change{true, top.key, top.start, top.end, top.count})
		}
		if top.fired && (flush || to >= top.end+a.late) {
			removeHeap(&a.pq, top)
			delete(a.open, wkey{top.key, top.start})
			continue
		}
		break
	}
}

func (a *Agg) Dropped() int64 { a.mu.RLock(); defer a.mu.RUnlock(); return a.dropped }
func (a *Agg) Flush() []Change {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.seen, a.wmValid, a.maxTS, a.wm = true, true, math.MaxInt64, math.MaxInt64
	out := []Change{}
	a.advance(math.MaxInt64, true, func(c Change) { out = append(out, c); a.log = append(a.log, c) })
	return out
}
