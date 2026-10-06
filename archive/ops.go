package archive

import (
	"strconv"
)

// SetClassification 下调/调整借阅人最高密级（管理操作，不推进时钟）。
func (s *Service) SetClassification(userID string, maxClass Classification) Outcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	if userID == "" || !maxClass.Valid() {
		return bad(ErrInvalidParam, "bad user id or classification")
	}
	u, ok := s.users[userID]
	if !ok {
		return bad(ErrNotFound, "user not found: "+userID)
	}
	u.maxClass = maxClass
	return okOut("classification set")
}

// Seal 封存一卷：在库立即封存；借出/待取中的卷在归还或放弃后封存，排队预约一并取消。
func (s *Service) Seal(volumeID string) Outcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	if volumeID == "" {
		return bad(ErrInvalidParam, "empty volume id")
	}
	v, ok := s.volumes[volumeID]
	if !ok {
		return bad(ErrNotFound, "volume not found: "+volumeID)
	}
	switch v.status {
	case VolSealed:
		return bad(ErrState, "already sealed")
	case VolInLibrary:
		v.status = VolSealed
		v.queue.clear()
	default:
		v.sealPending = true
		v.queue.clear()
	}
	return okOut("sealed")
}

// borrowCheck 的判定顺序即错误优先级在借阅路径上的具体化。
func (s *Service) borrowCheck(u *user, v *volume) Outcome {
	if v.status == VolSealed {
		return bad(ErrState, "volume sealed")
	}
	if u.maxClass < v.class {
		return bad(ErrClearance, "clearance insufficient")
	}
	if u.status == UserSuspended {
		return bad(ErrSuspended, "user suspended")
	}
	if v.status == VolLent {
		return bad(ErrAlreadyLent, "volume lent or held")
	}
	return okOut("")
}

// Borrow 借阅单卷。
func (s *Service) Borrow(now int, userID, volumeID string) Outcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	if userID == "" || volumeID == "" {
		return bad(ErrInvalidParam, "empty id")
	}
	if o := s.checkClock(now); !o.OK {
		return o
	}
	u, v, o := s.resolve(userID, volumeID)
	if !o.OK {
		return o
	}
	if o := s.borrowCheck(u, v); !o.OK {
		return o
	}
	s.tick(now)
	if o := s.borrowCheck(u, v); !o.OK {
		return o
	}
	v.status = VolLent
	v.loan = &LoanInfo{UserID: userID, StartDay: now, DueDay: now + s.cfg.LoanDays[v.class]}
	u.activeLoans++
	return okOut("borrowed due=" + strconv.Itoa(v.loan.DueDay))
}

// BorrowBatch 多卷同借，全有或全无。failIndex 为失败项下标（-1 为成功或全局失败）。
// 先对每卷按下标判定，再在所有失败项中取错误优先级最高（Err 值最小）者；同优先级取最小下标。
func (s *Service) BorrowBatch(now int, userID string, volumeIDs []string) (Outcome, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if userID == "" || len(volumeIDs) == 0 {
		return bad(ErrInvalidParam, "empty request"), -1
	}
	seen := map[string]bool{}
	for _, id := range volumeIDs {
		if id == "" || seen[id] {
			return bad(ErrInvalidParam, "duplicate or empty volume in batch"), -1
		}
		seen[id] = true
	}
	if o := s.checkClock(now); !o.OK {
		return o, -1
	}
	u, ok := s.users[userID]
	if !ok {
		return bad(ErrNotFound, "user not found: "+userID), -1
	}
	type cand struct {
		v *volume
		i int
		o Outcome
	}
	cands := make([]cand, 0, len(volumeIDs))
	for i, id := range volumeIDs {
		v, vok := s.volumes[id]
		if !vok {
			cands = append(cands, cand{nil, i, bad(ErrNotFound, "volume not found: "+id)})
			continue
		}
		cands = append(cands, cand{v, i, s.borrowCheck(u, v)})
	}
	worst := -1
	var wo Outcome
	for _, c := range cands {
		if c.o.OK {
			continue
		}
		if worst == -1 || c.o.Err < wo.Err || (c.o.Err == wo.Err && c.i < worst) {
			worst, wo = c.i, c.o
		}
	}
	if worst != -1 {
		return wo, worst
	}
	s.tick(now)
	for _, c := range cands {
		if o := s.borrowCheck(u, c.v); !o.OK {
			return o, c.i
		}
	}
	for _, c := range cands {
		c.v.status = VolLent
		c.v.loan = &LoanInfo{UserID: userID, StartDay: now, DueDay: now + s.cfg.LoanDays[c.v.class]}
	}
	u.activeLoans += len(cands)
	return okOut("borrowed " + strconv.Itoa(len(cands)) + " volumes"), -1
}

// reserveCheck：封存(状态)优先于密级；暂停不拦截预约（预约冻结、不参与分配）。
func (s *Service) reserveCheck(u *user, v *volume) Outcome {
	if v.status == VolSealed {
		return bad(ErrState, "volume sealed")
	}
	if u.maxClass < v.class {
		return bad(ErrClearance, "clearance insufficient")
	}
	if v.queue.contains(u.id) {
		return bad(ErrState, "duplicate reservation")
	}
	if v.status == VolInLibrary {
		return bad(ErrState, "volume in library; borrow instead")
	}
	return okOut("")
}

// Reserve 对借出（含待取）中的卷预约排队。
func (s *Service) Reserve(now int, userID, volumeID string) Outcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	if userID == "" || volumeID == "" {
		return bad(ErrInvalidParam, "empty id")
	}
	if o := s.checkClock(now); !o.OK {
		return o
	}
	u, v, o := s.resolve(userID, volumeID)
	if !o.OK {
		return o
	}
	if o := s.reserveCheck(u, v); !o.OK {
		return o
	}
	s.tick(now)
	if o := s.reserveCheck(u, v); !o.OK {
		return o
	}
	v.queue.enqueue(userID)
	return okOut("queued pos=" + strconv.Itoa(v.queue.len()))
}
