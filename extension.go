package ontology

import "container/heap"

// TargetKind 区分延期授予对象：个人或小组。
type TargetKind int

const (
	PersonalTarget TargetKind = iota // 个人延期，只影响该成员的扣分
	GroupTarget                      // 小组延期，对全体成员生效
)

// grant 是一次延期授权。撤销只影响此后的版本，已产生的版本判定不回溯。
type grant struct {
	id       int64
	kind     TargetKind
	target   string
	duration int64
	revoked  bool
}

// extSet 维护同一主体当前生效的延期时长集合，支持 O(1) 摊还的最大值查询：
// 用最大堆加 counts 懒删除，查询只读堆顶；堆中陈旧元素总数不超过历史授权总数，
// 因此查询开销不随延期总数增长（每次弹出都由一次先前的授权支付）。
type extSet struct {
	counts map[int64]int // 时长 -> 生效中的授权次数
	h      durationHeap  // 最大堆，允许含已撤销的陈旧元素
}

func newExtSet() *extSet {
	return &extSet{counts: map[int64]int{}}
}

func (s *extSet) add(d int64) {
	s.counts[d]++
	heap.Push(&s.h, d)
}

func (s *extSet) remove(d int64) {
	if s.counts[d] <= 1 {
		delete(s.counts, d)
	} else {
		s.counts[d]--
	}
	// 堆中元素留待 max 时懒删除。
}

// max 返回当前生效延期的最大时长，无则 0。
func (s *extSet) max() int64 {
	m, _ := s.maxProbed()
	return m
}

// maxProbed 同 max，但返回本次查询弹出的陈旧堆元素个数，供测试验证查询复杂度。
func (s *extSet) maxProbed() (int64, int) {
	pops := 0
	for len(s.h) > 0 {
		top := s.h[0]
		if s.counts[top] > 0 {
			return top, pops
		}
		heap.Pop(&s.h)
		pops++
	}
	return 0, pops
}

// durationHeap 是 int64 最大堆。
type durationHeap []int64

func (h durationHeap) Len() int           { return len(h) }
func (h durationHeap) Less(i, j int) bool { return h[i] > h[j] }
func (h durationHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *durationHeap) Push(x any)        { *h = append(*h, x.(int64)) }
func (h *durationHeap) Pop() any {
	old := *h
	n := len(old)
	v := old[n-1]
	*h = old[:n-1]
	return v
}
