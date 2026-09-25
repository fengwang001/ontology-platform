// Package api 对外提供双流水位对齐器：校验、收尾、只读视图与自检。
package api

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"sync"

	"ontology/align"
)

// Event 是一条带事件时间的流事件，Stream 取 'A' 或 'B'。
type Event = align.Event

var ( // 三类可判定的哨兵错误，互不相同。
	ErrBadStream  = errors.New("align: stream must be 'A' or 'B'")
	ErrNegativeTS = errors.New("align: TS must be >= 0")
	ErrClosed     = errors.New("align: already closed")
)

// Aligner 并发安全。
type Aligner struct {
	mu      sync.Mutex
	al      *align.Aligner
	emitted []Event
	dropped int
	closed  bool
}

func New() *Aligner { return &Aligner{al: align.New()} }

// Feed 校验并投递一条事件，返回本次发出的事件；迟到事件被丢弃并计数。
// 任何被拒绝的调用都整体失败、不改变任何状态。
func (a *Aligner) Feed(ev Event) ([]Event, error) {
	if ev.Stream != 'A' && ev.Stream != 'B' {
		return nil, ErrBadStream
	}
	if ev.TS < 0 {
		return nil, ErrNegativeTS
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil, ErrClosed
	}
	out, ok := a.al.Feed(ev)
	if !ok {
		a.dropped++
		return nil, nil
	}
	a.emitted = append(a.emitted, out...)
	return out, nil
}

// Close 把两条流水位线推到 +inf 并清空缓冲；重复 Close 失败且不留痕。
func (a *Aligner) Close() ([]Event, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil, ErrClosed
	}
	a.closed = true
	out := a.al.Close()
	a.emitted = append(a.emitted, out...)
	return out, nil
}

// View 返回迄今已发出事件的副本。
func (a *Aligner) View() []Event {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Event(nil), a.emitted...)
}

// Dropped 返回迟到丢弃数。
func (a *Aligner) Dropped() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.dropped
}

// SelfCheck 用内置的八步事件核验四条不变量；在全新实例上运行，不碰接收者状态。
func (*Aligner) SelfCheck() error {
	ev := func(s byte, t int64) Event { return Event{Stream: s, TS: t} }
	seq := []Event{ev('A', 1), ev('A', 2), ev('B', 1), ev('A', 3), ev('B', 2), ev('B', 5), ev('A', 4), ev('A', 2)}
	want := []Event{ev('A', 1), ev('B', 1), ev('A', 2), ev('B', 2), ev('A', 3), ev('A', 4), ev('B', 5)}
	c := New()
	var all []Event
	var m [2]int64
	var seen [2]bool
	for i, e := range seq {
		out, err := c.Feed(e)
		if err != nil {
			return fmt.Errorf("selfcheck feed %d: %w", i, err)
		}
		k := int(e.Stream - 'A')
		seen[k], m[k] = true, max(m[k], e.TS)
		for _, o := range out { // 不变量 3：发出的 TS 不得超过当时 W
			if !seen[0] || !seen[1] || o.TS > min(m[0], m[1]) {
				return fmt.Errorf("selfcheck: premature emit %v at step %d", o, i)
			}
		}
		all = append(all, out...)
	}
	tail, err := c.Close()
	if err != nil {
		return fmt.Errorf("selfcheck close: %w", err)
	}
	all = append(all, tail...)
	if !slices.IsSortedFunc(all, func(x, y Event) int { return cmp.Compare(x.TS, y.TS) }) { // 不变量 2
		return fmt.Errorf("selfcheck: order broken: %v", all)
	}
	if !slices.Equal(all, want) || c.Dropped() != 1 { // 不变量 1：朴素重排
		return fmt.Errorf("selfcheck: got %v dropped %d", all, c.Dropped())
	}
	return checkRejects()
}

func checkRejects() error { // 不变量 4：三类拒绝可判定、互不相同且不留痕。
	c := New()
	if _, err := c.Feed(Event{Stream: 'A', TS: 2}); err != nil {
		return err
	}
	v0, d0 := c.View(), c.Dropped()
	if _, err := c.Feed(Event{Stream: 'X', TS: 1}); !errors.Is(err, ErrBadStream) {
		return fmt.Errorf("selfcheck bad stream: %v", err)
	}
	if _, err := c.Feed(Event{Stream: 'A', TS: -1}); !errors.Is(err, ErrNegativeTS) {
		return fmt.Errorf("selfcheck negative ts: %v", err)
	}
	if !slices.Equal(c.View(), v0) || c.Dropped() != d0 {
		return errors.New("selfcheck: rejected feed changed state")
	}
	if _, err := c.Close(); err != nil {
		return err
	}
	v1 := c.View()
	if _, err := c.Close(); !errors.Is(err, ErrClosed) {
		return fmt.Errorf("selfcheck re-close: %v", err)
	}
	if _, err := c.Feed(Event{Stream: 'B', TS: 1}); !errors.Is(err, ErrClosed) {
		return fmt.Errorf("selfcheck feed after close: %v", err)
	}
	if !slices.Equal(c.View(), v1) {
		return errors.New("selfcheck: post-close op changed state")
	}
	return nil
}
