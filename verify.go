package ontology

import (
	"fmt"
	"slices"
)

// Verify 自检内部不变量，全部通过时返回 nil，否则返回描述第一条
// 违例的错误。检查项：
//
//  1. 前缀和（即每一点的覆盖数）不出现负值；
//  2. Segments 视图按 Lo 升序、两两不重叠；
//  3. 视图中不存在 Count 为零的段；
//  4. 相邻段的 Count 互不相同（同 Count 的相邻段必须已合并）；
//  5. 视图与端点净增减量一致：视图可由 delta 重算得到。
func (c *Counter) Verify() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dirty {
		c.materializeLocked()
		c.dirty = false
	}
	for i, v := range c.pref {
		if v < 0 {
			return fmt.Errorf("ontology: negative coverage %d at endpoint %d", v, c.ends[i])
		}
	}
	segs := c.segmentsLocked()
	for i, s := range segs {
		if s.Count <= 0 {
			return fmt.Errorf("ontology: zero/negative segment %+v", s)
		}
		if s.Hi <= s.Lo {
			return fmt.Errorf("ontology: degenerate segment %+v", s)
		}
		if i == 0 {
			continue
		}
		prev := segs[i-1]
		if s.Lo < prev.Hi {
			return fmt.Errorf("ontology: overlapping segments %+v and %+v", prev, s)
		}
		if s.Lo == prev.Hi && s.Count == prev.Count {
			return fmt.Errorf("ontology: adjacent equal-count segments %+v and %+v", prev, s)
		}
	}
	want := segmentsFromDelta(c.delta)
	if !slices.Equal(segs, want) {
		return fmt.Errorf("ontology: segments %v disagree with deltas %v", segs, want)
	}
	return nil
}

// segmentsLocked 由物化缓存构建规范视图。调用方须持有锁。
func (c *Counter) segmentsLocked() []Segment {
	var segs []Segment
	for i := 0; i+1 < len(c.ends); i++ {
		count := c.pref[i]
		if count == 0 {
			continue
		}
		if n := len(segs); n > 0 && segs[n-1].Count == count && segs[n-1].Hi == c.ends[i] {
			segs[n-1].Hi = c.ends[i+1]
			continue
		}
		segs = append(segs, Segment{Lo: c.ends[i], Hi: c.ends[i+1], Count: count})
	}
	return segs
}

// segmentsFromDelta 独立地由端点净增减量重算规范视图，用于交叉校验。
func segmentsFromDelta(delta map[int64]int64) []Segment {
	ends := make([]int64, 0, len(delta))
	for p, d := range delta {
		if d != 0 {
			ends = append(ends, p)
		}
	}
	slices.Sort(ends)
	var segs []Segment
	var sum int64
	for i, p := range ends {
		sum += delta[p]
		if i+1 == len(ends) || sum == 0 {
			continue
		}
		if n := len(segs); n > 0 && segs[n-1].Count == sum && segs[n-1].Hi == p {
			segs[n-1].Hi = ends[i+1]
			continue
		}
		segs = append(segs, Segment{Lo: p, Hi: ends[i+1], Count: sum})
	}
	return segs
}
