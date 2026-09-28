package sampler

import "sync"

// Sequence 是由调用方提供确定序列的随机数源。
// 游标只在成功返回随机数时前进；取值非法或序列用尽均不改变游标。
type Sequence struct {
	mu     sync.Mutex
	values []float64
	cursor int
}

// NewSequence 用给定随机数序列构造随机源。
func NewSequence(values []float64) *Sequence {
	copied := make([]float64, len(values))
	copy(copied, values)
	return &Sequence{values: copied}
}

// Next 返回序列中的下一个随机数；用尽时返回 ErrRandomsExhausted。
// 若下一个值不在 (0,1) 内则返回 ErrIllegalRandom，且游标保持不动。
func (q *Sequence) Next() (float64, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.cursor >= len(q.values) {
		return 0, ErrRandomsExhausted
	}
	v := q.values[q.cursor]
	if !(v > 0 && v < 1) {
		return 0, ErrIllegalRandom
	}
	q.cursor++
	return v, nil
}

// Consumed 返回游标位置（已取走的随机数个数）。
func (q *Sequence) Consumed() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.cursor
}

// Remaining 返回尚未取走的随机数个数。
func (q *Sequence) Remaining() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.values) - q.cursor
}
