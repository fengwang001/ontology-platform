// Package seqmap 维护变更流的稀疏锚点表：
// 每条锚点记录 (Seq, Offset)，支持按 Seq / Offset 双向「向下取整」定位。
package seqmap

import "sort"

// Anchor 是一条稀疏锚点：某分段首条事件的序号与起始位点。
type Anchor struct {
	Seq    int64
	Offset int64
}

// Map 是锚点的有序集合，Seq 与 Offset 均严格递增。
type Map struct {
	anchors []Anchor
}

// Add 追加一条锚点，调用方保证 Seq、Offset 严格递增。
func (m *Map) Add(seq, offset int64) {
	m.anchors = append(m.anchors, Anchor{Seq: seq, Offset: offset})
}

// Reset 清空全部锚点（Rebuild 用）。
func (m *Map) Reset() { m.anchors = m.anchors[:0] }

// Anchors 返回锚点副本，供 Verify 校验。
func (m *Map) Anchors() []Anchor {
	out := make([]Anchor, len(m.anchors))
	copy(out, m.anchors)
	return out
}

// FloorSeq 返回最后一个 Seq <= s 的锚点。
func (m *Map) FloorSeq(s int64) (Anchor, bool) {
	i := sort.Search(len(m.anchors), func(i int) bool { return m.anchors[i].Seq > s })
	if i == 0 {
		return Anchor{}, false
	}
	return m.anchors[i-1], true
}

// FloorOffset 返回最后一个 Offset <= o 的锚点。
func (m *Map) FloorOffset(o int64) (Anchor, bool) {
	i := sort.Search(len(m.anchors), func(i int) bool { return m.anchors[i].Offset > o })
	if i == 0 {
		return Anchor{}, false
	}
	return m.anchors[i-1], true
}
