// Package api 对外门面：New/Feed/View/SelfCheck，依赖 wagg。
package api

import (
	"errors"
	"maps"
	"math"
	"sync"

	"ontology/wagg"
	"ontology/win"
)

// 哨兵错误再导出，便于调用方 errors.Is 判定。
var (
	ErrParam = wagg.ErrParam
	ErrDelay = wagg.ErrDelay
	ErrOrder = wagg.ErrOrder
)

// Event 与 Change 直接复用 wagg 的定义。
type (
	Event  = wagg.Event
	Change = wagg.Change
)

// Key 是物化视图的键：(Key, 窗口)。
type Key struct {
	Key        string
	Start, End int64
}

// Engine 持有聚合器与已输出的全部变更日志。
type Engine struct {
	mu  sync.RWMutex
	agg *wagg.Agg
	log []Change
}

// New 创建引擎；参数非法时返回哨兵错误。
func New(size, hop, delay int64) (*Engine, error) {
	a, err := wagg.New(size, hop, delay)
	if err != nil {
		return nil, err
	}
	return &Engine{agg: a}, nil
}

// Feed 喂一批事件，返回本批触发的变更日志；失败整批不留痕。
func (e *Engine) Feed(evs []Event) ([]Change, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	cs, err := e.agg.Feed(evs)
	if err != nil {
		return nil, err
	}
	e.log = append(e.log, cs...)
	return cs, nil
}

// Flush 触发所有余留窗口，返回追加的变更日志。
func (e *Engine) Flush() []Change {
	e.mu.Lock()
	defer e.mu.Unlock()
	cs := e.agg.Flush()
	e.log = append(e.log, cs...)
	return cs
}

// View 返回下游按顺序应用全部变更日志后的物化视图。
func (e *Engine) View() map[Key]int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	v := make(map[Key]int64, len(e.log))
	for _, c := range e.log {
		v[Key{Key: c.Key, Start: c.Start, End: c.End}] += c.Count
	}
	return v
}

// batchRecompute 批量重算：每个事件落入其全部 size/hop 个重叠窗口分组计数。
func batchRecompute(evs []Event, size, hop int64) map[Key]int64 {
	v := map[Key]int64{}
	for _, e := range evs {
		for _, w := range win.Windows(e.TS, size, hop) {
			v[Key{e.Key, w.Start, w.End}]++
		}
	}
	return v
}

// SelfCheck 对内置事件序列核验四条不变量，全部通过返回 nil。
// 只用局部新建引擎，不触碰接收者状态，可并发调用。
func (e *Engine) SelfCheck() error {
	seq := []Event{{Key: "b", TS: -5}, {Key: "b", TS: -1}} // 含负时间戳的第二 Key
	for _, ts := range []int64{6, 10, 12, 14, 16, 18, 20, 22} {
		seq = append(seq, Event{Key: "a", TS: ts})
	}
	eng, err := New(8, 4, 2)
	if err != nil {
		return err
	}
	log, err := eng.Feed(seq)
	if err != nil {
		return err
	}
	log = append(log, eng.Flush()...)
	want := batchRecompute(seq, 8, 4)
	if !maps.Equal(eng.View(), want) { // 不变量1：与批量重算一致
		return errors.New("selfcheck: view != batch recompute")
	}
	seen := map[Key]int64{}
	prevEnd := int64(math.MinInt64)
	for _, c := range log {
		k := Key{Key: c.Key, Start: c.Start, End: c.End}
		if _, dup := seen[k]; dup { // 不变量2：每窗口恰好一条 +
			return errors.New("selfcheck: duplicate change")
		}
		seen[k] = c.Count
		if c.End < prevEnd { // 不变量3：触发顺序 == end 升序
			return errors.New("selfcheck: trigger out of order")
		}
		prevEnd = c.End
	}
	if !maps.Equal(seen, want) {
		return errors.New("selfcheck: change count != final")
	}
	before := eng.View() // 不变量4：乱序被拒且不留痕
	if _, err = eng.Feed([]Event{{Key: "a", TS: 0}}); !errors.Is(err, ErrOrder) {
		return errors.New("selfcheck: out-of-order not rejected")
	}
	if !maps.Equal(eng.View(), before) {
		return errors.New("selfcheck: rejected feed mutated state")
	}
	if _, err = New(8, 0, 0); !errors.Is(err, ErrParam) {
		return errors.New("selfcheck: bad param not rejected")
	}
	if _, err = New(8, 4, -1); !errors.Is(err, ErrDelay) {
		return errors.New("selfcheck: negative delay not rejected")
	}
	return nil
}
