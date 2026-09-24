// Package est 实现支持增量加入与撤回的基数估计器：
// 基数小用显式多集精确计数，超过阈值 S 转计数型 HLL，永不回落。
package est

import (
	"errors"
	"sync"

	"ontology/hll"
)

// ErrBadS 表示阈值 S 非法（S < 0）。
var ErrBadS = errors.New("est: S must be >= 0")

// Estimator 是基数估计器，可被多个 goroutine 并发使用。
type Estimator struct {
	mu      sync.Mutex
	p, s    int
	refs    map[string]int // 稀疏模式：键 → 出现次数
	dense   bool           // 粘性：一旦转稠密永不回落
	sketch  *hll.Sketch
	touched int // 非导出：稠密模式下最近一次 Add/Remove 修改的寄存器个数
}

// New 创建估计器；p < 1 或 S < 0 报可判定错误。
func New(p, S int) (*Estimator, error) {
	if S < 0 {
		return nil, ErrBadS
	}
	if _, err := hll.New(p); err != nil {
		return nil, err
	}
	return &Estimator{s: S, refs: make(map[string]int), p: p}, nil
}

// Add 加入一个键的一次出现。
func (e *Estimator) Add(key string) error {
	if key == "" {
		return hll.ErrEmptyKey
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.dense {
		e.touched = 1
		return e.sketch.Add(key)
	}
	e.refs[key]++
	if len(e.refs) == e.s+1 { // 首次超过 S：转稠密，粘性
		e.sketch, _ = hll.New(e.p)
		for k := range e.refs {
			e.sketch.Add(k)
		}
		e.refs = nil
		e.dense = true
	}
	return nil
}

// Remove 撤回一个键的一次出现；键不存在报可判定错误且不改状态。
func (e *Estimator) Remove(key string) error {
	if key == "" {
		return hll.ErrEmptyKey
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.dense {
		if err := e.sketch.Remove(key); err != nil {
			return err
		}
		e.touched = 1
		return nil
	}
	if e.refs[key] == 0 {
		return hll.ErrNotFound
	}
	e.refs[key]--
	if e.refs[key] == 0 {
		delete(e.refs, key)
	}
	return nil
}

// Card 返回当前基数估计：稀疏期为精确不同键数，稠密期为 HLL 估计。
func (e *Estimator) Card() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.dense {
		return e.sketch.Card()
	}
	return float64(len(e.refs))
}

// Dense 报告当前是否处于稠密模式。
func (e *Estimator) Dense() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.dense
}

// Ranks 返回稠密模式的寄存器秩数组；稀疏模式返回 nil。
func (e *Estimator) Ranks() []int {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.dense {
		return nil
	}
	return e.sketch.Ranks()
}
