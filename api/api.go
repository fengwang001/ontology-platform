// Package api 对外暴露滑动去重窗口：并发安全封装、视图与计数、自检。
package api

import (
	"fmt"
	"sync"

	"ontology/dedup"
)

// 哨兵错误（与 dedup 包同一组实例，三者互不相同）。
var (
	ErrEmptyID            = dedup.ErrEmptyID
	ErrNonPositiveWindow  = dedup.ErrNonPositiveWindow
	ErrNonPositiveMaxOpen = dedup.ErrNonPositiveMaxOpen
)

// Deduper 是并发安全的滑动去重窗口。
type Deduper struct {
	mu sync.Mutex
	t  *dedup.Table
}

func New(w int64, maxOpen int) (*Deduper, error) {
	t, err := dedup.New(w, maxOpen)
	if err != nil {
		return nil, err
	}
	return &Deduper{t: t}, nil
}

func (d *Deduper) Dedup(id string, ts int64) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.t.Dedup(id, ts)
}

func (d *Deduper) View() map[string]int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.t.View()
}

func (d *Deduper) Duplicated() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.t.Duplicated()
}

func (d *Deduper) Accepted() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.t.Accepted()
}

func (d *Deduper) SelfCheck() error { return selfCheck() }

type event struct {
	id string
	ts int64
}

// naive 是朴素参照：每 ID 只保留最近被接受的 TS，逐事件重放。
func naive(w int64, maxOpen int, evs []event) []bool {
	last := map[string]int64{}
	out := make([]bool, len(evs))
	for i, e := range evs {
		l, ok := last[e.id]
		if !ok {
			if len(last) >= maxOpen {
				minID, minLast, first := "", int64(0), true
				for k, v := range last {
					if first || v < minLast || (v == minLast && k < minID) {
						minID, minLast, first = k, v, false
					}
				}
				delete(last, minID)
			}
			last[e.id] = e.ts
			out[i] = true
			continue
		}
		if e.ts <= l || e.ts-l < w {
			continue
		}
		last[e.id] = e.ts
		out[i] = true
	}
	return out
}

// selfCheck 用内置序列（含确定性伪随机部分）核验四条不变量。
func selfCheck() error {
	const w, maxOpen = int64(5), 3
	evs := []event{{"X", 10}, {"X", 15}, {"Y", 20}, {"Z", 30}, {"Y", 24}, {"W", 40}, {"Y", 25}, {"Z", 32}}
	idn, ts := 0, int64(40)
	for i := 0; i < 200; i++ { // 确定性伪随机：含乱序、重复与大量淘汰
		idn = (idn*7 + 3) % 11
		ts += int64((i*13)%9) - 3
		evs = append(evs, event{fmt.Sprintf("id%d", idn), ts})
	}
	d, err := New(w, maxOpen)
	if err != nil {
		return err
	}
	want := naive(w, maxOpen, evs)
	lastAccept := map[string]int64{}
	for i, e := range evs {
		_, present := d.View()[e.id]
		got, err := d.Dedup(e.id, e.ts)
		if err != nil {
			return fmt.Errorf("selfcheck: event %d: %w", i, err)
		}
		if got != want[i] { // 不变量 2：与朴素参照一致
			return fmt.Errorf("selfcheck: event %d (%s,%d): stream=%v naive=%v", i, e.id, e.ts, got, want[i])
		}
		if got {
			if present && e.ts-lastAccept[e.id] < w { // 不变量 1：窗口不重叠
				return fmt.Errorf("selfcheck: overlapping window for %s", e.id)
			}
			lastAccept[e.id] = e.ts
		}
		if len(d.View()) > maxOpen { // 不变量 3：保留数不超限
			return fmt.Errorf("selfcheck: more than %d ids kept", maxOpen)
		}
	}
	if ok, _ := d.Dedup("X", 1000); !ok { // 不变量 3：被淘汰者按新事件
		return fmt.Errorf("selfcheck: evicted id not treated as new")
	}
	view, acc, dup := d.View(), d.Accepted(), d.Duplicated()
	if _, err := d.Dedup("", 1); err != ErrEmptyID { // 不变量 4：失败不留痕
		return fmt.Errorf("selfcheck: empty id err=%v", err)
	}
	if _, err := New(0, 1); err != ErrNonPositiveWindow {
		return fmt.Errorf("selfcheck: w<=0 err=%v", err)
	}
	if _, err := New(1, 0); err != ErrNonPositiveMaxOpen {
		return fmt.Errorf("selfcheck: maxOpen<=0 err=%v", err)
	}
	if d.Accepted() != acc || d.Duplicated() != dup || len(d.View()) != len(view) {
		return fmt.Errorf("selfcheck: state changed after rejection")
	}
	for k, v := range view {
		if d.View()[k] != v {
			return fmt.Errorf("selfcheck: last[%s] changed after rejection", k)
		}
	}
	return nil
}
