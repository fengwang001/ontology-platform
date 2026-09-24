// Package api 是对外接口层：参数校验、整批原子生效、并发安全的只读访问、自检。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/cwagg"
)

// 可判定的哨兵错误，互不相同。
var (
	ErrSize     = errors.New("cntwin: size must be positive")
	ErrLateness = errors.New("cntwin: lateness must be non-negative")
	ErrPos      = errors.New("cntwin: pos must be non-negative")
	ErrKey      = errors.New("cntwin: key must be non-empty")
)

// Event 是一条上游变更：Pos 为元素在该 Key 序列中的位置，Val 为任意整数。
type Event struct {
	Key string
	Pos int64
	Val int64
}

// Fire 是一次窗口触发的输出。
type Fire = cwagg.Fire

// Engine 是计数翻滚窗口聚合引擎，只读方法可并发调用。
type Engine struct {
	mu  sync.RWMutex
	agg *cwagg.Agg
}

// New 创建引擎；size<=0 或 lateness<0 时失败且不产生任何状态。
func New(size, lateness int64) (*Engine, error) {
	if size <= 0 {
		return nil, ErrSize
	}
	if lateness < 0 {
		return nil, ErrLateness
	}
	return &Engine{agg: cwagg.New(size, lateness)}, nil
}

// Feed 应用一批元素，返回本批新触发的输出。任一条非法则整批不生效。
func (e *Engine) Feed(evs []Event) ([]Fire, error) {
	for _, ev := range evs { // 先整批校验，失败不留痕
		if ev.Pos < 0 {
			return nil, ErrPos
		}
		if ev.Key == "" {
			return nil, ErrKey
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Fire
	for _, ev := range evs {
		if f, fired := e.agg.Add(ev.Key, ev.Pos, ev.Val); fired {
			out = append(out, f)
		}
	}
	return out, nil
}

// Fired 返回迄今全部触发输出。
func (e *Engine) Fired() []Fire {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.agg.Fired()
}

// Dropped 返回迄今被丢弃的元素个数。
func (e *Engine) Dropped() int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.agg.Dropped()
}

// SelfCheck 用内置事件序列核验四条不变量，全部通过返回 nil。
func (e *Engine) SelfCheck() error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	eng, _ := New(5, 2)
	evs := []Event{ // 覆盖：多 Key、迟到接受、迟到丢弃、重复投递、已关闭窗口再投递
		{"a", 0, 10}, {"a", 1, 20}, {"a", 2, 30}, {"a", 3, 40}, {"a", 4, 50},
		{"a", 5, 60}, {"a", 9, 90}, {"a", 7, 70}, {"a", 1, 99}, {"a", 0, 1},
		{"b", 0, 1}, {"b", 2, 3}, {"b", 1, 2}, {"b", 4, 5}, {"b", 3, 4},
	}
	if _, err := eng.Feed(evs); err != nil {
		return err
	}
	// 不变量 1+3：独立重算接受集合并分组累加，与 Fired 逐窗口一致。
	type kw struct {
		key string
		win int64
	}
	acc, cnt, wm := map[kw]int64{}, map[kw]int64{}, map[string]int64{}
	seen := map[[2]interface{}]bool{}
	for _, ev := range evs {
		if seen[[2]interface{}{ev.Key, ev.Pos}] {
			continue
		}
		seen[[2]interface{}{ev.Key, ev.Pos}] = true
		k := kw{ev.Key, ev.Pos / 5}
		w := wm[ev.Key] - 1 // 水位偏移 1 存储，未见过则为 -1
		if cnt[k] == 5 || (ev.Pos < w && ev.Pos < w-2) {
			continue // 窗口已关闭，或迟到超出允许范围 → 丢弃
		}
		acc[k] += ev.Val
		cnt[k]++
		if ev.Pos > w {
			wm[ev.Key] = ev.Pos + 1
		}
	}
	fired := map[kw]bool{}
	for _, f := range eng.Fired() {
		k := kw{f.Key, f.Win}
		if fired[k] { // 不变量 2：每 (Key,窗口) 至多触发一次
			return fmt.Errorf("selfcheck: duplicate fire %+v", k)
		}
		fired[k] = true
		if acc[k] != f.Sum {
			return fmt.Errorf("selfcheck: sum mismatch %+v", k)
		}
	}
	// 不变量 4：拒绝不留痕——非法批次前后 Fired/Dropped 不变。
	nf, nd := len(eng.Fired()), eng.Dropped()
	for _, bad := range [][]Event{{{Key: "a", Pos: -1}}, {{Key: "", Pos: 0}}} {
		if _, err := eng.Feed(bad); err == nil {
			return errors.New("selfcheck: invalid batch accepted")
		}
	}
	if len(eng.Fired()) != nf || eng.Dropped() != nd {
		return errors.New("selfcheck: rejected batch mutated state")
	}
	for _, c := range []struct {
		s, l int64
		want error
	}{{0, 0, ErrSize}, {1, -1, ErrLateness}} {
		if _, err := New(c.s, c.l); err != c.want {
			return errors.New("selfcheck: bad constructor args accepted")
		}
	}
	return nil
}
