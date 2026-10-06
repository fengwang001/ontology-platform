package smartlocker

import (
	"container/heap"
	"math"
)

type cooledCode struct {
	code        string
	availableAt int64
	index       int
}

type cooledCodeHeap []*cooledCode

func (h cooledCodeHeap) Len() int { return len(h) }

func (h cooledCodeHeap) Less(i, j int) bool {
	if h[i].availableAt != h[j].availableAt {
		return h[i].availableAt < h[j].availableAt
	}
	return codeNumber(h[i].code) < codeNumber(h[j].code)
}

func (h cooledCodeHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *cooledCodeHeap) Push(value any) {
	item := value.(*cooledCode)
	item.index = len(*h)
	*h = append(*h, item)
}

func (h *cooledCodeHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

type codePool struct {
	active  map[string]bool
	cooling cooledCodeHeap
	next    uint64
}

func newCodePool() *codePool {
	pool := &codePool{active: make(map[string]bool)}
	heap.Init(&pool.cooling)
	return pool
}

func (pool *codePool) allocate(at int64) string {
	if pool.cooling.Len() > 0 && pool.cooling[0].availableAt <= at {
		item := heap.Pop(&pool.cooling).(*cooledCode)
		pool.active[item.code] = true
		return item.code
	}
	pool.next++
	code := formatCodeNumber(pool.next)
	pool.active[code] = true
	return code
}

func (pool *codePool) release(code string, at, cooldown int64) {
	if !pool.active[code] {
		return
	}
	delete(pool.active, code)
	availableAt := at + cooldown
	if availableAt < at {
		availableAt = math.MaxInt64
	}
	heap.Push(&pool.cooling, &cooledCode{code: code, availableAt: availableAt})
}
