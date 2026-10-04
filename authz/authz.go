// Package authz 实现盘点调整的审批权限：授权、批准、驳回。
package authz

import "ontology/adjust"

// Grant 授予人员 Approve / Senior 权限。
func Grant(sys *adjust.System, person adjust.ID, approve, senior bool) error {
	sys.Lock()
	defer sys.Unlock()
	if !adjust.ValidID(person) {
		return adjust.ErrInvalid
	}
	var bits uint8
	if approve {
		bits |= adjust.PermApprove
	}
	if senior {
		bits |= adjust.PermSenior
	}
	sys.SetPermLocked(person, bits)
	return nil
}

// approveLike 实现 Approve 与 Reject 共用的拒绝次序校验。
// 返回库位记录；只有 Approve 的调用方在全部校验通过后才落账。
func check(sys *adjust.System, task, loc, approver adjust.ID) (*adjust.Loc, error) {
	if !adjust.ValidID(task) || !adjust.ValidID(loc) || !adjust.ValidID(approver) {
		return nil, adjust.ErrInvalid
	}
	t, ok := sys.TaskLocked(task)
	if !ok {
		return nil, adjust.ErrNotFound
	}
	l, ok := sys.LocLocked(loc)
	if !ok {
		return nil, adjust.ErrNotFound
	}
	if _, in := t.Locs[loc]; !in {
		return nil, adjust.ErrNotFound
	}
	if t.Closed || l.Phase != adjust.PhasePending {
		return nil, adjust.ErrState
	}
	if sys.PermLocked(approver)&adjust.PermApprove == 0 {
		return nil, adjust.ErrNoApprove
	}
	if approver == l.P1 || approver == l.P2 || approver == l.P3 {
		return nil, adjust.ErrMustSwitch
	}
	diff := l.Pending
	if diff < 0 {
		diff = -diff
	}
	if diff*l.Price > sys.Lim && sys.PermLocked(approver)&adjust.PermSenior == 0 {
		return nil, adjust.ErrNoSenior
	}
	return l, nil
}

// Approve 批准待批调整：book += 差值（差值相对提交时刻，申请之后的 Move 已改 book）。
// book+diff<0 时报库存不足，申请保持 Pending，可重试。
func Approve(sys *adjust.System, task, loc, approver adjust.ID) error {
	sys.Lock()
	defer sys.Unlock()

	l, err := check(sys, task, loc, approver)
	if err != nil {
		return err
	}
	if l.Book+l.Pending < 0 {
		return adjust.ErrUnderstock
	}
	diff := l.Pending
	l.Book += diff
	l.Adj += diff
	l.Phase = adjust.PhaseDone
	l.Pending = 0
	return nil
}

// Reject 驳回申请：库位 Done 且不调整。
func Reject(sys *adjust.System, task, loc, approver adjust.ID) error {
	sys.Lock()
	defer sys.Unlock()

	l, err := check(sys, task, loc, approver)
	if err != nil {
		return err
	}
	l.Phase = adjust.PhaseDone
	l.Pending = 0
	return nil
}
