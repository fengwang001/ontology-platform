// Package eng 维护位点展开引擎的状态：最近原始位点、展开值与事件计数。
// 依赖 wrap 包，并发安全。
package eng

import (
	"errors"
	"sync"

	"ontology/wrap"
)

var (
	// ErrRewind 表示位点倒退（r < prev 且未超过回卷阈值），被拒绝。
	ErrRewind = errors.New("eng: offset rewound")
	// ErrEmpty 表示尚未喂入任何位点时的查询。
	ErrEmpty = errors.New("eng: no offset fed yet")
)

// Engine 是位点展开引擎。用 New 构造。
type Engine struct {
	mu        sync.RWMutex
	threshold uint32
	hasPrev   bool
	prev      uint32
	pu        int64
	counts    map[wrap.Event]int
	lookback  int // 最近一次 Feed 为判定而回看/比较的位点个数，恒为 1（只看 prev）
}

// New 构造引擎；threshold 的合法性由上层 api 校验。
func New(threshold uint32) *Engine {
	return &Engine{threshold: threshold, counts: map[wrap.Event]int{}}
}

// Feed 喂入一个原始位点，返回分类事件。
// 倒退与溢出被拒绝：prev、pu、事件计数均不变。
func (e *Engine) Feed(r uint32) (wrap.Event, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lookback = 1 // 判定只与 prev 比较一次，O(1) 状态、不重放历史
	if !e.hasPrev {
		e.prev, e.pu, e.hasPrev = r, int64(r), true
		e.counts[wrap.First]++
		return wrap.First, nil
	}
	ev := wrap.Classify(e.prev, r, e.threshold)
	if ev == wrap.Rewind {
		return ev, ErrRewind
	}
	pu, err := wrap.Unwrap(e.pu, e.prev, r, ev)
	if err != nil {
		return ev, err
	}
	e.prev, e.pu = r, pu
	e.counts[ev]++
	return ev, nil
}

// LastUnwrapped 返回最近接受位点的展开值；空态返回 ErrEmpty。
func (e *Engine) LastUnwrapped() (int64, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if !e.hasPrev {
		return 0, ErrEmpty
	}
	return e.pu, nil
}

// LastRaw 返回最近接受的原始位点；空态返回 ErrEmpty。
func (e *Engine) LastRaw() (uint32, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if !e.hasPrev {
		return 0, ErrEmpty
	}
	return e.prev, nil
}

// Counts 返回事件计数的副本。
func (e *Engine) Counts() map[wrap.Event]int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make(map[wrap.Event]int, len(e.counts))
	for k, v := range e.counts {
		out[k] = v
	}
	return out
}
