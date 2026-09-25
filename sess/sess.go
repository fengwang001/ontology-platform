// Package sess 描述单个会话区间与计数，以及会话级两条判定原语。
// 本包不依赖工程内任何其他包。
package sess

// Session 是一个会话区间 [Start, End] 及其内事件计数。
// 零值不是合法会话；swin 保证外部只拿到拷贝。
type Session struct {
	Start  int64
	End    int64
	Count  int64
	closed bool
}

// New 创建一个只含单个事件 ts 的开放会话 [ts, ts]，计数为 1。
func New(ts int64) Session { return Session{Start: ts, End: ts, Count: 1} }

// Hit 判定 ts 是否落在会话区间或其 gap 邻域内：start-gap <= ts <= end+gap。
// 两端都取等号：间隔恰好等于 gap 的事件属于同一会话。
func (s Session) Hit(ts, gap int64) bool {
	return s.Start-gap <= ts && ts <= s.End+gap
}

// ShouldClose 判定在水位线 wm 下会话是否应当闭合：wm > end+gap（严格大于）。
func (s Session) ShouldClose(wm, gap int64) bool { return wm > s.End+gap }

// Closed 报告会话是否已被冻结。
func (s Session) Closed() bool { return s.closed }

// Close 冻结会话；冻结后只能再调用 Close，不能再 Absorb/Merge。
func (s *Session) Close() { s.closed = true }

// Absorb 把一个已被接受的事件 ts 并入会话：区间取并、计数加一。
// 允许 ts < Start（迟到事件反向扩展）或 ts > End（正向扩展）。
func (s *Session) Absorb(ts int64) {
	if ts < s.Start {
		s.Start = ts
	}
	if ts > s.End {
		s.End = ts
	}
	s.Count++
}

// Merge 把同一次合并集判定中的另一个会话并入 s，区间取并、计数累加。
func (s *Session) Merge(o Session) {
	if o.Start < s.Start {
		s.Start = o.Start
	}
	if o.End > s.End {
		s.End = o.End
	}
	s.Count += o.Count
}
