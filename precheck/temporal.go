package precheck

import (
	"fmt"
	"sort"
)

// segment 是某键时态历史中的一段：[start, 下一段.start) 内 value 生效。
type segment[T any] struct {
	start   Moment
	present bool
	value   T
}

const temporalChunkSize = 1024

// segmentChunks 是分块的段序列：写入只追加到当前块（O(1) 摊还），
// 避免大切片扩容时反复整体复制；点查先按每块首元素定位块（O(log 块数)），
// 再在块内二分（O(log 块大小)），总比较次数仍是 O(log 段数) 级。
type segmentChunks[T any] struct {
	chunks [][]segment[T]
}

func (c *segmentChunks[T]) append(seg segment[T]) {
	if len(c.chunks) == 0 || len(c.chunks[len(c.chunks)-1]) == temporalChunkSize {
		c.chunks = append(c.chunks, make([]segment[T], 0, temporalChunkSize))
	}
	last := len(c.chunks) - 1
	c.chunks[last] = append(c.chunks[last], seg)
}

func (c *segmentChunks[T]) len() int {
	if len(c.chunks) == 0 {
		return 0
	}
	return (len(c.chunks)-1)*temporalChunkSize + len(c.chunks[len(c.chunks)-1])
}

func (c *segmentChunks[T]) at(globalIdx int) segment[T] {
	return c.chunks[globalIdx/temporalChunkSize][globalIdx%temporalChunkSize]
}

// searchLE 返回最后一个 start <= t 的段全局下标，不存在返回 -1。
func (c *segmentChunks[T]) searchLE(t Moment, checks *int64) int {
	n := c.len()
	if n == 0 {
		return -1
	}
	// 先在“每块首元素”上二分找到候选块。
	block := sort.Search(len(c.chunks), func(i int) bool {
		if checks != nil {
			*checks++
		}
		return c.chunks[i][0].start > t
	}) - 1
	if block < 0 {
		return -1
	}
	base := block * temporalChunkSize
	seg := c.chunks[block]
	off := sort.Search(len(seg), func(i int) bool {
		if checks != nil {
			*checks++
		}
		return seg[i].start > t
	}) - 1
	if off < 0 {
		// t 落在前一块的末尾与本块首元素之间的“空窗”不应发生，
		// 但稳妥起见回退到前一块的最后一段。
		if block == 0 {
			return -1
		}
		return base - 1
	}
	return base + off
}

// TemporalStore 是按键组织的左闭右开时态存储，支持历史缺口标记。
type TemporalStore[T any] struct {
	segments map[string]*segmentChunks[T]
	gaps     map[string][][2]Moment

	// 可独立核查的性能统计：Point 调用次数与“段检查次数”。
	// 重建单个历史快照时，每个键的段检查次数等于二分查找次数 O(log 段数)，
	// 与系统累计版本演进总次数无线性关系（详见设计说明的复杂度章节）。
	stats TemporalStats
}

// TemporalStats 暴露时态点查的计数器，供复杂度测试独立验证。
type TemporalStats struct {
	PointCalls    int64 `json:"point_calls"`
	SegmentChecks int64 `json:"segment_checks"`
}

func (s *TemporalStore[T]) Stats() TemporalStats { return s.stats }

// ResetStats 清零计数器（不影响任何数据）。
func (s *TemporalStore[T]) ResetStats() { s.stats = TemporalStats{} }

func NewTemporalStore[T any]() *TemporalStore[T] {
	return &TemporalStore[T]{segments: map[string]*segmentChunks[T]{}, gaps: map[string][][2]Moment{}}
}

// Point 返回键在时刻 t 的生效值（边界取新版本侧）。
func (s *TemporalStore[T]) Point(key string, t Moment) (value T, present bool, gap bool) {
	s.stats.PointCalls++
	if gs, ok := s.gaps[key]; ok {
		// 缺口区间半开：[start, end)。缺口判定优先于段判定，
		// 使得“无法重建”永远压过“取到某个值”。
		for _, g := range gs {
			if t >= g[0] && t < g[1] {
				return value, false, true
			}
		}
	}
	chunks := s.segments[key]
	if chunks == nil || chunks.len() == 0 {
		return value, false, false
	}
	// 左闭右开：段在其 start 时刻立即生效；边界时刻 t 取新版本一侧。
	idx := chunks.searchLE(t, &s.stats.SegmentChecks)
	if idx < 0 {
		return value, false, false
	}
	seg := chunks.at(idx)
	if !seg.present {
		return value, false, false
	}
	return seg.value, true, false
}

// Put 在 start 时刻起写入值（start 必须不早于该键已有段的起点）。
func (s *TemporalStore[T]) Put(key string, start Moment, value T) {
	s.appendSegment(key, start, true, value)
}

// Remove 在 start 时刻起删除键。
func (s *TemporalStore[T]) Remove(key string, start Moment) {
	var zero T
	s.appendSegment(key, start, false, zero)
}

func (s *TemporalStore[T]) appendSegment(key string, start Moment, present bool, value T) {
	chunks := s.segments[key]
	if chunks != nil && chunks.len() > 0 {
		last := chunks.at(chunks.len() - 1)
		if last.start > start {
			panic(fmt.Sprintf("temporal: out-of-order write to %q at %d (last start %d)", key, start, last.start))
		}
	}
	if chunks == nil {
		chunks = &segmentChunks[T]{}
		s.segments[key] = chunks
	}
	chunks.append(segment[T]{start: start, present: present, value: value})
}

// Gap 标记半开区间 [start, end) 的历史数据缺失。
func (s *TemporalStore[T]) Gap(key string, start, end Moment) {
	if end <= start {
		panic(fmt.Sprintf("temporal: empty gap [%d,%d) for %q", start, end, key))
	}
	s.gaps[key] = append(s.gaps[key], [2]Moment{start, end})
}

// SegmentCount 返回某键的段数（复杂度验证辅助）。
func (s *TemporalStore[T]) SegmentCount(key string) int {
	if c := s.segments[key]; c != nil {
		return c.len()
	}
	return 0
}

// Keys 返回所有曾经出现过的键。
func (s *TemporalStore[T]) Keys() []string {
	keys := make([]string, 0, len(s.segments))
	for k := range s.segments {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
