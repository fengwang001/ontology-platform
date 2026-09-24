// Package api 对外提供全或无的原子批次物化器。依赖 plan。
package api

import (
	"errors"
	"fmt"
	"maps"
	"sync"

	"ontology/ev"
	"ontology/plan"
)

// ErrEmptyBatch 表示 ApplyBatch 收到了零个事件的批次。
var ErrEmptyBatch = errors.New("api: empty batch")

// Event 是对外事件类型，与 ev.Event 一致。
type Event = ev.Event

// Put、Del 构造事件；expect 为 nil 表示要求键当前不存在。
func Put(key, val string, expect *string) Event { return ev.Put(key, val, expect) }
func Del(key string, expect *string) Event      { return ev.Del(key, expect) }

// Engine 是原子批次物化器，并发安全。
type Engine struct {
	mu   sync.RWMutex
	view map[string]string
	rec  plan.Rehearser
}

// New 返回一个空视图的引擎。
func New() *Engine { return &Engine{view: map[string]string{}} }

// ApplyBatch 预演整批事件：全部通过才一次性应用到可见视图，
// 任一失败则视图逐键不变，并返回对应第一条失败事件的可判定错误。
func (e *Engine) ApplyBatch(evs []Event) error {
	if len(evs) == 0 {
		return ErrEmptyBatch
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	changes, err := e.rec.Rehearse(evs, func(k string) (string, bool) {
		v, ok := e.view[k]
		return v, ok
	})
	if err != nil {
		return err
	}
	for _, c := range changes { // 全部通过后才一次性生效
		if c.Del {
			delete(e.view, c.Key)
		} else {
			e.view[c.Key] = c.Val
		}
	}
	return nil
}

// View 返回当前可见视图的快照副本。
func (e *Engine) View() map[string]string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return maps.Clone(e.view)
}

// SelfCheck 对一组内置批序列核验四条不变量，全部通过返回 nil。
func (e *Engine) SelfCheck() error {
	eng := New()
	naive := map[string]string{} // 朴素逐事件应用器（不变量 3 参照）
	s1, s2, s10, sx := "1", "2", "10", "x"
	batches := [][]Event{
		{Put("k1", "1", nil), Put("k2", "2", nil)},                   // 建初态
		{Put("k1", "10", &s1), Del("k2", &s2), Put("k2", "20", nil)}, // 批内删后重建
		{Put("k1", "x", &s10), Put("k1", "y", &sx)},                  // (丙) 批内链式期望
	}
	for i, b := range batches {
		if err := eng.ApplyBatch(b); err != nil {
			return fmt.Errorf("selfcheck batch %d: %w", i, err)
		}
		for _, evv := range b { // 朴素器只重放成功批
			if evv.Kind == ev.DelKind {
				delete(naive, evv.Key)
			} else {
				naive[evv.Key] = evv.Val
			}
		}
	}
	if !maps.Equal(eng.View(), naive) { // 不变量 2、3
		return errors.New("selfcheck: view diverges from naive reference")
	}
	before := eng.View()
	if err := eng.ApplyBatch([]Event{Del("k1", nil)}); !errors.Is(err, ev.ErrExpectation) {
		return errors.New("selfcheck: rejected batch not reported as expectation failure")
	}
	if !maps.Equal(eng.View(), before) { // 不变量 1、4：失败不留痕
		return errors.New("selfcheck: rejected batch mutated the view")
	}
	if e == nil {
		return errors.New("selfcheck: nil receiver")
	}
	return nil
}
