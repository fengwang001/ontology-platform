package cal

// isWorkday 报告某日是否在星期掩码中且不是节假日。
func (c *Calendar) isWorkday(day int64) bool {
	if c.mask&(1<<uint(day%7)) == 0 {
		return false
	}
	return !c.holidays.contains(day)
}

// advance 返回从 t 起（含 t 所在分钟）消耗 need 个工作分钟后的时刻 x，
// 使 Work(t,x)==need 且 Work(t,x-1)==need-1；need 可为 0（返回对齐后的 t）。
// 结果超过 MaxTime 时返回 ok=false。
func (c *Calendar) advance(t, need int64) (int64, bool) {
	if need == 0 {
		if x := c.alignWindow(t); x <= MaxTime {
			return x, true
		}
		return 0, false
	}
	target := c.prefix(t) + need
	lo, hi := t, MaxTime+1
	for lo < hi {
		mid := lo + (hi-lo)/2
		if c.prefix(mid) < target {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo > MaxTime {
		return 0, false
	}
	return lo, true
}

// alignWindow 把 t 对齐到其所属（或之后最近）工作窗口的起点；
// t 恰落在某工作日窗口内时原样返回。
func (c *Calendar) alignWindow(t int64) int64 {
	day, rem := t/minutesPerDay, t%minutesPerDay
	if rem < c.open || !c.isWorkday(day) {
		if rem >= c.close {
			day++
		}
		day = c.nextWorkday(day)
		return day*minutesPerDay + c.open
	}
	return t
}

// nextWorkday 返回 >= day 的最近工作日日号（周末用模 7 算术跳过）。
func (c *Calendar) nextWorkday(day int64) int64 {
	for {
		w := uint(day % 7)
		if c.mask&(1<<w) != 0 && !c.holidays.contains(day) {
			return day
		}
		step := int64(1)
		if c.mask&(1<<w) == 0 {
			for c.mask&(1<<uint((int64(w)+step)%7)) == 0 {
				step++
			}
		}
		day += step
	}
}
