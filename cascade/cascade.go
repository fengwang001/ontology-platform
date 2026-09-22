// Package cascade 层级换算与降级：给定剩余 tick 数算出应落在哪一层哪个槽，
// 以及各层量程与游标换算。高层元素在游标推进到其所在槽时由调用方取走整槽、
// 用 Level/Slot 重新定位到低层（惰性降级，见 DESIGN.md 推导三）。
package cascade

import "ontology/wheel"

// Layout 层级布局：Levels 层、每层 Size 槽。
type Layout struct {
	Levels int
	Size   int
}

// Build 按布局创建各层轮，游标对齐起始时刻 start。
func (l Layout) Build(start int64) []*wheel.Wheel {
	wheels := make([]*wheel.Wheel, l.Levels)
	for i := range wheels {
		wheels[i] = wheel.New(l.Size)
		wheels[i].SetPos(l.Cursor(start, i))
	}
	return wheels
}

// Granularity 返回层 level 的粒度：Size^level。
func (l Layout) Granularity(level int) int64 {
	g := int64(1)
	for i := 0; i < level; i++ {
		g *= int64(l.Size)
	}
	return g
}

// MaxDelay 返回整个轮系可表示的最大延迟：Size^Levels - 1。
func (l Layout) MaxDelay() int64 { return l.Granularity(l.Levels) - 1 }

// Level 返回剩余 remaining 个 tick 应落入的层：
// 最小满足 remaining < Size^(i+1) 的 i。
func (l Layout) Level(remaining int64) int {
	for i := 0; i < l.Levels-1; i++ {
		if remaining < l.Granularity(i+1) {
			return i
		}
	}
	return l.Levels - 1
}

// Slot 返回绝对到期时刻 deadline 在层 level 的槽号。
func (l Layout) Slot(deadline int64, level int) int {
	return int((deadline / l.Granularity(level)) % int64(l.Size))
}

// Cursor 返回累计推进量 now 在层 level 的应有游标（自检用）。
func (l Layout) Cursor(now int64, level int) int {
	return l.Slot(now, level)
}
