// Package seqmap 维护稀疏锚点表，支持「序号↔位点」两个方向的锚点定位。
// 本包不依赖项目内其他包。
package seqmap

import "sort"

// Anchor 记录某分段首条事件的序号与起始位点。
type Anchor struct {
	Seq    int64
	Offset int64
}

// Map 是按追加顺序保存的锚点表；锚点须按 Seq/Offset 严格递增追加。
type Map struct {
	anchors []Anchor
}

// Add 追加一条锚点。
func (m *Map) Add(a Anchor) { m.anchors = append(m.anchors, a) }

// Len 返回锚点条数。
func (m *Map) Len() int { return len(m.anchors) }

// At 返回第 i 条锚点。
func (m *Map) At(i int) Anchor { return m.anchors[i] }

// Slice 返回锚点表副本。
func (m *Map) Slice() []Anchor { return append([]Anchor(nil), m.anchors...) }

// Replace 整体替换锚点表（重建或故障注入用）。
func (m *Map) Replace(a []Anchor) { m.anchors = append([]Anchor(nil), a...) }

// FloorSeq 返回最后一个 Seq <= s 的锚点；没有则 ok=false。
func (m *Map) FloorSeq(s int64) (a Anchor, ok bool) {
	i := sort.Search(len(m.anchors), func(i int) bool { return m.anchors[i].Seq > s }) - 1
	if i < 0 {
		return Anchor{}, false
	}
	return m.anchors[i], true
}

// FloorOffset 返回最后一个 Offset <= o 的锚点；没有则 ok=false。
func (m *Map) FloorOffset(o int64) (a Anchor, ok bool) {
	i := sort.Search(len(m.anchors), func(i int) bool { return m.anchors[i].Offset > o }) - 1
	if i < 0 {
		return Anchor{}, false
	}
	return m.anchors[i], true
}
