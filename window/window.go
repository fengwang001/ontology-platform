// Package window 维护单个日志指纹在单一窗口内的采样计数。
package window

// Entry 是一个键的窗口状态：所属窗口、已计数条数、未交付丢弃数。
type Entry struct {
	Win     int64
	Cnt     int64
	Dropped int64
}

// Params 是采样参数 N 与 M。
type Params struct {
	N int64
	M int64
}

// RollOver 保证条目位于窗口 cur；若发生窗口切换，返回旧窗口号与其未交付丢弃数。
func (e *Entry) RollOver(cur int64) (oldWin, dropped int64, ok bool) {
	if e.Win == cur {
		return 0, 0, false
	}
	oldWin = e.Win
	dropped, had := e.Dropped, e.Dropped > 0
	e.Win, e.Cnt, e.Dropped = cur, 0, 0
	return oldWin, dropped, had
}

// Observe 处理一条 sev<4 的记录，返回是否放行。
func (e *Entry) Observe(p Params) bool {
	e.Cnt++
	if e.Cnt <= p.N {
		return true
	}
	if (e.Cnt-p.N)%p.M == 0 {
		return true
	}
	e.Dropped++
	return false
}

// Admit 是 sev>=4 的记录：仅放行，不动计数。
func (e *Entry) Admit() bool {
	return true
}
