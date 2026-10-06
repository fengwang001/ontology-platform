package staffing

import "fmt"

// Snapshot 导出完整状态的深拷贝。它是纯读取：不触发惰性结算、不推进时钟，
// 精确反映“截至当前已接受操作最后一次触及”的世界。
// 惰性过期/放弃只在显式触及（GetOffer/Occupancy/后续业务操作）时发生。
func (s *Service) Snapshot(now int) (out Snapshot, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = now

	out = Snapshot{
		Now:        s.now,
		Positions:  map[string]Position{},
		Candidates: map[string]bool{},
		Offers:     map[int64]Offer{},
		Exceptions: map[string]ExceptionApproval{},
		Occupied:   map[string]int{},
		Onboarded:  map[string]int{},
		Pending:    map[string]int{},
	}
	for id, p := range s.positions {
		out.Positions[id] = *p
	}
	for id := range s.candidates {
		out.Candidates[id] = true
	}
	for id, o := range s.offers {
		out.Offers[id] = *o
	}
	for id, e := range s.exceptions {
		out.Exceptions[id] = *e
	}
	for pid := range s.positions {
		out.Occupied[pid] = s.occupied(pid)
		out.Onboarded[pid] = s.onboardedCnt[pid]
		out.Pending[pid] = s.pendingCnt[pid]
	}
	return out, nil
}

// CheckInvariant 校验编制守恒与索引一致性；测试与模型对照时调用。
func CheckInvariant(snap Snapshot) error {
	for pid, p := range snap.Positions {
		occ := snap.Occupied[pid]
		onb := snap.Onboarded[pid]
		pen := snap.Pending[pid]
		if onb < 0 || pen < 0 || occ < 0 {
			return fmt.Errorf("position %s: negative count occ=%d onb=%d pen=%d", pid, occ, onb, pen)
		}
		if occ != onb+pen {
			return fmt.Errorf("position %s: occupied %d != onboarded %d + pending %d", pid, occ, onb, pen)
		}
		if occ > p.Headcount {
			return fmt.Errorf("position %s: occupied %d > headcount %d", pid, occ, p.Headcount)
		}
	}

	onbByCandidate := map[string]bool{}
	penByCandidate := map[string]int64{}
	onbCnt := map[string]int{}
	penCnt := map[string]int{}
	for _, o := range snap.Offers {
		switch o.Status {
		case StatusOnboarded:
			if o.LeftAt >= 0 {
				break
			}
			if onbByCandidate[o.CandidateID] {
				return fmt.Errorf("candidate %s holds two active onboarded offers", o.CandidateID)
			}
			onbByCandidate[o.CandidateID] = true
			onbCnt[o.PositionID]++
		case StatusPending, StatusAccepted:
			if old, ok := penByCandidate[o.CandidateID]; ok {
				return fmt.Errorf("candidate %s holds two pending offers %d,%d", o.CandidateID, old, o.ID)
			}
			penByCandidate[o.CandidateID] = o.ID
			penCnt[o.PositionID]++
		}
	}
	for pid := range snap.Positions {
		if onbCnt[pid] != snap.Onboarded[pid] {
			return fmt.Errorf("position %s: onboarded index %d != recomputed %d",
				pid, snap.Onboarded[pid], onbCnt[pid])
		}
		if penCnt[pid] != snap.Pending[pid] {
			return fmt.Errorf("position %s: pending index %d != recomputed %d",
				pid, snap.Pending[pid], penCnt[pid])
		}
	}
	return nil
}
