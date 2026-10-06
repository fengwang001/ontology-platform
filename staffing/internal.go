package staffing

import "fmt"

// settleTx 记录一次操作中惰性结算带来的全部变更，支持失败回滚。
// 语义：惰性过期/放弃是“时间推进 + 被操作触及”的产物，
// 仅当该操作最终被接受时物化；操作被拒绝时整体回滚，
// 因而被拒绝的操作不改变任何状态、占用与时钟。
type settleTx struct {
	undo []func()
}

func (tx *settleTx) rollback() {
	for i := len(tx.undo) - 1; i >= 0; i-- {
		tx.undo[i]()
	}
}

// advanceClock 在被接受操作与惰性结算之前校验时钟。
// 回退是时钟层面的拒绝：不改变任何业务状态（含不推进时钟）。
func (s *Service) advanceClock(now int) error {
	if now < 0 {
		return newError(CodeInvalidParam, "now must be >= 0, got %d", now)
	}
	if s.clockSet && now < s.now {
		return newError(CodeClockRollback, "now %d < last accepted now %d", now, s.now)
	}
	return nil
}

// commitClock 仅在操作被接受时推进时钟。
func (s *Service) commitClock(now int) {
	s.now = now
	s.clockSet = true
}

// settleOffer 在时刻 now 触及一份通知，按规则做惰性结算（变更记入 tx）：
//   - PENDING 且 now > Deadline：过期，释放占用，不触发冷却；
//   - ACCEPTED 且 now > EntryDate+grace：放弃，释放占用，记录冷却。
//
// 恰等于临界日不结算（当天仍可答复 / 入职）。
func (s *Service) settleOffer(o *Offer, now int, tx *settleTx) string {
	switch o.Status {
	case StatusPending:
		if now > o.Deadline {
			s.releasePending(o, tx)
			old := *o
			o.Status = StatusExpired
			tx.undo = append(tx.undo, func() { *o = old })
			return fmt.Sprintf("offer %d lazy-expired at day %d (deadline %d), headcount released", o.ID, now, o.Deadline)
		}
	case StatusAccepted:
		limit := o.EntryDate + s.grace
		if now > limit {
			s.releasePending(o, tx)
			old := *o
			o.Status = StatusAbandoned
			o.RespondedAt = now
			tx.undo = append(tx.undo, func() { *o = old })
			s.markBlock(o.CandidateID, o.PositionID, now, tx)
			return fmt.Sprintf("offer %d lazy-abandoned at day %d (entry %d + grace %d = %d), headcount released, cooldown starts",
				o.ID, now, o.EntryDate, s.grace, limit)
		}
	}
	return ""
}

// settleCandidate 触及候选人当前未决通知（发放唯一性判定前使用）。
func (s *Service) settleCandidate(candidateID string, now int, tx *settleTx) string {
	id, ok := s.candidatePending[candidateID]
	if !ok {
		return ""
	}
	return s.settleOffer(s.offers[id], now, tx)
}

// releasePending 将一份 PENDING/ACCEPTED 通知从未决占用与候选人索引中移除；
// 所有变更登记到 tx，以便操作被拒绝时回滚。
func (s *Service) releasePending(o *Offer, tx *settleTx) {
	s.pendingCnt[o.PositionID]--
	_, hadCandidate := s.candidatePending[o.CandidateID]
	set := s.pendingByPos[o.PositionID]
	_, hadInSet := set[o.ID]
	delete(s.candidatePending, o.CandidateID)
	delete(set, o.ID)
	tx.undo = append(tx.undo, func() {
		s.pendingCnt[o.PositionID]++
		if hadCandidate {
			s.candidatePending[o.CandidateID] = o.ID
		}
		if hadInSet {
			if s.pendingByPos[o.PositionID] == nil {
				s.pendingByPos[o.PositionID] = map[int64]bool{}
			}
			s.pendingByPos[o.PositionID][o.ID] = true
		}
	})
}

// addPending 登记一份新的未决通知（操作已被接受，无需回滚记录）。
func (s *Service) addPending(o *Offer) {
	s.pendingCnt[o.PositionID]++
	s.candidatePending[o.CandidateID] = o.ID
	set := s.pendingByPos[o.PositionID]
	if set == nil {
		set = map[int64]bool{}
		s.pendingByPos[o.PositionID] = set
	}
	set[o.ID] = true
}

// settlePosition 触及岗位全部当前未决通知。
// 遍历规模等于未决数（有界于编制总数），不扫描历史通知。
func (s *Service) settlePosition(positionID string, now int, tx *settleTx) []string {
	set := s.pendingByPos[positionID]
	if set == nil {
		return nil
	}
	ids := make([]int64, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	var reasons []string
	for _, id := range ids {
		if r := s.settleOffer(s.offers[id], now, tx); r != "" {
			reasons = append(reasons, r)
		}
	}
	return reasons
}

// markBlock 记录候选人在岗位上最近一次拒绝/放弃日期（变更记入 tx）。
func (s *Service) markBlock(candidateID, positionID string, day int, tx *settleTx) {
	m := s.lastBlock[candidateID]
	if m == nil {
		m = map[string]int{}
		s.lastBlock[candidateID] = m
	}
	old, existed := m[positionID]
	m[positionID] = day
	tx.undo = append(tx.undo, func() {
		if existed {
			s.lastBlock[candidateID][positionID] = old
		} else {
			delete(s.lastBlock[candidateID], positionID)
		}
	})
}

// inCooldown 判断候选人在岗位上是否处于冷却中：
// now-blockDay < cooldown 拒绝；恰等于 cooldown 允许。
func (s *Service) inCooldown(candidateID, positionID string, now int) (bool, int) {
	if m := s.lastBlock[candidateID]; m != nil {
		if blockDay, ok := m[positionID]; ok && now-blockDay < s.cooldown {
			return true, blockDay
		}
	}
	return false, 0
}

// occupied 为岗位当前已占用（在岗 + 未决），O(1)。
func (s *Service) occupied(positionID string) int {
	return s.onboardedCnt[positionID] + s.pendingCnt[positionID]
}

func (s *Service) exceptionKey(positionID string, quarter int) string {
	return fmt.Sprintf("%s/%d", positionID, quarter)
}

// log 输出一步操作的判定日志。
func (s *Service) log(op, input string, ok bool, code Code, output, reason string) {
	if s.logger == nil {
		return
	}
	s.seq++
	s.logger(StepLog{
		Seq: s.seq, Op: op, Input: input, OK: ok, Code: code,
		Output: output, Reason: reason,
	})
}
