package metering

import "math"

// noEffect 表示本次写操作不影响任何历史区间（无需更正重算）。
const noEffect = int64(math.MaxInt64)

// entry 为读数序列中的一个条目：普通读数（实抄/估抄）或换表标记。
type entry struct {
	time     int64
	value    int64 // 普通读数值
	estimate bool  // 是否估抄
	replace  bool  // 是否换表标记
	oldFinal int64 // 换表：旧表终读数
	newStart int64 // 换表：新表起始读数
}

// leftValue 返回该条目作为用量段左端点时的起算值。
func (e entry) leftValue() int64 {
	if e.replace {
		return e.newStart
	}
	return e.value
}

// rightValue 返回该条目作为用量段右端点时的结算值。
func (e entry) rightValue() int64 {
	if e.replace {
		return e.oldFinal
	}
	return e.value
}

// interval 为左闭右开的整数时刻区间。
type interval struct{ start, end int64 }

// meter 为一只表（总表或分户表）的只追加读数序列及其静态属性。
type meter struct {
	id        string
	role      Role
	rangeMax  int64
	area      int64
	entries   []entry
	occupancy []interval // 在住记录，已归并，互不重叠
}

func (m *meter) lastEntry() (entry, bool) {
	if len(m.entries) == 0 {
		return entry{}, false
	}
	return m.entries[len(m.entries)-1], true
}

// usageBetween 计算同一表段内 v1（早）到 v2（晚）的用量。
// v2 < v1 且跌落差额不超过量程一半时视为翻转一次，按跨过上限计；
// 差额超过量程一半时 ok=false（读数非法）。
func usageBetween(v1, v2, rangeMax int64) (u int64, ok bool) {
	if v2 >= v1 {
		return v2 - v1, true
	}
	if 2*(v1-v2) <= rangeMax {
		return (rangeMax - v1) + v2, true
	}
	return 0, false
}

// lastActualIndex 返回最近一次实抄（含换表标记）的下标，无则 -1。
func (m *meter) lastActualIndex() int {
	for i := len(m.entries) - 1; i >= 0; i-- {
		if !m.entries[i].estimate {
			return i
		}
	}
	return -1
}

// appendReading 校验并追加一条读数，返回受影响的历史起点（供更正重算）。
// 实抄会先移除其之前的全部连续估抄（估抄被实抄替代）；校验失败不改变序列。
func (m *meter) appendReading(t, v int64, estimate bool, gap int64) (int64, *Error) {
	if last, ok := m.lastEntry(); ok && t <= last.time {
		return 0, fail(ErrOutOfOrder, "表 %s 读数时刻 %d 不晚于已有时刻 %d", m.id, t, last.time)
	}
	if estimate {
		la := m.lastActualIndex()
		if la < 0 {
			return 0, fail(ErrEstimateNotAllowed, "表 %s 尚无实抄基线", m.id)
		}
		if t-m.entries[la].time <= gap {
			return 0, fail(ErrEstimateNotAllowed, "表 %s 距上一次实抄 %d 未超过 %d", m.id, t-m.entries[la].time, gap)
		}
		if last, ok := m.lastEntry(); ok && v < last.leftValue() {
			return 0, fail(ErrEstimateNotAllowed, "表 %s 估抄值 %d 小于上一次读数 %d", m.id, v, last.leftValue())
		}
		m.entries = append(m.entries, entry{time: t, value: v, estimate: true})
		if len(m.entries) >= 2 {
			return m.entries[len(m.entries)-2].time, nil
		}
		return noEffect, nil
	}
	// 实抄：在副本上移除尾部估抄后校验，全部通过才提交。
	la := m.lastActualIndex()
	base := m.entries[:la+1]
	if la >= 0 {
		if _, ok := usageBetween(base[la].leftValue(), v, m.rangeMax); !ok {
			return 0, fail(ErrIllegalReading, "表 %s 读数 %d 相对 %d 跌落超过量程一半", m.id, v, base[la].leftValue())
		}
	}
	if la == len(m.entries)-1 {
		m.entries = append(m.entries, entry{time: t, value: v}) // 无估抄，直接追加
	} else {
		next := make([]entry, 0, len(base)+1)
		next = append(next, base...)
		next = append(next, entry{time: t, value: v})
		m.entries = next
	}
	if la >= 0 {
		return base[la].time, nil
	}
	return noEffect, nil
}

// appendReplacement 校验并追加换表标记：旧表按终读数结算，新表自起始读数起算，
// 换表当刻不计用量。换表标记视同实抄，会先移除尾部估抄。
func (m *meter) appendReplacement(t, oldFinal, newStart int64) (int64, *Error) {
	if last, ok := m.lastEntry(); ok && t <= last.time {
		return 0, fail(ErrOutOfOrder, "表 %s 换表时刻 %d 不晚于已有时刻 %d", m.id, t, last.time)
	}
	la := m.lastActualIndex()
	base := m.entries[:la+1]
	if la >= 0 {
		if _, ok := usageBetween(base[la].leftValue(), oldFinal, m.rangeMax); !ok {
			return 0, fail(ErrIllegalReading, "表 %s 旧表终读数 %d 相对 %d 跌落超过量程一半", m.id, oldFinal, base[la].leftValue())
		}
	}
	rep := entry{time: t, replace: true, oldFinal: oldFinal, newStart: newStart}
	if la == len(m.entries)-1 {
		m.entries = append(m.entries, rep)
	} else {
		next := make([]entry, 0, len(base)+1)
		next = append(next, base...)
		next = append(next, rep)
		m.entries = next
	}
	if la >= 0 {
		return base[la].time, nil
	}
	return noEffect, nil
}
