// Package check 提供朴素 FIFO 参照与结果比对，供测试和 demo 钉住 queue 的行为。
package check

import "ontology/queue"

// Ref 是切片模拟的朴素 FIFO 队列，作为正确性参照。
type Ref[T any] struct {
	data []T
}

// Enqueue 追加到队尾。
func (r *Ref[T]) Enqueue(v T) {
	r.data = append(r.data, v)
}

// Dequeue 移除并返回队头；空队返回零值、false。
func (r *Ref[T]) Dequeue() (T, bool) {
	var zero T
	if len(r.data) == 0 {
		return zero, false
	}
	v := r.data[0]
	r.data = r.data[1:]
	return v, true
}

// Peek 返回队头但不移除；空队返回零值、false。
func (r *Ref[T]) Peek() (T, bool) {
	var zero T
	if len(r.data) == 0 {
		return zero, false
	}
	return r.data[0], true
}

// Len 返回参照队列长度。
func (r *Ref[T]) Len() int { return len(r.data) }

// Match 对 q 执行 n 次操作并与参照逐一比对；ops 中 true=Enqueue。
// 返回全部结果是否一致以及执行的操作总数。
func Match(q *queue.Queue[int], n int, seed int64) (bool, int) {
	rng := newRng(seed)
	ref := &Ref[int]{}
	ok := true
	for i := 0; i < n; i++ {
		if rng.intn(2) == 0 || (ref.Len() == 0 && rng.intn(2) == 0) {
			v := rng.intn(1 << 30)
			ref.Enqueue(v)
			_ = q.Enqueue(v)
			continue
		}
		gv, gok := q.Dequeue()
		rv, rok := ref.Dequeue()
		if gv != rv || gok != rok || q.Len() != ref.Len() {
			ok = false
		}
	}
	return ok, n
}

// rng 是极简确定性 LCG，避免引入 math/rand 之外的讨论面。
type rng struct{ state uint64 }

func newRng(seed int64) *rng { return &rng{state: uint64(seed) | 1} }

func (r *rng) intn(n int) int {
	r.state = r.state*6364136223846793005 + 1442695040888963407
	return int((r.state >> 33) % uint64(n))
}
