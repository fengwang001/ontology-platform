package delay

import "ontology/live"

// View 是截止时刻计算所需的对局视图: D 为观战延迟(毫秒),
// Ended/TE 给出是否结束及 End 事件时刻 tE。
type View struct {
	D     int64
	Ended bool
	TE    int64
}

// Cutoff 返回某观战者在 now 的可见截止时刻。
// 裁判恒为 now; 非裁判未结束为 now-D, 已结束为 min(tE, 2*now-tE-D),
// 全程整数运算不做除法, D 为奇数同样精确。
func Cutoff(now int64, judge bool, v View) int64 {
	if judge {
		return now
	}
	c := now - v.D
	if v.Ended {
		c = 2*now - v.TE - v.D
		if c > v.TE {
			c = v.TE
		}
	}
	return c
}

// Visible 判定事件 e 对该观战者此刻是否可投递。
// 裁判一切事件可见; 非裁判要求 t<=cutoff, 且 Hidden 另须已结束且 cutoff==tE。
func Visible(e live.Event, now int64, judge bool, v View) bool {
	c := Cutoff(now, judge, v)
	if e.Now > c {
		return false
	}
	if judge || e.Kind != live.Hidden {
		return true
	}
	return v.Ended && c == v.TE
}
