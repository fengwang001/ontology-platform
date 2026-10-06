package ontology

// present 判断住户在整数日 day 是否在住（左闭右开）。
func present(r *Resident, day int64) bool {
	for _, seg := range r.Segments {
		if day >= seg.Start && (seg.End == 0 || day < seg.End) {
			return true
		}
	}
	return false
}

// roomAtDay 返回住户在 day 当天所住房间号。
func roomAtDay(r *Resident, day int64) (int64, bool) {
	for _, seg := range r.Segments {
		if day >= seg.Start && (seg.End == 0 || day < seg.End) {
			return seg.Room, true
		}
	}
	return 0, false
}

// segmentsOverlap 判断同一房间两个相邻住户的在住期是否重叠（端点交接允许）。
func segmentsOverlap(aStart, aEnd, bStart, bEnd int64) bool {
	// 开放端点（0）视为 +Inf；两个区间相交当且仅当两端都不严格先于对方。
	aLo, aHi := aStart, aEnd
	bLo, bHi := bStart, bEnd
	if aHi != 0 && bLo >= aHi {
		return false
	}
	if bHi != 0 && aLo >= bHi {
		return false
	}
	return true
}

// inResident 判断 id 是否为已登记住户（含已退出者）。
func (s *Service) inResident(id int64) bool {
	_, ok := s.residents[id]
	return ok
}

// firstCheckIn 返回住户最早入住日。
func firstCheckIn(r *Resident) int64 {
	day := r.Segments[0].Start
	for _, seg := range r.Segments {
		if seg.Start < day {
			day = seg.Start
		}
	}
	return day
}
