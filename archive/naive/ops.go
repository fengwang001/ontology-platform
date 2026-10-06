package naive

import (
	"fmt"

	"ontology/archive"
)

func (m *Model) clockOK(now int) bool { return now >= m.lastNow }

func (m *Model) borrowEligible(uid string, v *nVolume) (archive.ErrorCode, string, bool) {
	u := m.users[uid]
	if v.status == archive.VolSealed {
		return archive.ErrState, "sealed", false
	}
	if u.maxClass < v.class {
		return archive.ErrClearance, "clearance", false
	}
	if u.status == archive.UserSuspended {
		return archive.ErrSuspended, "suspended", false
	}
	if v.status == archive.VolLent {
		return archive.ErrAlreadyLent, "lent", false
	}
	return archive.OK, "", true
}

// Borrow 借阅单卷。
func (m *Model) Borrow(now int, uid, vid string) Result {
	if uid == "" || vid == "" {
		return rfail(archive.ErrInvalidParam, "empty id")
	}
	if !m.clockOK(now) {
		return rfail(archive.ErrClockRollback, "rollback")
	}
	u, uok := m.users[uid]
	v, vok := m.volumes[vid]
	if !uok || !vok {
		return rfail(archive.ErrNotFound, "not found")
	}
	if e, r, ok := m.borrowEligible(uid, v); !ok {
		return rfail(e, r)
	}
	m.settle(now)
	if e, r, ok := m.borrowEligible(uid, v); !ok {
		return rfail(e, r)
	}
	v.status = archive.VolLent
	v.loan = &nLoan{user: uid, start: now, due: now + m.cfg.LoanDays[v.class]}
	u.activeLoans++
	return rgood("borrowed")
}

// BorrowBatch 多卷同借，全有或全无。
func (m *Model) BorrowBatch(now int, uid string, vids []string) Result {
	if uid == "" || len(vids) == 0 {
		return rfail(archive.ErrInvalidParam, "empty")
	}
	seen := map[string]bool{}
	for _, id := range vids {
		if id == "" || seen[id] {
			return rfail(archive.ErrInvalidParam, "dup")
		}
		seen[id] = true
	}
	if !m.clockOK(now) {
		return rfail(archive.ErrClockRollback, "rollback")
	}
	u, ok := m.users[uid]
	if !ok {
		return rfail(archive.ErrNotFound, "no user")
	}
	type ce struct {
		idx int
		e   archive.ErrorCode
		r   string
	}
	var bads []ce
	for i, id := range vids {
		v, vok := m.volumes[id]
		if !vok {
			bads = append(bads, ce{i, archive.ErrNotFound, "no volume"})
			continue
		}
		if e, r, ok := m.borrowEligible(uid, v); !ok {
			bads = append(bads, ce{i, e, r})
		}
	}
	if len(bads) > 0 {
		w := bads[0]
		for _, c := range bads[1:] {
			if c.e < w.e || (c.e == w.e && c.idx < w.idx) {
				w = c
			}
		}
		return Result{OK: false, Err: w.e, Reason: w.r, FailIndex: w.idx}
	}
	m.settle(now)
	for i, id := range vids {
		v := m.volumes[id]
		if e, r, ok := m.borrowEligible(uid, v); !ok {
			return Result{OK: false, Err: e, Reason: r, FailIndex: i}
		}
	}
	for _, id := range vids {
		v := m.volumes[id]
		v.status = archive.VolLent
		v.loan = &nLoan{user: uid, start: now, due: now + m.cfg.LoanDays[v.class]}
	}
	u.activeLoans += len(vids)
	return rgood("batch")
}

func (m *Model) reserveEligible(uid string, v *nVolume) (archive.ErrorCode, string, bool) {
	u := m.users[uid]
	if v.status == archive.VolSealed {
		return archive.ErrState, "sealed", false
	}
	if u.maxClass < v.class {
		return archive.ErrClearance, "clearance", false
	}
	for _, q := range v.queue {
		if q == uid {
			return archive.ErrState, "dup reserve", false
		}
	}
	if v.status == archive.VolInLibrary {
		return archive.ErrState, "in library", false
	}
	return archive.OK, "", true
}

// Reserve 预约。
func (m *Model) Reserve(now int, uid, vid string) Result {
	if uid == "" || vid == "" {
		return rfail(archive.ErrInvalidParam, "empty id")
	}
	if !m.clockOK(now) {
		return rfail(archive.ErrClockRollback, "rollback")
	}
	_, uok := m.users[uid]
	v, vok := m.volumes[vid]
	if !uok || !vok {
		return rfail(archive.ErrNotFound, "not found")
	}
	if e, r, ok := m.reserveEligible(uid, v); !ok {
		return rfail(e, r)
	}
	m.settle(now)
	if e, r, ok := m.reserveEligible(uid, v); !ok {
		return rfail(e, r)
	}
	v.queue = append(v.queue, uid)
	return rgood("reserved")
}

// CancelReservation 取消预约（含待取）。
func (m *Model) CancelReservation(now int, uid, vid string) Result {
	if uid == "" || vid == "" {
		return rfail(archive.ErrInvalidParam, "empty id")
	}
	if !m.clockOK(now) {
		return rfail(archive.ErrClockRollback, "rollback")
	}
	if _, ok := m.users[uid]; !ok {
		return rfail(archive.ErrNotFound, "no user")
	}
	v, ok := m.volumes[vid]
	if !ok {
		return rfail(archive.ErrNotFound, "no volume")
	}
	m.settle(now)
	if v.hold != nil && v.hold.user == uid {
		m.abandonHold(v, now)
		return rgood("cancel hold")
	}
	if !contains(v.queue, uid) {
		return rfail(archive.ErrState, "no reservation")
	}
	v.queue = removeFirst(v.queue, uid)
	return rgood("cancelled")
}

