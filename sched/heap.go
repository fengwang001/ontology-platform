package sched

import "ontology/tenant"

// heap 是非空租户的最小堆：vt 小者优先，vt 相同按 ID 字典序。
// cmps 记录最近一次 Pop 过程中的比较次数（由外部加锁访问）。
type heap struct {
	items []*tenant.Tenant
	cmps  int
}

func (h *heap) len() int { return len(h.items) }

func (h *heap) less(i, j int) bool {
	a, b := h.items[i], h.items[j]
	if a.VT() != b.VT() {
		return a.VT() < b.VT()
	}
	return a.ID() < b.ID()
}

func (h *heap) swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }

func (h *heap) push(t *tenant.Tenant) {
	h.items = append(h.items, t)
	h.up(h.len() - 1)
}

func (h *heap) pop() *tenant.Tenant {
	n := h.len() - 1
	h.swap(0, n)
	h.down(0, n)
	t := h.items[n]
	h.items = h.items[:n]
	return t
}

// remove 从堆中移除指定租户（仅允许其队列为空时调用）。
func (h *heap) remove(t *tenant.Tenant) bool {
	for i, v := range h.items {
		if v == t {
			n := h.len() - 1
			if i != n {
				h.swap(i, n)
				h.down(i, n)
				h.up(i)
			}
			h.items = h.items[:n]
			return true
		}
	}
	return false
}

func (h *heap) up(j int) {
	for {
		i := (j - 1) / 2
		if i == j || !h.cmp(j, i) {
			return
		}
		h.swap(i, j)
		j = i
	}
}

func (h *heap) down(i0, n int) {
	i := i0
	for {
		l := 2*i + 1
		if l >= n {
			return
		}
		j := l
		if r := l + 1; r < n && h.cmp(r, l) {
			j = r
		}
		if !h.cmp(j, i) {
			return
		}
		h.swap(i, j)
		i = j
	}
}

// cmp 包装一次键比较并计数。
func (h *heap) cmp(i, j int) bool {
	h.cmps++
	return h.less(i, j)
}
