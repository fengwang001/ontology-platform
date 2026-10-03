package alert

import "ontology/cal"

// addHoliday 是随机测试参考世界的节假日添加（规则与 cal.AddHoliday 对齐）。
func (w *refWorld) addHoliday(day, now int64) error {
	if now < w.clock {
		return cal.ErrClock
	}
	if w.holidays[day] {
		w.clock = now
		return nil
	}
	if day <= now/1440 {
		return cal.ErrPast
	}
	w.holidays[day] = true
	for _, rt := range w.timers {
		rt.elapsed = map[int64]int64{}
	}
	w.clock = now
	return nil
}

// ack 是参考世界的确认逻辑。
func (w *refWorld) ack(rt *refTimer, q uint8, now int64) error {
	if now < w.clock {
		return cal.ErrClock
	}
	if rt.acked[q] {
		w.clock = now
		return nil
	}
	w.recompute(rt, now)
	if !rt.due[q] || rt.trig[q] > now {
		return ErrNotDue
	}
	rt.acked[q] = true
	w.clock = now
	return nil
}
