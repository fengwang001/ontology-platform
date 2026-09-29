package window

// FinalCount 一个窗口的最终计数结果。
type FinalCount struct {
	Key         string
	WindowStart int64
	WindowEnd   int64
	Count       int64
}

// RecomputeFinalCounts 以独立的最小模型重算每个窗口的最终计数，
// 用于核对引擎变更日志的最终值是否一致（本地批量重算验证）。
//
// 模型只复现“接受/丢弃”语义：事件按给定顺序处理，
// 水位线 = 已见最大事件时间 - WatermarkDelay（只进不退），
// 到达时水位线 >= 窗口右边界 + AllowedLateness 的事件被丢弃，其余计入。
func RecomputeFinalCounts(events []Event, cfg Config) []FinalCount {
	counts := make(map[WindowKey]int64)
	order := make([]WindowKey, 0)
	var maxSeen int64
	hasEvent := false
	var watermark int64
	for _, ev := range events {
		if ev.Key == "" {
			continue
		}
		start := floorDiv(ev.Timestamp, cfg.WindowSize) * cfg.WindowSize
		end := start + cfg.WindowSize
		if hasEvent && watermark >= end+cfg.AllowedLateness {
			// 超出迟到上限，丢弃。
			continue
		}
		wk := WindowKey{Key: ev.Key, Start: start}
		if _, ok := counts[wk]; !ok {
			order = append(order, wk)
		}
		counts[wk]++
		first := !hasEvent
		if first || ev.Timestamp > maxSeen {
			maxSeen = ev.Timestamp
			hasEvent = true
		}
		if wm := maxSeen - cfg.WatermarkDelay; first || wm > watermark {
			watermark = wm
		}
	}
	out := make([]FinalCount, 0, len(order))
	for _, wk := range order {
		out = append(out, FinalCount{
			Key:         wk.Key,
			WindowStart: wk.Start,
			WindowEnd:   wk.Start + cfg.WindowSize,
			Count:       counts[wk],
		})
	}
	return out
}

// FinalCountsFromChangelog 从变更日志汇总每个窗口的最终取值：
// 取每个窗口最后一条“取值类”记录（EARLY/ON_TIME/LATE_UPDATE）的计数。
func FinalCountsFromChangelog(changelog []Change) []FinalCount {
	last := make(map[WindowKey]Change)
	order := make([]WindowKey, 0)
	for _, c := range changelog {
		if c.Type == TriggerLateRetract {
			continue
		}
		wk := WindowKey{Key: c.Key, Start: c.WindowStart}
		if _, ok := last[wk]; !ok {
			order = append(order, wk)
		}
		last[wk] = c
	}
	out := make([]FinalCount, 0, len(order))
	for _, wk := range order {
		c := last[wk]
		out = append(out, FinalCount{
			Key:         wk.Key,
			WindowStart: wk.Start,
			WindowEnd:   c.WindowEnd,
			Count:       c.Count,
		})
	}
	return out
}
