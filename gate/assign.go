package gate

// Assign 把航班的某一段指派到某登机口；已有指派时为改派，
// 改派按先释放原登机口再判定的结果执行，被拒绝时原指派保持不变。
func (s *System) Assign(flightID string, seg SegKind, gateID string, now int) Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || seg < SegWhole || seg > SegBoard {
		return reject(ReasonInvalidParam, "bad params: seg=%d now=%d", seg, now)
	}
	if now < s.now {
		return reject(ReasonClockSkew, "now=%d < clock=%d", now, s.now)
	}
	s.pruneAll(now)
	fs, ok := s.flights[flightID]
	if !ok {
		return reject(ReasonNotFound, "flight %s not found", flightID)
	}
	g, ok := s.gates[gateID]
	if !ok {
		return reject(ReasonNotFound, "gate %s not found", gateID)
	}
	sg, ok := fs.segs[seg]
	if !ok {
		return reject(ReasonInvalidParam, "flight %s has no %s segment", flightID, seg)
	}
	if now >= fs.f.CurArr {
		return reject(ReasonImmutable, "flight %s arrived at %d, gate immutable at now=%d",
			flightID, fs.f.CurArr, now)
	}
	if fs.f.Level > g.maxLevel {
		return reject(ReasonLevelIncompatible, "flight %s level %d > gate %s max %d",
			flightID, fs.f.Level, gateID, g.maxLevel)
	}
	if !kindCompat(fs.f.Kind, g.kind) {
		return reject(ReasonKindMismatch, "flight %s kind %d vs gate %s kind %d",
			flightID, fs.f.Kind, gateID, g.kind)
	}
	if adj := s.adjacencyConflict(fs, g, sg.start, sg.end); adj != nil {
		return reject(ReasonAdjacency, "max-level flight %s at adjacent gate %s [%d,%d)",
			adj.owner.f.ID, adj.gate, adj.start, adj.end)
	}
	if tc := s.timeConflict(fs, g, sg.start, sg.end); tc != nil {
		r := reject(ReasonTimeConflict, "overlaps flight %s [%d,%d) at gate %s",
			tc.owner.f.ID, tc.start, tc.end, gateID)
		r.Conflict = tc.owner.f.ID
		return r
	}
	if sg.gate != "" {
		s.gates[sg.gate].tree.remove(sg.key())
	}
	sg.gate = gateID
	g.tree.insert(sg.key(), sg.end, sg)
	s.accept(now)
	return okResult("flight %s %s segment assigned to gate %s [%d,%d)",
		flightID, seg, gateID, sg.start, sg.end)
}

// adjacencyConflict 检查相邻限制：仅当本航班为最高等级机型时，
// 其所有相邻登机口不得同时被最高等级机型占用（区间相交即违反）。
func (s *System) adjacencyConflict(fs *flightState, g *gateState, st, en int) *segment {
	if fs.f.Level != MaxLevel {
		return nil
	}
	for _, adjID := range g.adj {
		ag := s.gates[adjID]
		for _, sg := range ag.tree.overlap(st, en, &s.visits) {
			if sg.owner != fs && sg.owner.f.Level == MaxLevel {
				return sg
			}
		}
	}
	return nil
}

// timeConflict 返回目标登机口上与本分段相交的其他航班分段；
// 多个冲突时取占用区间起点最早者，起点相同取标识最小者。
func (s *System) timeConflict(fs *flightState, g *gateState, st, en int) *segment {
	var best *segment
	for _, sg := range g.tree.overlap(st, en, &s.visits) {
		if sg.owner == fs {
			continue
		}
		if best == nil || sg.start < best.start ||
			(sg.start == best.start && sg.owner.f.ID < best.owner.f.ID) {
			best = sg
		}
	}
	return best
}
