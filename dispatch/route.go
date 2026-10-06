package dispatch

import "math"

// noEtaCap 表示停靠没有推定到达时刻上限。
const noEtaCap = int64(math.MaxInt64)

// stop 是骑手停靠序列中的一个内部停靠。
type stop struct {
	orderID string
	kind    StopKind
	dwell   int64
	etaCap  int64 // 取消订单时收紧的推定到达上限，保证取消后推定不变晚
}

// orderInfo 是投影一次停靠所需的订单只读信息。
type orderInfo struct {
	pickup  Point
	drop    Point
	readyAt int64
}

func (st stop) point(o orderInfo) Point {
	if st.kind == StopPickup {
		return o.pickup
	}
	return o.drop
}

// project 从 (pos, depart) 出发，按 stops 顺序推定每个停靠的到达与离开时刻。
// 取货停靠早到时等待至出餐就绪；每个停靠的停留时长计入离开时刻。
func project(src TravelTimeSource, lookup func(string) orderInfo, pos Point, depart int64, stops []stop) (eta, leave []int64) {
	eta = make([]int64, len(stops))
	leave = make([]int64, len(stops))
	cur := pos
	t := depart
	for i, st := range stops {
		o := lookup(st.orderID)
		e := t + src.TravelTime(cur, st.point(o))
		if e > st.etaCap {
			e = st.etaCap
		}
		eta[i] = e
		lv := e
		if st.kind == StopPickup && lv < o.readyAt {
			lv = o.readyAt
		}
		leave[i] = lv + st.dwell
		cur = st.point(o)
		t = leave[i]
	}
	return eta, leave
}

// totalDuration 是序列总耗时：末停靠离开时刻减去出发时刻。
func totalDuration(depart int64, leave []int64) int64 {
	if len(leave) == 0 {
		return 0
	}
	return leave[len(leave)-1] - depart
}

// insertStops 返回把 pk 放在下标 p、dl 放在下标 q（p<q）后的新序列。
func insertStops(stops []stop, p, q int, pk, dl stop) []stop {
	out := make([]stop, 0, len(stops)+2)
	out = append(out, stops[:p]...)
	out = append(out, pk)
	out = append(out, stops[p:q-1]...)
	out = append(out, dl)
	out = append(out, stops[q-1:]...)
	return out
}
