// Package dispatch 管理多个 key：按 key 路由 Feed，汇总各 key 发射序列、在途数与丢弃数。
// 依赖 fifo，不依赖 api。Hub 自带互斥锁，供 api 层并发使用。
package dispatch

import (
	"sync"

	"ontology/fifo"
)

// ErrBackpressure 与 fifo 的背压错误是同一个哨兵，api 层用 errors.Is 判定。
var ErrBackpressure = fifo.ErrBackpressure

// Hub 管理多个 key 的独立缓冲区。
type Hub struct {
	mu   sync.Mutex
	max  int
	keys map[string]*fifo.Buffer
}

// New 创建每个 key 缓冲上限为 maxInFlight 的多 key 管理器。
func New(maxInFlight int) *Hub {
	return &Hub{max: maxInFlight, keys: make(map[string]*fifo.Buffer)}
}

// get 取出（或惰性创建）某 key 的缓冲区；调用方须持锁。
func (h *Hub) get(key string) *fifo.Buffer {
	b, ok := h.keys[key]
	if !ok {
		b = fifo.New(h.max)
		h.keys[key] = b
	}
	return b
}

// Feed 把序号路由给对应 key，返回该 key 本次新发射的序号。
func (h *Hub) Feed(key string, seq int64) ([]int64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.get(key).Feed(seq)
}

// Emitted 返回某 key 已发射序列的升序副本 [1, next)；key 从未出现时为 nil。
func (h *Hub) Emitted(key string) []int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	b, ok := h.keys[key]
	if !ok {
		return nil
	}
	n := b.Next()
	out := make([]int64, 0, n-1)
	for i := int64(1); i < n; i++ {
		out = append(out, i)
	}
	return out
}

// Buffered 返回某 key 当前在途缓冲的数量。
func (h *Hub) Buffered(key string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if b, ok := h.keys[key]; ok {
		return b.Len()
	}
	return 0
}

// BufferedSnapshot 返回某 key 在途缓冲的升序副本（供自检/测试参照比对）。
func (h *Hub) BufferedSnapshot(key string) []int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if b, ok := h.keys[key]; ok {
		return b.Buffered()
	}
	return nil
}

// Dropped 返回所有 key 的重复丢弃总数。
func (h *Hub) Dropped() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	var total int64
	for _, b := range h.keys {
		total += b.Dropped()
	}
	return total
}
