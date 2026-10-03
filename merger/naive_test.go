package merger

import (
	"sort"
)

// naiveMerger 是按规格直接写成的朴素模拟：每次唤醒都全表扫描，
// AdvanceTo 逐时刻推进、每时刻检查是否满足唤醒条件（tau >= e 且 tau >= last+g）。
// 它用于与优化实现（索引堆 + 增量维护）逐操作对拍。
type naiveMerger struct {
	g, batch int64
	timers   map[string]*timer
	last     int64
	hasLast  bool
	stats    Stats
}

func newNaive(g, batch int64) *naiveMerger {
	return &naiveMerger{g: g, batch: batch, timers: make(map[string]*timer)}
}

func (nm *naiveMerger) add(id string, p, s, n int64) error {
	if !validParams(id, p, s, n) {
		return ErrInvalidParam
	}
	if _, ok := nm.timers[id]; ok {
		return ErrDuplicate
	}
	if n < nm.last {
		return ErrPastNominal
	}
	if len(nm.timers) >= maxTimers {
		return ErrFull
	}
	nm.timers[id] = &timer{id: id, p: p, s: s, n: n}
	return nil
}

func (nm *naiveMerger) remove(id string) error {
	if !validID(id) {
		return ErrInvalidParam
	}
	if _, ok := nm.timers[id]; !ok {
		return ErrNotFound
	}
	delete(nm.timers, id)
	return nil
}

// next 全表扫描取 e = min(n+s)，再与 last+g 取大。
func (nm *naiveMerger) next() (int64, error) {
	if len(nm.timers) == 0 {
		return 0, ErrNoTimers
	}
	first := true
	var e int64
	for _, tm := range nm.timers {
		if end := tm.n + tm.s; first || end < e {
			e, first = end, false
		}
	}
	if nm.hasLast && nm.last+nm.g > e {
		return nm.last + nm.g, nil
	}
	return e, nil
}

// wake 全表扫描候选（n <= w），排序后取前 B 个触发。
func (nm *naiveMerger) wake() WakeResult {
	w, _ := nm.next()
	var cand []*timer
	for _, tm := range nm.timers {
		if tm.n <= w {
			cand = append(cand, tm)
		}
	}
	sort.Slice(cand, func(i, j int) bool { return lessEnd(cand[i], cand[j]) })
	fire := int(nm.batch)
	if len(cand) < fire {
		fire = len(cand)
	}
	res := WakeResult{W: w, Left: len(cand) - fire}
	for _, tm := range cand[:fire] {
		late := w - (tm.n + tm.s)
		if late < 0 {
			late = 0
		}
		k := (w - tm.n) / tm.p
		tm.n += (k + 1) * tm.p
		res.Fired = append(res.Fired, Fired{ID: tm.id, Late: late, K: k})
		nm.stats.Fired++
		nm.stats.Skipped += k
		if late > 0 {
			nm.stats.Late++
		}
	}
	nm.last = w
	nm.hasLast = true
	nm.stats.Wakes++
	return res
}

// advanceTo 逐时刻推进：从当前时钟起每个时刻 tau 检查是否可唤醒
// （tau >= e 且 tau >= last+g），满足即唤醒并继续从同一时刻检查
// （g=0 时同一时刻可能连续唤醒），单次调用至多 1e5 次。
func (nm *naiveMerger) advanceTo(t int64) ([]WakeResult, error) {
	if t < 0 || t > maxT {
		return nil, ErrInvalidParam
	}
	if t < nm.last {
		return nil, ErrClockBack
	}
	if len(nm.timers) == 0 {
		return nil, ErrNoTimers
	}
	var out []WakeResult
	tau := nm.last
	for tau <= t && len(out) < maxAdvanceWakes {
		w, _ := nm.next()
		if tau >= w {
			out = append(out, nm.wake())
			continue // 唤醒后仍在同一时刻，重新检查
		}
		tau = w // w 之前不可能有唤醒，直接跳到下一候选时刻
	}
	return out, nil
}
