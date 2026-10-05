// Package filter 依据测点状态计算待报与心跳事件：死区、最小间隔尾随与心跳。
// 所有函数均为当前状态的纯函数，状态一变即由调用方重算。
package filter

import "ontology/profile"

// Kind 为事件种类。
type Kind int

const (
	Report    Kind = iota // 待报事件
	Heartbeat             // 心跳事件
)

func (k Kind) String() string {
	if k == Report {
		return "Report"
	}
	return "Heartbeat"
}

// ReportDue 报告待报条件是否满足：非故障、有 cur、cur.ts 严格大于 lastAt
// （或尚无 lastAt），且尚无 lastV 或 |cur.v-lastV| 严格大于 db。
func ReportDue(st *profile.State) bool {
	if st.Fault || !st.HasCur {
		return false
	}
	if st.HasLast && st.CurTS <= st.LastAt {
		return false
	}
	if st.HasLast && profile.Abs(st.CurV-st.LastV) <= st.DB {
		return false
	}
	return true
}

// ReportAt 返回待报事件时刻 dc=max(lastAt+minI, cur.ts, defer)
// （尚无 lastAt 时为 max(cur.ts, defer)）。调用前须满足 ReportDue。
func ReportAt(st *profile.State) int64 {
	at := st.CurTS
	if st.HasLast && st.LastAt+st.MinI > at {
		at = st.LastAt + st.MinI
	}
	if st.Defer > at {
		at = st.Defer
	}
	return at
}

// HeartbeatDue 报告心跳条件是否满足：非故障且已有 lastAt。
func HeartbeatDue(st *profile.State) bool {
	return !st.Fault && st.HasLast
}

// HeartbeatAt 返回心跳事件时刻 dh=max(lastAt+maxI, defer)。
func HeartbeatAt(st *profile.State) int64 {
	at := st.LastAt + st.MaxI
	if st.Defer > at {
		at = st.Defer
	}
	return at
}

// Next 返回该测点最早事件的时刻与种类；同一时刻待报先于心跳。
func Next(st *profile.State) (at int64, k Kind, ok bool) {
	if ReportDue(st) {
		at, k, ok = ReportAt(st), Report, true
	}
	if HeartbeatDue(st) {
		if h := HeartbeatAt(st); !ok || h < at {
			at, k, ok = h, Heartbeat, true
		}
	}
	return at, k, ok
}

// ReasonFor 计算上报原因，a 为事件时刻。
func ReasonFor(st *profile.State, k Kind, a int64) profile.Reason {
	if k == Heartbeat {
		return profile.ReasonHeartbeat
	}
	if !st.HasLast {
		return profile.ReasonFirst
	}
	if a == st.CurTS {
		return profile.ReasonChange
	}
	return profile.ReasonTrailing
}
