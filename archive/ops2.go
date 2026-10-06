package archive

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// CancelReservation 取消本人预约（含已分配待取；放弃待取会触发继续分配）。
func (s *Service) CancelReservation(now int, userID, volumeID string) Outcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	if userID == "" || volumeID == "" {
		return bad(ErrInvalidParam, "empty id")
	}
	if o := s.checkClock(now); !o.OK {
		return o
	}
	_, v, o := s.resolve(userID, volumeID)
	if !o.OK {
		return o
	}
	s.tick(now)
	if v.hold != nil && v.hold.UserID == userID {
		s.releaseHold(v, now)
		return okOut("cancelled assigned hold")
	}
	var target *qEntry
	for e := v.queue.head; e != nil; e = e.next {
		if e.userID == userID {
			target = e
			break
		}
	}
	if target == nil {
		return bad(ErrState, "no reservation to cancel")
	}
	v.queue.remove(target)
	return okOut("reservation cancelled")
}

func (s *Service) pickupCheck(u *user, v *volume) Outcome {
	if v.hold == nil || v.hold.UserID != u.id {
		return bad(ErrState, "no assigned hold for user")
	}
	if u.status == UserSuspended {
		return bad(ErrSuspended, "user suspended")
	}
	if u.maxClass < v.class {
		return bad(ErrClearance, "clearance now insufficient")
	}
	return okOut("")
}

// Pickup 取走已分配给本人的卷，借期自取卷日起算。
func (s *Service) Pickup(now int, userID, volumeID string) Outcome {
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
	if o := s.pickupCheck(u, v); !o.OK {
		return o
	}
	s.tick(now)
	if o := s.pickupCheck(u, v); !o.OK {
		if v.hold == nil || v.hold.UserID != u.id {
			return bad(ErrState, "hold expired before pickup")
		}
		return o
	}
	loan := &LoanInfo{UserID: userID, StartDay: now, DueDay: now + s.cfg.LoanDays[v.class]}
	v.loan = loan
	v.hold = nil
	v.status = VolLent
	u.activeLoans++
	return okOut("picked up due=" + strconv.Itoa(loan.DueDay))
}

// Return 归还本人借阅中的卷，结算逾期、触发暂停判定与继续分配/封存。
func (s *Service) Return(now int, userID, volumeID string) Outcome {
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
	if v.loan == nil || v.loan.UserID != userID {
		return bad(ErrState, "volume not borrowed by user")
	}
	s.tick(now)
	overdue := 0
	if now > v.loan.DueDay {
		overdue = now - v.loan.DueDay
	}
	v.loan = nil
	u.activeLoans--
	u.lastReturn = now
	if overdue > 0 {
		u.overdueTotal += overdue
		if u.overdueTotal >= s.cfg.OverdueThreshold && u.status == UserActive {
			u.status = UserSuspended
		}
	}
	if v.sealPending {
		v.sealPending = false
		v.status = VolSealed
		v.queue.clear()
	} else {
		v.status = VolInLibrary
		s.assignFromQueue(v, now)
	}
	if overdue > 0 {
		return okOut("returned overdue=" + strconv.Itoa(overdue))
	}
	return okOut("returned on time")
}

