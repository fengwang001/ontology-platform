package seqno

import "sort"

// Set 跟踪单个成员已处理的序号集合与其本地检查点。
//
// 内部表示为「lcp + lcp 之上的稀疏洞集合 holes」：
// 1..lcp 全部已处理；holes 记录 lcp 以上已处理但尚未连成前缀的序号。
// Observe 时每个序号至多使 lcp 增加若干次，且每次增加对应一个
// 此前从未计入前缀的序号，因此 steps 不超过首次 Observe 成功的次数。
type Set struct {
	lcp   int
	max   int
	holes map[int]struct{}
	steps int
}

// NewSet 创建空集合。
func NewSet() *Set { return &Set{holes: map[int]struct{}{}} }

// Observe 记录成员已处理 seq（seq>=1）；重复记录返回 false。
func (s *Set) Observe(seq int) (newly bool) {
	if seq <= s.lcp {
		return false
	}
	if _, ok := s.holes[seq]; ok {
		return false
	}
	s.holes[seq] = struct{}{}
	if seq > s.max {
		s.max = seq
	}
	for {
		next := s.lcp + 1
		if _, ok := s.holes[next]; !ok {
			break
		}
		delete(s.holes, next)
		s.lcp = next
		s.steps++
	}
	return true
}

// Contains 报告 seq 是否已处理。
func (s *Set) Contains(seq int) bool {
	if seq <= s.lcp {
		return seq >= 1
	}
	_, ok := s.holes[seq]
	return ok
}

// LCP 返回本地检查点：使 1..k 全部已处理的最大 k。
func (s *Set) LCP() int { return s.lcp }

// Max 返回已处理的最大序号（无则 0）。
func (s *Set) Max() int { return s.max }

// Holes 返回 lcp+1..Max 之间尚未处理的序号（升序）。
func (s *Set) Holes() []int {
	holes := make([]int, 0)
	for seq := s.lcp + 1; seq <= s.max; seq++ {
		if _, ok := s.holes[seq]; !ok {
			holes = append(holes, seq)
		}
	}
	return holes
}

// Sorted 返回全部已处理序号（升序）。
func (s *Set) Sorted() []int {
	out := make([]int, 0, s.lcp+len(s.holes))
	for seq := 1; seq <= s.lcp; seq++ {
		out = append(out, seq)
	}
	for seq := range s.holes {
		out = append(out, seq)
	}
	sort.Ints(out)
	return out
}

// DiscardAbove 丢弃 seq 大于 g 的处理记录，返回丢弃序号数。
func (s *Set) DiscardAbove(g int) int {
	removed := 0
	if g < 0 {
		g = 0
	}
	for seq := range s.holes {
		if seq > g {
			delete(s.holes, seq)
			removed++
		}
	}
	if s.lcp > g {
		removed += s.lcp - g
		s.lcp = g
	}
	s.max = s.lcp
	for seq := range s.holes {
		if seq > s.max {
			s.max = seq
		}
	}
	return removed
}

// Steps 返回 lcp 累计推进的总步数（非导出计数器的导出只读视图，供验证）。
func (s *Set) Steps() int { return s.steps }