// Pickup 取卷。
func (m *Model) Pickup(now int, uid, vid string) Result {
	if uid == "" || vid == "" {
		return rfail(archive.ErrInvalidParam, "empty id")
	}
	if !m.clockOK(now) {
		return rfail(archive.ErrClockRollback, "rollback")
	}
	u, uok := m.users[uid]
	v, vok := m.volumes[vid]
	if !uok || !vok {
		return rfail(archive.ErrNotFound, "not found")
	}
	check := func() Result {
		if v.hold == nil || v.hold.user != uid {
			return rfail(archive.ErrState, "no hold")
		}
		if u.status == archive.UserSuspended {
			return rfail(archive.ErrSuspended, "suspended")
		}
		if u.maxClass < v.class {
			return rfail(archive.ErrClearance, "clearance")
		}
		return rgood("")
	}
	if r := check(); !r.OK {
		return r
	}
	m.settle(now)
	if r := check(); !r.OK {
		if v.hold == nil || v.hold.user != uid {
			return rfail(archive.ErrState, "hold expired")
		}
		return r
	}
	v.loan = &nLoan{user: uid, start: now, due: now + m.cfg.LoanDays[v.class]}
	v.hold = nil
	v.status = archive.VolLent
	u.activeLoans++
	return rgood("picked")
}

// Return 归还。
func (m *Model) Return(now int, uid, vid string) Result {
	if uid == "" || vid == "" {
		return rfail(archive.ErrInvalidParam, "empty id")
	}
	if !m.clockOK(now) {
		return rfail(archive.ErrClockRollback, "rollback")
	}
	u, uok := m.users[uid]
	v, vok := m.volumes[vid]
	if !uok || !vok {
		return rfail(archive.ErrNotFound, "not found")
	}
	if v.loan == nil || v.loan.user != uid {
		return rfail(archive.ErrState, "not borrower")
	}
	m.settle(now)
	overdue := 0
	if now > v.loan.due {
		overdue = now - v.loan.due
	}
	v.loan = nil
	u.activeLoans--
	u.lastReturn = now
	if overdue > 0 {
		u.overdueTotal += overdue
		if u.overdueTotal >= m.cfg.OverdueThreshold && u.status == archive.UserActive {
			u.status = archive.UserSuspended
		}
	}
	if v.sealPending {
		v.sealPending = false
		v.status = archive.VolSealed
		v.queue = nil
	} else {
		v.status = archive.VolInLibrary
		m.assign(v, now)
	}
	return rgood("returned")
}

// Renew 续借。
func (m *Model) Renew(now int, uid, vid, approver string) Result {
	if uid == "" || vid == "" {
		return rfail(archive.ErrInvalidParam, "empty id")
	}
	if !m.clockOK(now) {
		return rfail(archive.ErrClockRollback, "rollback")
	}
	if approver == uid {
		return rfail(archive.ErrInvalidParam, "self approval")
	}
	u, uok := m.users[uid]
	v, vok := m.volumes[vid]
	if !uok || !vok {
		return rfail(archive.ErrNotFound, "not found")
	}
	if v.class >= archive.ClassConfidential && approver == "" {
		return rfail(archive.ErrInvalidParam, "approver required")
	}
	if v.loan == nil || v.loan.user != uid {
		return rfail(archive.ErrState, "not borrower")
	}
	if v.sealPending {
		return rfail(archive.ErrState, "seal pending")
	}
	if u.maxClass < v.class {
		return rfail(archive.ErrClearance, "clearance")
	}
	if u.status == archive.UserSuspended {
		return rfail(archive.ErrSuspended, "suspended")
	}
	windowStart := v.loan.due - m.cfg.RenewWindowDays
	if now < windowStart || now > v.loan.due {
		return rfail(archive.ErrRenewNotAllowed,
			fmt.Sprintf("window [%d,%d] now=%d", windowStart, v.loan.due, now))
	}
	if v.loan.renewals >= m.cfg.MaxRenewals {
		return rfail(archive.ErrRenewNotAllowed, "limit")
	}
	if len(v.queue) > 0 {
		return rfail(archive.ErrReservation, "reservation")
	}
	m.settle(now)
	v.loan.due += m.cfg.LoanDays[v.class]
	v.loan.renewals++
	return rgood("renewed")
}

// Advance 仅推进时钟。
func (m *Model) Advance(now int) Result {
	if !m.clockOK(now) {
		return rfail(archive.ErrClockRollback, "rollback")
	}
	m.settle(now)
	return rgood("advanced")
}

// SetClassification 管理操作。
func (m *Model) SetClassification(uid string, c archive.Classification) Result {
	u, ok := m.users[uid]
	if !ok {
		return rfail(archive.ErrNotFound, "no user")
	}
	u.maxClass = c
	return rgood("set")
}

// Seal 封存。
func (m *Model) Seal(vid string) Result {
	v, ok := m.volumes[vid]
	if !ok {
		return rfail(archive.ErrNotFound, "no volume")
	}
	switch v.status {
	case archive.VolSealed:
		return rfail(archive.ErrState, "sealed")
	case archive.VolInLibrary:
		v.status = archive.VolSealed
		v.queue = nil
	default:
		v.sealPending = true
		v.queue = nil
	}
	return rgood("sealed")
}
