// Package api 对外提供事件时间单调化输出引擎。依赖 buf。
package api

import (
	"errors"
	"math"
	"slices"
	"sort"
	"sync"

	"ontology/buf"
	"ontology/tm"
)

// 可判定的哨兵错误，三者互不相同。
var (
	ErrNegativeDelay  = errors.New("api: delay must be >= 0")
	ErrEmptyID        = errors.New("api: event ID must not be empty")
	ErrTooManyPending = errors.New("api: pending events exceed maxPending")
)

type Event = buf.Event
type Out = buf.Out

// Engine 是单调化输出引擎，并发安全。
type Engine struct {
	mu         sync.RWMutex
	buf        *buf.Buffer
	maxPending int
}

// New 构造引擎；delay < 0 时返回 ErrNegativeDelay。
func New(delay int64, maxPending int) (*Engine, error) {
	if delay < 0 {
		return nil, ErrNegativeDelay
	}
	return &Engine{buf: buf.New(tm.New(delay)), maxPending: maxPending}, nil
}

// Feed 喂入一批事件，返回本批发射项；整批先校验，任一不满足则整批不生效、状态不变。
func (e *Engine) Feed(evs []Event) ([]Out, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, ev := range evs {
		if ev.ID == "" {
			return nil, ErrEmptyID
		}
	}
	if e.buf.Pending()+len(evs) > e.maxPending {
		return nil, ErrTooManyPending
	}
	return e.buf.Feed(evs), nil
}

// Output 返回已发射序列的副本。
func (e *Engine) Output() []Out {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.buf.Output()
}

// WM 返回当前水位线（ok=false 表示负无穷）。
func (e *Engine) WM() (int64, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.buf.WM()
}

// Flush 排空缓冲区，返回本次发射项。
func (e *Engine) Flush() []Out {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.buf.Flush()
}

// SelfCheck 用内置事件序列核验四条不变量，全部通过返回 nil。可并发调用。
func (e *Engine) SelfCheck() error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return selfCheck()
}

func ev(ts int64, id string) Event { return Event{TS: ts, ID: id} }

// selfCheck 在全新的内部引擎上核验：朴素一致、输出单调、水位线单调、失败不留痕。
func selfCheck() error {
	seqs := [][]Event{
		{ev(10, "a"), ev(7, "b"), ev(12, "c"), ev(9, "d"), ev(5, "e"), ev(11, "f"), ev(8, "g")},
		{ev(3, "x"), ev(1, "y"), ev(2, "z")},
		{ev(5, "p"), ev(5, "q"), ev(4, "r"), ev(6, "s")},
	}
	for _, evs := range seqs { // 整批喂入 + Flush
		eng, err := New(2, len(evs))
		if err != nil {
			return err
		}
		if _, err := eng.Feed(evs); err != nil {
			return err
		}
		eng.Flush()
		got := eng.Output()
		if !slices.Equal(got, naive(evs)) { // 不变量 1：与朴素重算一致
			return errors.New("selfcheck: mismatch with naive recomputation")
		}
		for i, o := range got { // 不变量 2：输出单调且 outTS >= TS
			if o.OutTS < o.TS || (i > 0 && o.OutTS < got[i-1].OutTS) {
				return errors.New("selfcheck: output not monotonic")
			}
		}
	}
	eng, _ := New(3, 10) // 不变量 3：逐条喂入，水位线只进不退
	prev := int64(math.MinInt64)
	for _, e := range seqs[0] {
		if _, err := eng.Feed([]Event{e}); err != nil {
			return err
		}
		if wm, ok := eng.WM(); ok {
			if wm < prev {
				return errors.New("selfcheck: watermark regressed")
			}
			prev = wm
		}
	}
	eng2, _ := New(1, 2) // 不变量 4：失败不留痕
	_, err1 := eng2.Feed([]Event{ev(1, "a"), ev(2, "")})
	_, err2 := eng2.Feed([]Event{ev(1, "a"), ev(2, "b"), ev(3, "c")})
	_, err3 := New(-1, 1)
	if !errors.Is(err1, ErrEmptyID) || !errors.Is(err2, ErrTooManyPending) || !errors.Is(err3, ErrNegativeDelay) {
		return errors.New("selfcheck: rejection error mismatch")
	}
	if _, ok := eng2.WM(); ok || len(eng2.Output()) != 0 {
		return errors.New("selfcheck: rejected feed changed state")
	}
	return nil
}

// naive 朴素重算：按 TS 非递减（并列按到达序）排序后逐条上钳。
func naive(evs []Event) []Out {
	s := append([]Event(nil), evs...)
	sort.SliceStable(s, func(i, j int) bool { return s[i].TS < s[j].TS })
	outs := make([]Out, 0, len(s))
	for i, e := range s {
		o := Out{ID: e.ID, TS: e.TS, OutTS: e.TS}
		if i > 0 {
			o.OutTS = tm.Clamp(e.TS, outs[i-1].OutTS)
		}
		outs = append(outs, o)
	}
	return outs
}