// Renew 续借。机密及以上需 approver 同意且 approver 不得是本人；
// 新借期自原借期最后一日的下一日起算（DueDay 顺延一个借期，与申请日无关）。
func (s *Service) Renew(now int, userID, volumeID, approver string) Outcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	if userID == "" || volumeID == "" {
		return bad(ErrInvalidParam, "empty id")
	}
	if o := s.checkClock(now); !o.OK {
		return o
	}
	// 自审批属于参数非法，先于存在性检查（优先级：参数非法 > 不存在）。
	if approver == userID {
		return bad(ErrInvalidParam, "approver must not be borrower")
	}
	u, v, o := s.resolve(userID, volumeID)
	if !o.OK {
		return o
	}
	// 机密及以上是否需要审批取决于卷密级，故该参数检查位于存在性之后（取舍见设计说明）。
	if v.class >= ClassConfidential && approver == "" {
		return bad(ErrInvalidParam, "approver required for classified volume")
	}
	if v.loan == nil || v.loan.UserID != userID {
		return bad(ErrState, "volume not borrowed by user")
	}
	if v.sealPending {
		return bad(ErrState, "volume pending seal")
	}
	if u.maxClass < v.class {
		return bad(ErrClearance, "clearance now insufficient")
	}
	if u.status == UserSuspended {
		return bad(ErrSuspended, "user suspended")
	}
	windowStart := v.loan.DueDay - s.cfg.RenewWindowDays
	if now < windowStart || now > v.loan.DueDay {
		return bad(ErrRenewNotAllowed,
			fmt.Sprintf("renew window closed: now=%d window=[%d,%d]", now, windowStart, v.loan.DueDay))
	}
	if v.loan.RenewalsUsed >= s.cfg.MaxRenewals {
		return bad(ErrRenewNotAllowed, "renewal limit reached")
	}
	if v.queue.len() > 0 {
		return bad(ErrReservation, "active reservation exists")
	}
	s.tick(now)
	v.loan.DueDay += s.cfg.LoanDays[v.class]
	v.loan.RenewalsUsed++
	return okOut("renewed due=" + strconv.Itoa(v.loan.DueDay))
}

// Advance 仅推进时钟并处理待取到期放弃与暂停解除。
func (s *Service) Advance(now int) Outcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	if o := s.checkClock(now); !o.OK {
		return o
	}
	s.tick(now)
	return okOut("advanced to " + strconv.Itoa(now))
}

// ScanSteps 返回某卷最近一次分配扫描访问的存活节点数（开销可验证性）。
func (s *Service) ScanSteps(volumeID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.volumes[volumeID]; ok {
		return v.scanSteps
	}
	return -1
}

// GetVolume / GetUser 返回只读快照（不存在返回 nil）。
func (s *Service) GetVolume(id string) *VolumeState {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.volumes[id]
	if !ok {
		return nil
	}
	vs := &VolumeState{
		ID: v.id, Class: v.class, Status: v.status,
		QueueUserIDs: v.queue.users(), SealPending: v.sealPending,
	}
	if v.loan != nil {
		l := *v.loan
		vs.Loan = &l
	}
	if v.hold != nil {
		h := *v.hold
		vs.Hold = &h
	}
	return vs
}

func (s *Service) GetUser(id string) *UserState {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[id]
	if !ok {
		return nil
	}
	return &UserState{ID: u.id, MaxClass: u.maxClass, Status: u.status,
		OverdueTotal: u.overdueTotal, LastReturn: u.lastReturn}
}

// Snapshot 返回确定性的完整状态文本，供重放与朴素模型比对。
func (s *Service) Snapshot() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b strings.Builder
	b.WriteString("now=" + strconv.Itoa(s.lastNow) + "\n")
	ids := make([]string, 0, len(s.volumes))
	for id := range s.volumes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		v := s.volumes[id]
		fmt.Fprintf(&b, "V %s class=%s status=%s sealPending=%t", id, v.class, v.status, v.sealPending)
		if v.loan != nil {
			fmt.Fprintf(&b, " loan=%s[%d..%d]x%d", v.loan.UserID,
				v.loan.StartDay, v.loan.DueDay, v.loan.RenewalsUsed)
		}
		if v.hold != nil {
			fmt.Fprintf(&b, " hold=%s[%d..%d]", v.hold.UserID,
				v.hold.AssignedDay, v.hold.DeadlineDay)
		}
		fmt.Fprintf(&b, " queue=%v\n", v.queue.users())
	}
	uids := make([]string, 0, len(s.users))
	for id := range s.users {
		uids = append(uids, id)
	}
	sort.Strings(uids)
	for _, id := range uids {
		u := s.users[id]
		fmt.Fprintf(&b, "U %s max=%s status=%s overdue=%d lastReturn=%d loans=%d\n",
			id, u.maxClass, u.status, u.overdueTotal, u.lastReturn, u.activeLoans)
	}
	return b.String()
}
