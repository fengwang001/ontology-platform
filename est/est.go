// Package est 实现支持增量加入与撤回的基数估计器：
// 稀疏模式用显式多集精确计数，不同键数首次超过阈值 S 后粘性切换为计数型 HLL。
package est

import (
	"errors"
	"sync"

	"ontology/hll"
)

var (
	// ErrInvalidP：p < 1。
	ErrInvalidP = errors.New("est: p must be >= 1")
	// ErrInvalidS：S < 0。
	ErrInvalidS = errors.New("est: S must be >= 0")
	// ErrEmptyKey：key 为空串。
	ErrEmptyKey = errors.New("est: key must not be empty")
	// ErrNotPresent：Remove 不存在的键。
	ErrNotPresent = errors.New("est: key not present")
)

// Estimator 是基数估计器。零值不可用，须用 New 创建。
type Estimator struct {
	mu      sync.Mutex
	p       int
	s       int
	refs    map[string]int // 稀疏模式：键 -> 出现次数
	sketch  *hll.Sketch    // 稠密模式草图（稀疏时为 nil）
	dense   bool           // 粘性：一旦转稠密永不回落
	touched int            // 非导出：稠密模式下最近一次 Add/Remove 修改的寄存器个数
}

// New 创建估计器；p < 1 报 ErrInvalidP，S < 0 报 ErrInvalidS。
func New(p, S int) (*Estimator, error) {
	if p < 1 {
		return nil, ErrInvalidP
	}
	if S < 0 {
		return nil, ErrInvalidS
	}
	return &Estimator{p: p, s: S, refs: make(map[string]int)}, nil
}

// Add 加入一次 key；空串报 ErrEmptyKey，状态不变。
func (e *Estimator) Add(key string) error {
	if key == "" {
		return ErrEmptyKey
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.dense {
		e.refs[key]++
		if len(e.refs) == e.s+1 { // 恰在 d 首次超过 S 时转稠密，且只此一次
			e.convert()
		}
		return nil
	}
	e.sketch.Add(key)
	e.touched = 1
	return nil
}

// Remove 撤回一次 key；空串报 ErrEmptyKey，键不存在报 ErrNotPresent，状态不变。
func (e *Estimator) Remove(key string) error {
	if key == "" {
		return ErrEmptyKey
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.dense {
		n, ok := e.refs[key]
		if !ok {
			return ErrNotPresent
		}
		if n == 1 {
			delete(e.refs, key)
		} else {
			e.refs[key] = n - 1
		}
		return nil
	}
	if err := e.sketch.Remove(key); err != nil {
		return ErrNotPresent
	}
	e.touched = 1
	return nil
}

// Card 返回当前基数估计：稀疏模式为精确不同键数，稠密模式为 HLL 估计。
func (e *Estimator) Card() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.dense {
		return float64(len(e.refs))
	}
	return e.sketch.Card()
}

// Dense 报告当前是否处于稠密模式。
func (e *Estimator) Dense() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.dense
}

// Ranks 返回稠密模式的寄存器秩数组副本；稀疏模式返回 nil。
func (e *Estimator) Ranks() []int {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.dense {
		return nil
	}
	return e.sketch.Ranks()
}

// convert 用当前全部不同键构建计数型 HLL 并丢弃 refs（只在 d==S+1 时调用一次）。
func (e *Estimator) convert() {
	sk, err := hll.New(e.p)
	if err != nil { // p 已在 New 校验，不会发生
		panic(err)
	}
	for k := range e.refs {
		sk.Add(k)
	}
	e.sketch = sk
	e.refs = nil
	e.dense = true
}
