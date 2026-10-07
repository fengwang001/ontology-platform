package calendar

// checkAvailability 判定 [checkIn, checkOut) 在 now 时刻的可订性。
// 子原因按固定次序报告：封锁 -> 与预订冲突 -> 间隙不足 -> 最短入住不足 -> 孤夜。
//
// 开销只与本次预订的夜数、Gap、maxMinStay 及封锁条数有关，
// 与该房源的历史预订总数无关（由 Listing.probes 计数可验证）。
// excludeID 用于「修改日期」场景：在原预订已释放的日历上判定。
func (s *Service) checkAvailability(l *Listing, now, checkIn, checkOut, excludeID int64) *Error {
	// 1. 不与封锁相交
	for _, id := range l.blockOrder {
		b := l.blocks[id]
		if b.Start < checkOut && checkIn < b.End {
			return errNotBookable(ReasonBlocked,
				"日期 [%d,%d) 与封锁 [%d,%d) 相交", checkIn, checkOut, b.Start, b.End)
		}
	}
	// 2. 不与任何有效保留或已确认预订相交
	for n := checkIn; n < checkOut; n++ {
		if s.activeOccupant(l, n, now, excludeID) {
			return errNotBookable(ReasonBookingConflict, "夜 %d 已被预订占用", n)
		}
	}
	// 邻居扫描：前一笔占用的最后一夜 / 后一笔占用的第一夜；遇到封锁即停（封锁相邻的空档不受约束）
	prevNight, prevIsOcc, prevFound := s.scanNeighbor(l, now, checkIn, excludeID, -1)
	nextNight, nextIsOcc, nextFound := s.scanNeighbor(l, now, checkOut, excludeID, +1)
	// 3. 换客间隙：两侧空出的整天数都必须 >= Gap
	if prevFound && prevIsOcc && checkIn-prevNight-1 < l.Gap {
		return errNotBookable(ReasonGapTooSmall,
			"距前一笔预订（最后一夜 %d）仅空 %d 天，小于间隙 %d", prevNight, checkIn-prevNight-1, l.Gap)
	}
	if nextFound && nextIsOcc && nextNight-checkOut < l.Gap {
		return errNotBookable(ReasonGapTooSmall,
			"距后一笔预订（首夜 %d）仅空 %d 天，小于间隙 %d", nextNight, nextNight-checkOut, l.Gap)
	}
	// 4. 最短入住：取入住日适用的设定
	if nights := checkOut - checkIn; nights < l.minStayAt(checkIn) {
		return errNotBookable(ReasonMinStayTooShort,
			"入住 %d 夜，少于入住日 %d 适用的最短入住 %d 夜", nights, checkIn, l.minStayAt(checkIn))
	}
	// 5. 孤夜：扣除换客间隙后剩余的空档夜数，大于零且小于空档首日的最短入住则拒绝
	if prevFound && prevIsOcc {
		gapStart := prevNight + 1
		if orphan := checkIn - gapStart - l.Gap; orphan > 0 && orphan < l.minStayAt(gapStart) {
			return errNotBookable(ReasonOrphanNight,
				"入住前留下空档 %d 夜（扣除间隙 %d），小于空档首日 %d 的最短入住 %d",
				orphan, l.Gap, gapStart, l.minStayAt(gapStart))
		}
	}
	if nextFound && nextIsOcc {
		gapStart := checkOut
		if orphan := nextNight - checkOut - l.Gap; orphan > 0 && orphan < l.minStayAt(gapStart) {
			return errNotBookable(ReasonOrphanNight,
				"退房后留下空档 %d 夜（扣除间隙 %d），小于空档首日 %d 的最短入住 %d",
				orphan, l.Gap, gapStart, l.minStayAt(gapStart))
		}
	}
	return nil
}

// activeOccupant 报告某夜在 now 时刻是否被有效预订（保留未过期或已确认）占用。
func (s *Service) activeOccupant(l *Listing, night, now, excludeID int64) bool {
	id, ok := l.occupant(night)
	if !ok || id == excludeID {
		return false
	}
	b := s.bookings[id]
	return b != nil && b.activeAt(now)
}

// scanNeighbor 从 from 出发向 dir 方向逐夜扫描（dir=-1 时首夜为 from-1，dir=+1 时首夜为 from），
// 返回遇到的第一个封锁夜或占用夜。扫描上界为 Gap+maxMinStay 夜：
// 超过该距离的空档必然同时满足间隙与孤夜约束，无需继续。
func (s *Service) scanNeighbor(l *Listing, now, from, excludeID, dir int64) (night int64, isOccupancy, found bool) {
	limit := l.Gap + l.maxMinStay
	for i := int64(1); i <= limit; i++ {
		var n int64
		if dir < 0 {
			n = from - i
		} else {
			n = from + i - 1
		}
		if l.isBlocked(n) {
			return n, false, true
		}
		if s.activeOccupant(l, n, now, excludeID) {
			return n, true, true
		}
	}
	return 0, false, false
}
