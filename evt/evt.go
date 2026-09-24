// Package evt 定义上游事件、合法性校验，以及两个会话是否该归并的判定。
// 本包不依赖 session-window 的其他任何包。
package evt

import "errors"

// Event 是上游输入：某个 Key 在时刻 TS 发生的一个事件。
type Event struct {
	Key string
	TS  int64
}

// ErrInvalidEvent 是事件非法的哨兵错误（目前唯一非法情形：Key 为空串）。
var ErrInvalidEvent = errors.New("evt: invalid event: empty key")

// Valid 报告事件是否合法：Key 必须非空；TS 取任意 int64 均合法。
func (e Event) Valid() bool { return e.Key != "" }

// ShouldMerge 回答：两个按 start 排序、互不重叠的相邻会话（前者结束于 end1，
// 后者开始于 start2）是否必须归并。规则唯一：相邻两点时间差不超过 gap 即同属
// 一会话。gap 由调用方保证为正。
func ShouldMerge(end1, start2, gap int64) bool { return start2-end1 <= gap }
