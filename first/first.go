// Package first 维护活跃事件多重集，并按 (TS, Key) 序增量维护首值（最小堆）。
// 依赖 evt，不依赖 api。
package first

import "ontology/evt"

// Set 是活跃事件多重集：counts 记每种 (Key,TS) 的出现次数，
// heap 为每次出现压入一项的最小堆；计数归零的堆项在到顶时惰性弹出。
type Set struct {
	counts  map[evt.Event]int
	heap    []evt.Event
	n       int // 活跃出现总数
	lastCmp int // 最近一次 Add/Remove 在堆上 下沉/上浮 的比较次数（非导出）
}

// NewSet 创建空多重集。
func NewSet() *Set { return &Set{counts: map[evt.Event]int{}} }

// Add 加入一次出现，并沿堆高上浮；lastCmp 记录本次上浮比较次数。
func (s *Set) Add(e evt.Event) {
	s.heap = append(s.heap, e)
	s.lastCmp = s.bubble(len(s.heap) - 1)
	s.counts[e]++
	s.n++
}

// Remove 撤掉一次出现（调用方须保证该出现存在），随后立即把计数为 0 的
// 过期堆顶逐层下沉弹出，使次首立刻补位；lastCmp 记录本次下沉比较次数。
func (s *Set) Remove(e evt.Event) {
	s.counts[e]--
	s.n--
	if s.counts[e] == 0 {
		delete(s.counts, e)
	}
	s.lastCmp = s.prune()
}

// Has 报告某事件当前是否至少有一次出现。
func (s *Set) Has(e evt.Event) bool { return s.counts[e] > 0 }

// Len 返回活跃出现总数。
func (s *Set) Len() int { return s.n }

// First 返回当前首值；无活跃事件时 ok=false。纯只读：每次 Add/Remove
// 返回前都已把过期堆顶 prune 干净，故非空时 heap[0] 必为活跃全局最小。
func (s *Set) First() (evt.Event, bool) {
	if len(s.heap) == 0 {
		return evt.Event{}, false
	}
	return s.heap[0], true
}

// prune 弹出所有计数为 0 的堆顶，返回下沉过程的比较次数。
func (s *Set) prune() int {
	cmp := 0
	for len(s.heap) > 0 {
		top := s.heap[0]
		if s.counts[top] > 0 {
			break // 堆顶活跃 => 全局最小，停止
		}
		last := len(s.heap) - 1
		s.heap[0] = s.heap[last]
		s.heap = s.heap[:last]
		if last > 0 {
			cmp += s.sink(0, last)
		}
	}
	return cmp
}

// bubble 把下标 i 的元素上浮到合法位置，返回父子比较次数。
func (s *Set) bubble(i int) int {
	cmp := 0
	for i > 0 {
		p := (i - 1) / 2
		cmp++
		if !evt.Less(s.heap[i], s.heap[p]) {
			break
		}
		s.heap[i], s.heap[p] = s.heap[p], s.heap[i]
		i = p
	}
	return cmp
}

// sink 把下标 i 的元素在长度 n 的堆中下沉，返回比较次数。
func (s *Set) sink(i, n int) int {
	cmp := 0
	for {
		l := 2*i + 1
		if l >= n {
			break
		}
		j := l
		if r := l + 1; r < n {
			cmp++ // 两子择优
			if evt.Less(s.heap[r], s.heap[l]) {
				j = r
			}
		}
		cmp++ // 父与较小子比较
		if !evt.Less(s.heap[j], s.heap[i]) {
			break
		}
		s.heap[i], s.heap[j] = s.heap[j], s.heap[i]
		i = j
	}
	return cmp
}

// ceilLog2 返回不小于 log2(m) 的最小整数（m>=1）。
func ceilLog2(m int) int {
	k := 0
	for d := 1; d < m; d <<= 1 {
		k++
	}
	return k
}

// VerifyComplexity 供演示调用：多档 m 下末位 Add 的上浮比较次数必须
// 不超过 2·⌈log2 m⌉+2，只回结论，不暴露计数器数值。
func VerifyComplexity() bool {
	for _, m := range []int{100, 1000, 10000} {
		s := NewSet()
		for i := 0; i < m; i++ {
			s.Add(evt.Event{Key: "k", TS: int64(i)})
		}
		s.Add(evt.Event{Key: "zzz", TS: int64(m)}) // 新最大值，上浮立即停
		if s.lastCmp > 2*ceilLog2(m)+2 {
			return false
		}
		s.Add(evt.Event{Key: "aaa", TS: -1}) // 新最小值，沿堆高一直上浮
		if s.lastCmp > 2*ceilLog2(m)+2 {
			return false
		}
	}
	return true
}
