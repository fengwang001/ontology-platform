package dispatch

// StopETA 为单个停靠的推定到达/离开时刻。
type StopETA struct {
	Arrive int64
	Depart int64
}

// Schedule 是一条未完成序列的推定结果；与序列等长、按下标对齐。
type Schedule []StopETA

// EstimateSchedule 按规则推定整条序列：
// 首个停靠以 (startPos, startAt) 为起点；取货早到等待至 ReadyAt；
// 离开时刻 = max(到达, ReadyAt) + Dwell，送达不等待。
// 任一边在耗时源中不存在时返回 nil,false。
func EstimateSchedule(tt TravelTimeSource, startPos Location, startAt int64, stops []Stop) (Schedule, bool) {
	sched := make(Schedule, len(stops))
	pos := startPos
	depart := startAt
	for i := range stops {
		travel, ok := tt.Travel(pos, stops[i].At)
		if !ok {
			return nil, false
		}
		arrive := depart + travel
		leave := arrive
		if stops[i].Kind == StopPickup && stops[i].ReadyAt > leave {
			leave = stops[i].ReadyAt // 早到等待至出餐就绪
		}
		leave += stops[i].Dwell
		sched[i] = StopETA{Arrive: arrive, Depart: leave}
		pos = stops[i].At
		depart = leave
	}
	return sched, true
}

// scheduleDuration 取“起点出发 -> 末停靠离开”的总跨度；起点相同因此
// 插入前后之差等价于“末停靠离开 - 首停靠到达 + 首边耗时”的增量，
// 用于骑手间总耗时增量比较（同一骑手下插入前后首边也可能变化）。
func scheduleDuration(startAt int64, sched Schedule) int64 {
	if len(sched) == 0 {
		return 0
	}
	return sched[len(sched)-1].Depart - startAt
}

// countHeld 统计骑手当前持有的不同订单数（已接未取 + 已取未送）。
// 每笔订单在未完成序列中至多出现取/送两个停靠，按 ID 去重。
func countHeld(stops []Stop) int {
	seen := make(map[OrderID]struct{}, len(stops))
	for i := range stops {
		seen[stops[i].OrderID] = struct{}{}
	}
	return len(seen)
}
