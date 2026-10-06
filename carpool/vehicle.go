package carpool

import "sort"

// stopLocations 返回一组订单所有上下车点的去重升序位置。
func stopLocations(orders []*Order) []int64 {
	set := make(map[int64]struct{}, 2*len(orders))
	for _, o := range orders {
		set[o.Pickup] = struct{}{}
		set[o.Dropoff] = struct{}{}
	}
	locs := make([]int64, 0, len(set))
	for x := range set {
		locs = append(locs, x)
	}
	sort.Slice(locs, func(i, j int) bool { return locs[i] < locs[j] })
	return locs
}

// stopDelayFor 返回乘客的停靠延误：在其上下车点之间（不含这两点）
// 的停靠位置数乘以单次停靠时长。同一位置的多个上下车合并为一次。
func stopDelayFor(o *Order, locs []int64, stopDuration int64) int64 {
	var n int64
	for _, x := range locs {
		if x > o.Pickup && x < o.Dropoff {
			n++
		}
	}
	return n * stopDuration
}

// capacityOK 检查从 pos 起每一段行程上的载客人数是否都不超过座位数。
func capacityOK(orders []*Order, pos int64, seats int) bool {
	pts := append([]int64{pos}, stopLocations(orders)...)
	sort.Slice(pts, func(i, j int) bool { return pts[i] < pts[j] })
	uniq := pts[:1]
	for _, x := range pts[1:] {
		if x != uniq[len(uniq)-1] {
			uniq = append(uniq, x)
		}
	}
	for i := 0; i+1 < len(uniq); i++ {
		var occ int64
		for _, o := range orders {
			if o.Pickup <= uniq[i] && o.Dropoff > uniq[i] {
				occ += int64(o.Persons)
			}
		}
		if occ > int64(seats) {
			return false
		}
	}
	return true
}

// estPickupTime 估计新订单的上车时刻：
// 当前时刻 + 行驶时间 + 上车点之前（不含上车点）的停靠总时长。
func estPickupTime(v *Vehicle, o *Order, locs []int64, cfg Config, now int64) int64 {
	t := now + (o.Pickup-v.Pos)*cfg.TimePerDistance
	for _, x := range locs {
		if x > v.Pos && x < o.Pickup {
			t += cfg.StopDuration
		}
	}
	return t
}

// insertDelta 判定订单 o 能否并入车辆 v。
// 可行时返回所有已在途乘客停靠延误之和的增加量，否则 ok=false。
func (v *Vehicle) insertDelta(o *Order, cfg Config, now int64) (delta int64, ok bool) {
	if o.Pickup < v.Pos {
		return 0, false
	}
	if len(v.Active) >= cfg.MaxActiveOrders {
		return 0, false
	}
	all := make([]*Order, 0, len(v.Active)+1)
	all = append(all, v.Active...)
	all = append(all, o)
	curLocs := stopLocations(v.Active)
	newLocs := stopLocations(all)
	for _, a := range v.Active {
		after := stopDelayFor(a, newLocs, cfg.StopDuration)
		if after > a.MaxStopDelay {
			return 0, false
		}
		delta += after - stopDelayFor(a, curLocs, cfg.StopDuration)
	}
	if stopDelayFor(o, newLocs, cfg.StopDuration) > o.MaxStopDelay {
		return 0, false
	}
	if !capacityOK(all, v.Pos, v.Seats) {
		return 0, false
	}
	if estPickupTime(v, o, newLocs, cfg, now) > o.LatestPickup {
		return 0, false
	}
	return delta, true
}
