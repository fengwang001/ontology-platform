package contract

import "math"

// Renewal 判定与查询的统一入口是惰性游标：
//   - 已提交时钟 lastNow 之前的到期日判定结果不可变，可安全缓存
//     （影响某日有效值的事件，其操作日必不晚于该日；时钟单调，故不会再来）；
//   - [lastNow, 目标日] 区间内的判定通过只读模拟完成，不落缓存。

// decideRenewal 计算到期日 E 的续签判定。
// boundary 为上一任期到期日（首任期为 MinInt），通知窗口为 (boundary, E-N]。
// 返回是否续签、续签期长度与通知提前天数（后两者无论是否续签都返回当日有效值）。
func (c *Contract) decideRenewal(E, boundary int) (renew bool, period, noticeDays int) {
	auto, _ := c.valueAt(c.autoID, E)
	period, _ = c.valueAt(c.periodID, E)
	noticeDays, _ = c.valueAt(c.noticeID, E)
	if auto == 0 || period <= 0 {
		return false, period, noticeDays
	}
	if c.hasTimelyNotice(E, noticeDays, boundary) {
		return false, period, noticeDays
	}
	return true, period, noticeDays
}

// hasTimelyNotice 报告是否存在被接受的不续签通知，
// 其接受日 d 满足 boundary < d <= E-noticeDays（恰等于提前天数也算及时）。
func (c *Contract) hasTimelyNotice(E, noticeDays, boundary int) bool {
	deadline := E - noticeDays
	for _, n := range c.notices {
		if n.day > boundary && n.day <= deadline {
			return true
		}
	}
	return false
}

// computeRenewals 物化所有到期日 < limit 的续签判定（可缓存部分）。
// 调用方保证 limit <= 已提交时钟。
func (c *Contract) computeRenewals(limit int) {
	for !c.done && c.expCursor < limit {
		E := c.expCursor
		if c.terminated && c.termDay <= E {
			c.done = true
			break
		}
		renew, period, noticeDays := c.decideRenewal(E, c.noticeBoundary)
		if !renew {
			c.done = true
			c.naturalEnd = true
			c.endDay = E
			break
		}
		c.renewals = append(c.renewals, Renewal{
			OldExpiry:  E,
			NewExpiry:  E + period,
			Period:     period,
			NoticeDays: noticeDays,
		})
		c.noticeBoundary = E
		c.expCursor = E + period
	}
}

// renewalView 是续签状态的一致快照：缓存部分 + 只读模拟部分。
type renewalView struct {
	expCursor  int
	done       bool
	naturalEnd bool
	endDay     int
	boundary   int
	renewals   []Renewal
}

// viewAt 返回覆盖到 limit 的续签视图。committed 为已提交时钟。
func (c *Contract) viewAt(committed, limit int) renewalView {
	c.computeRenewals(committed)
	v := renewalView{
		expCursor:  c.expCursor,
		done:       c.done,
		naturalEnd: c.naturalEnd,
		endDay:     c.endDay,
		boundary:   c.noticeBoundary,
		renewals:   append([]Renewal(nil), c.renewals...),
	}
	for !v.done && v.expCursor < limit {
		E := v.expCursor
		if c.terminated && c.termDay <= E {
			v.done = true
			break
		}
		renew, period, noticeDays := c.decideRenewal(E, v.boundary)
		if !renew {
			v.done = true
			v.naturalEnd = true
			v.endDay = E
			break
		}
		v.renewals = append(v.renewals, Renewal{
			OldExpiry:  E,
			NewExpiry:  E + period,
			Period:     period,
			NoticeDays: noticeDays,
		})
		v.boundary = E
		v.expCursor = E + period
	}
	return v
}

// finalEnd 返回合同的最后在期日；ok 为 false 表示合同仍在滚动续签、无确定终点。
func (v renewalView) finalEnd(c *Contract) (end int, ok bool) {
	end = math.MaxInt
	if v.done && v.naturalEnd {
		end = v.endDay
	}
	if c.terminated && c.termDay < end {
		end = c.termDay
	}
	return end, end != math.MaxInt
}

// inForceAt 报告合同在 day 是否在期。now 为已提交时钟。
func (c *Contract) inForceAt(day, now int) bool {
	if day < c.start {
		return false
	}
	v := c.viewAt(now, day)
	end, _ := v.finalEnd(c)
	return day <= end
}

// currentExpiry 返回当前到期日；已终止或自然届满的合同返回其最后在期日。
func (c *Contract) currentExpiry(now int) int {
	v := c.viewAt(now, now)
	if end, ok := v.finalEnd(c); ok {
		return end
	}
	return v.expCursor
}

// renewalHistory 返回截至 now 已发生的全部续签记录（不含 now 当日才判定的）。
func (c *Contract) renewalHistory(now int) []Renewal {
	v := c.viewAt(now, now)
	return v.renewals
}
