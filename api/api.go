// Package api 对外提供基于事件时间水位线的去重窗口。
package api

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	"ontology/dedup"
	"ontology/dwin"
)

type Event struct {
	ID string
	TS int64
}

var ErrInvalidConfig = errors.New("dedup api: invalid config (ttl>0, delay>=0, maxIDs>0 required)")
var ErrInvalidEvent = errors.New("dedup api: invalid event (empty ID)")
var ErrMemLimit = errors.New("dedup api: memory size exceeds maxIDs")
var errSelfCheck = errors.New("dedup api: self-check failed")

type Window struct { // 并发安全；em 为全部历史已输出的新事件
	mu     sync.RWMutex
	d      *dedup.Dedup
	maxIDs int
	em     []Event
}

func New(ttl, delay int64, maxIDs int) (*Window, error) {
	if ttl <= 0 || delay < 0 || maxIDs <= 0 {
		return nil, ErrInvalidConfig
	}
	return &Window{d: dedup.New(ttl, delay), maxIDs: maxIDs}, nil
}

// Feed 处理一批事件并返回本批新事件；任一条被拒则整批回滚，①②③④ 在 Process 内按序。
func (w *Window) Feed(evs []Event) ([]Event, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	st, mark := w.d.Snapshot(), len(w.em)
	out := make([]Event, 0, len(evs))
	for i := range evs {
		if evs[i].ID == "" {
			w.rollback(st, mark)
			return nil, ErrInvalidEvent
		}
		dup := w.d.Process(evs[i].ID, evs[i].TS)
		if w.d.Len() > w.maxIDs {
			w.rollback(st, mark)
			return nil, ErrMemLimit
		}
		if !dup {
			w.em = append(w.em, evs[i])
			out = append(out, evs[i])
		}
	}
	return out, nil
}

func (w *Window) rollback(st dedup.State, mark int) { w.d.Restore(st); w.em = w.em[:mark] }

func (w *Window) Emitted() []Event { w.mu.RLock(); defer w.mu.RUnlock(); return slices.Clone(w.em) }
func (w *Window) Dups() int64      { w.mu.RLock(); defer w.mu.RUnlock(); return w.d.Dups() }
func (w *Window) Mem() map[string]int64 {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.d.Mem()
}

// Watermark 在一条事件都没见过时返回 dwin.NegInf。
func (w *Window) Watermark() int64 { w.mu.RLock(); defer w.mu.RUnlock(); return w.d.WM() }

// SelfCheck 以第三节十步内置序列核验四不变量并委托亚线性扫描自检，可并发调用。
func (w *Window) SelfCheck() error {
	if naiveCheck(10, 2, builtinSeq) != nil || rejectedNoTrace() != nil {
		return errSelfCheck
	}
	return dedup.SelfCheck()
}

var builtinSeq = []Event{{"a", 5}, {"b", 8}, {"a", 9}, {"c", 17}, {"a", 14}, {"b", 20}, {"d", 4}, {"d", 6}, {"c", 26}, {"a", 25}} // 第三节十条事件

// naiveCheck 以朴素参照钉住不变量 1/2/3：参照每 ID 只留最近首见（与保留全部历史等价）。
func naiveCheck(ttl, delay int64, seq []Event) error {
	w, _ := New(ttl, delay, 1<<20)
	ref := map[string]int64{}
	var em []Event
	maxTS, prev, nd := int64(dwin.NegInf), int64(dwin.NegInf), int64(0)
	for _, e := range seq {
		out, err := w.Feed([]Event{e})
		if e.TS > maxTS {
			maxTS = e.TS
		}
		wm := maxTS - delay
		if err != nil || len(out) > 1 || wm < prev || w.Watermark() != wm {
			return errSelfCheck
		}
		prev = wm
		ts, had := ref[e.ID]
		dup := had && !dwin.Expired(wm, ts, ttl)
		if dup != (len(out) == 0) {
			return errSelfCheck
		}
		if dup {
			nd++
		} else {
			ref[e.ID], em = e.TS, append(em, e)
		}
		want := map[string]int64{}
		for id, t := range ref {
			if !dwin.Expired(wm, t, ttl) {
				want[id] = t
			}
		}
		if state(w) != fmt.Sprintf("%d|%v|%d|%v", wm, want, nd, em) {
			return errSelfCheck
		}
	}
	return nil
}

func rejectedNoTrace() error {
	for _, c := range [][3]int64{{0, 0, 1}, {1, -1, 1}, {1, 0, 0}} {
		if _, e := New(c[0], c[1], int(c[2])); !errors.Is(e, ErrInvalidConfig) {
			return errSelfCheck
		}
	}
	w, _ := New(1000, 0, 2)
	if _, e := w.Feed([]Event{{"a", 0}, {"b", 1}}); e != nil {
		return errSelfCheck
	}
	saved := state(w)
	cases := [][]Event{{{"", 2}}, {{"c", 2}}, {{"a", 3}, {"", 4}}}
	want := []error{ErrInvalidEvent, ErrMemLimit, ErrInvalidEvent}
	for i, evs := range cases {
		if _, e := w.Feed(evs); !errors.Is(e, want[i]) || state(w) != saved {
			return errSelfCheck
		}
	}
	if _, e := w.Feed([]Event{{"a", 1}}); e != nil || w.Dups() != 1 {
		return errSelfCheck
	}
	return nil
}

func state(w *Window) string { // 汇总四项可观测状态用于整体比对
	return fmt.Sprintf("%d|%v|%d|%v", w.Watermark(), w.Mem(), w.Dups(), w.Emitted())
}
