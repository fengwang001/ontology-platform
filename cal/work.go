package cal

import "sort"

func overlap16(a1, a2, b1, b2 int64) int64 {
	lo, hi := a1, a2
	if b1 > lo {
		lo = b1
	}
	if b2 < hi {
		hi = b2
	}
	if hi <= lo {
		return 0
	}
	return hi - lo
}

func (c *Calendar) workLocked(t1, t2 int64) int64 {
	if t1 == t2 {
		return 0
	}
	d1, d2 := t1/MinPerDay, t2/MinPerDay
	var total int64
	day := func(d, lo, hi int64) {
		if c.mask>>(d%7)&1 == 1 {
			total += overlap16(d*MinPerDay+c.open, d*MinPerDay+c.close, lo, hi)
		}
	}
	if d1 == d2 {
		day(d1, t1, t2)
	} else {
		day(d1, t1, (d1+1)*MinPerDay)
		day(d2, d2*MinPerDay, t2)
		if n := d2 - d1 - 1; n > 0 {
			perWeek := int64(popcnt(c.mask)) * (c.close - c.open)
			total += n / 7 * perWeek
			for k, w := int64(0), (d1+1)%7; k < n%7; k, w = k+1, (w+1)%7 {
				if c.mask>>w&1 == 1 {
					total += c.close - c.open
				}
			}
		}
	}
	i := sort.Search(len(c.holiday), func(i int) bool { return c.holiday[i] >= d1 })
	for ; i < len(c.holiday) && c.holiday[i] <= d2; i++ {
		d := c.holiday[i]
		if c.mask>>(d%7)&1 == 1 {
			total -= overlap16(d*MinPerDay+c.open, d*MinPerDay+c.close, t1, t2)
		}
	}
	return total
}

// WorkLocked 是 Work 的持调用方读锁版本，供 sla/alert 复合查询。
func (c *Calendar) WorkLocked(t1, t2 int64) int64 { return c.workLocked(t1, t2) }

// AdvanceLocked 是 advanceLocked 的导出版本（调用方持读锁）。
func (c *Calendar) AdvanceLocked(t, n int64) (int64, bool) {
	if n <= 0 {
		return t, true
	}
	return c.advanceLocked(t, n)
}

func popcnt(m uint8) int {
	n := 0
	for m != 0 {
		n += int(m & 1)
		m >>= 1
	}
	return n
}

// advanceLocked 返回从 t（任意时刻）继续累计 n≥1 个工作分钟后恰好达到的时刻。
// 结果超过 MaxTime 时 ok=false。以节假日为断点，无节假日区间用整周公式 + ≤6 天
// 零头批量推进；访问日数 O(1+h)，不逐日扫描。
func (c *Calendar) advanceLocked(t, n int64) (int64, bool) {
	wlen := c.close - c.open
	perWeek := int64(popcnt(c.mask)) * wlen
	cur := t
	for n > 0 {
		// 1) 先消耗 cur 所在窗口的剩余部分。
		d := cur / MinPerDay
		base := d * MinPerDay
		s := base + c.open
		e := base + c.close
		if c.mask>>(d%7)&1 == 1 && !c.isHolidayLocked(d) && cur >= s && cur < e {
			if room := e - cur; n > room {
				n -= room
			} else if r := cur + n; r <= MaxTime {
				return r, true
			} else {
				return 0, false
			}
		}
		// 2) nd = 下一个候选日（cur 当天开窗前则为当天，否则从次日起）。
		nd := d
		if !(cur < s && c.mask>>(d%7)&1 == 1 && !c.isHolidayLocked(d)) {
			nd = d + 1
		}
		// 3) 在有序节假日表上定位，构造无节假日区间 [nd, endDay)。
		i := sort.Search(len(c.holiday), func(i int) bool { return c.holiday[i] >= nd })
		endDay := MaxTime/MinPerDay + 1
		for i < len(c.holiday) {
			h0 := c.holiday[i]
			j := i + 1
			for j < len(c.holiday) && c.holiday[j] == c.holiday[j-1]+1 {
				j++
			}
			if c.gapCapacity(nd, h0, perWeek, wlen) >= n {
				endDay = h0
				break
			}
			n -= c.gapCapacity(nd, h0, perWeek, wlen)
			nd = c.holiday[j-1] + 1
			i = j
		}
		// 4) n 落在无节假日区间 [nd, endDay) 内，用整周公式定位。
		r, inside := c.positionInGap(nd, endDay, n, wlen, perWeek)
		if !inside {
			return 0, false
		}
		if r > MaxTime {
			return 0, false
		}
		return r, true
	}
	return cur, true
}

// gapCapacity 返回无节假日日区间 [a,b) 内的工作分钟数（仅按周掩码）。
func (c *Calendar) gapCapacity(a, b, perWeek, wlen int64) int64 {
	g := b - a
	if g <= 0 {
		return 0
	}
	total := (g / 7) * perWeek
	for k, w := int64(0), a%7; k < g%7; k, w = k+1, (w+1)%7 {
		if c.mask>>w&1 == 1 {
			total += wlen
		}
	}
	return total
}

// positionInGap 在无节假日区间 [a,b) 内定位累计 n 分钟的时刻；容量不足返回 false。
func (c *Calendar) positionInGap(a, b, n, wlen, perWeek int64) (int64, bool) {
	g := b - a
	if perWeek == 0 || g <= 0 {
		return 0, false
	}
	weeks := n / perWeek
	if n%perWeek != 0 {
		// 从第 weeks 个完整周之后的零头窗口顺序消费（至多 7 个窗口）。
		rem := n - weeks*perWeek
		d0 := a + weeks*7
		if d0 >= b {
			return 0, false
		}
		for k := int64(0); k < 7; k++ {
			day := d0 + k
			if day >= b {
				return 0, false
			}
			if c.mask>>(day%7)&1 == 0 {
				continue
			}
			if rem <= wlen {
				return day*MinPerDay + c.open + rem, true
			}
			rem -= wlen
		}
		return 0, false
	}
	// n 恰为 perWeek 整数倍：落在第 weeks-1 周最后一个工作窗口的末尾。
	if weeks == 0 || weeks > g/7 {
		return 0, false
	}
	last := a + weeks*7 - 1
	for c.mask>>(last%7)&1 == 0 {
		last--
	}
	if last < a {
		return 0, false
	}
	return last*MinPerDay + c.close, true
}
