// Package delay 计算观战可见截止时刻（cutoff）与事件可投递性。
// 全部为纯函数，不持有状态。
package delay

import "ontology/live"

// Cutoff 返回 now 时刻某观战者的可见截止时刻：事件须 t <= cutoff 才可见（取等可见）。
//
// 裁判的 cutoff 恒为 now。非裁判：未结束时为 now-D；已结束时为
// min(tE, 2*now-tE-D)，即赛后以两倍速追赶。追平判定用整数式
// 2*now-tE-D >= tE（等价于 now >= tE + ceil(D/2)），不对 D/2 取整，
// 避免奇数 D 的半毫秒舍入歧义。
func Cutoff(now, tEnd, d int64, ended, judge bool) int64 {
	if judge {
		return now
	}
	if !ended {
		return now - d
	}
	c := 2*now - tEnd - d
	if c > tEnd {
		c = tEnd
	}
	return c
}

// HiddenOK 报告此刻 Hidden 事件是否可投递：裁判恒可投递；
// 非裁判须对局已结束且 cutoff 已追平 tE。
func HiddenOK(cutoff, tEnd int64, ended, judge bool) bool {
	return judge || (ended && cutoff == tEnd)
}

// Deliverable 报告事件 ev 此刻对该观战者是否可投递。
// Normal 与 End 只看 t <= cutoff；Hidden 另须满足 HiddenOK。
func Deliverable(ev live.Event, cutoff, tEnd int64, ended, judge bool) bool {
	if ev.T > cutoff {
		return false
	}
	if ev.Kind == live.Hidden {
		return HiddenOK(cutoff, tEnd, ended, judge)
	}
	return true
}
