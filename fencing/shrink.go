package fencing

import "sort"

// shrinkEvent 是平台在某区域登记的收缩事件，生效区间左闭右开 [start, end)。
type shrinkEvent struct {
	id           string
	region       string
	start, end   int64
	level        int
	terminated   bool
	terminatedAt int64
}

// effEnd 是事件的实际结束时刻：计划结束与提前终止的较小者。
func (e *shrinkEvent) effEnd() int64 {
	if e.terminated && e.terminatedAt < e.end {
		return e.terminatedAt
	}
	return e.end
}

// covers 报告事件是否覆盖时刻 t（左闭右开）。
func (e *shrinkEvent) covers(t int64) bool {
	return e.start <= t && t < e.effEnd()
}

// regionState 维护一个区域内尚未结束（含未开始）的收缩事件。
//
// 不变量：events 中只保留 effEnd 大于最后一次已提交时钟的事件；
// 已结束事件在 prune 时被物理删除，因此本结构的大小只取决于
// 存活事件数，与历史已结束事件总数无关。
//
// 注意：prune 只能按"已提交时钟"（被接受操作的最大时刻）调用。
// 操作按自身时刻求值时不得破坏式清理，否则被拒绝操作之后的、
// 时刻更小的合法操作会看到被错误删除的事件。
type regionState struct {
	events map[string]*shrinkEvent
}

func newRegionState() *regionState {
	return &regionState{events: make(map[string]*shrinkEvent)}
}

// add 登记事件；已经结束的只留在系统索引中，不进入区域结构。
func (r *regionState) add(ev *shrinkEvent, now int64) {
	if ev.effEnd() > now {
		r.events[ev.id] = ev
	}
}

// prune 物理删除在已提交时刻 t 前已结束的事件；t 必须单调不减。
func (r *regionState) prune(t int64) {
	for id, ev := range r.events {
		if ev.effEnd() <= t {
			delete(r.events, id)
		}
	}
}

// maxLevelAt 返回覆盖时刻 t 的事件等级的最大值（无覆盖事件时为 0）。
// 只读，开销与存活事件数成正比，与单元总数、历史事件数无关。
func (r *regionState) maxLevelAt(t int64) int {
	level := 0
	for _, ev := range r.events {
		if ev.level > level && ev.covers(t) {
			level = ev.level
		}
	}
	return level
}

// recoveryFrom 在已登记事件按计划结束的前提下，返回大于 threshold 等级的
// 事件全部不再覆盖的最早时刻 t >= now。只扫描存活事件。
func (r *regionState) recoveryFrom(now int64, threshold int) int64 {
	blocking := make([]*shrinkEvent, 0, len(r.events))
	for _, ev := range r.events {
		if ev.level > threshold && ev.effEnd() > now {
			blocking = append(blocking, ev)
		}
	}
	sort.Slice(blocking, func(i, j int) bool { return blocking[i].start < blocking[j].start })
	t := now
	for _, ev := range blocking {
		if ev.effEnd() <= t {
			continue
		}
		if ev.start > t {
			return t // 空档：t 时刻无任何阻塞事件覆盖
		}
		t = ev.effEnd()
	}
	return t
}
