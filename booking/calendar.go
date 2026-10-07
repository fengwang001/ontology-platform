package booking

// calendar 是单个房源的日历：占用区间索引 + 按日最短入住设置 + 换客间隙。
// 判定可订性的开销 = 两次 treap 查找（O(log n)）+ 常数次最短入住查询，
// 不随历史预订总数线性增长。
type calendar struct {
	occ            treap
	minStayByDay   map[int]int
	defaultMinStay int
	gapDays        int
}

func newCalendar(defaultMinStay, gapDays int) *calendar {
	return &calendar{
		minStayByDay:   make(map[int]int),
		defaultMinStay: defaultMinStay,
		gapDays:        gapDays,
	}
}

// minStayAt 返回某日适用的最短入住夜数（按日覆盖优先于默认值）。
func (c *calendar) minStayAt(day int) int {
	if v, ok := c.minStayByDay[day]; ok {
		return v
	}
	return c.defaultMinStay
}

// overlapOf 把相交的占用区间翻译成封锁/冲突错误。
func overlapOf(v ivl) *OpError {
	if v.kind == kindBlock {
		return errf(CodeBlocked, "nights intersect block %s [%d,%d)", v.id, v.start, v.end)
	}
	return errf(CodeConflict, "nights intersect booking %s [%d,%d)", v.id, v.start, v.end)
}

// overlapErr 检查 [ci,co) 是否与任何占用相交（用于封锁的新增与延长）。
func (c *calendar) overlapErr(ci, co int) *OpError {
	if pred, ok := c.occ.predecessor(ci); ok && pred.end > ci {
		return overlapOf(pred)
	}
	if next, ok := c.occ.lowerBound(ci); ok && next.start < co {
		return overlapOf(next)
	}
	return nil
}

// judge 判定 [ci,co) 是否可订，按固定次序返回第一个不可订原因：
// 封锁 -> 与预订冲突 -> 间隙不足 -> 最短入住不足 -> 孤夜。
// 调用前须保证已失效的保留已从 occ 摘除。
func (c *calendar) judge(ci, co int) *OpError {
	c.occ.steps = 0
	pred, hasPred := c.occ.predecessor(ci)
	next, hasNext := c.occ.lowerBound(ci)

	// 1. 与封锁相交 / 2. 与有效预订相交（按日期先后报告先相交者）
	if hasPred && pred.end > ci {
		return overlapOf(pred)
	}
	if hasNext && next.start < co {
		return overlapOf(next)
	}
	// 此后 pred.end <= ci 且 next.start >= co，二者即直接相邻占用。

	// 3. 换客间隙：仅针对预订邻居，间隙为两预订之间必须空出的整天数
	if hasPred && pred.kind == kindBooking && ci-pred.end < c.gapDays {
		return errf(CodeGap, "need %d empty days after booking %s ends at %d, check-in at %d",
			c.gapDays, pred.id, pred.end, ci)
	}
	if hasNext && next.kind == kindBooking && next.start-co < c.gapDays {
		return errf(CodeGap, "need %d empty days before booking %s starts at %d, check-out at %d",
			c.gapDays, next.id, next.start, co)
	}

	// 4. 最短入住：取入住日适用的设定
	if need := c.minStayAt(ci); co-ci < need {
		return errf(CodeMinStay, "stay of %d nights shorter than min stay %d at day %d",
			co-ci, need, ci)
	}

	// 5. 孤夜：与相邻预订之间扣除换客间隙后剩余的空档，
	// 大于零且小于空档首日适用的最短入住则拒绝；与封锁相邻的空档豁免。
	if hasPred && pred.kind == kindBooking {
		if free := ci - pred.end - c.gapDays; free > 0 && free < c.minStayAt(pred.end+c.gapDays) {
			return errf(CodeOrphan, "left orphan gap of %d nights before check-in (min stay %d)",
				free, c.minStayAt(pred.end+c.gapDays))
		}
	}
	if hasNext && next.kind == kindBooking {
		if free := next.start - co - c.gapDays; free > 0 && free < c.minStayAt(co+c.gapDays) {
			return errf(CodeOrphan, "left orphan gap of %d nights after check-out (min stay %d)",
				free, c.minStayAt(co+c.gapDays))
		}
	}
	return nil
}
