package grant

import (
	"ontology/ledger"
	"ontology/policy"
)

// review 是 Approve 与 Reject 的公共实现。拒绝顺序：参数非法、
// 时钟回退、授权不存在、自评、权限、已决、逾期（恰等截止允许）。
func (s *Service) review(now int64, id, approver string, approve bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" || approver == "" {
		return ErrParam
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	g, ok := s.grants[id]
	if !ok {
		return ErrNotFound
	}
	if approver == g.Requester {
		return ErrSelfReview
	}
	p, ok := s.principals[approver]
	if !ok || !canReview(p, g.Resource) {
		return ErrPermission
	}
	if g.decided() {
		return ErrDecided
	}
	if now > g.Start+g.Rw {
		return ErrOverdue
	}
	s.clock = now
	kind := ledger.KindReject
	if approve {
		g.ApprovedAt = now
		kind = ledger.KindApprove
	} else {
		g.RejectedAt = now
	}
	s.led.Append(now, kind, id, approver, g.PolicyVer)
	return nil
}

// canReview 报告已登记主体是否可评审 resource 上的授权：
// security 均可，manager 须某个作用域覆盖该资源。
func canReview(p principal, resource string) bool {
	switch p.role {
	case policy.RoleSecurity:
		return true
	case policy.RoleManager:
		for _, sc := range p.scopes {
			if policy.Covers(sc, resource) {
				return true
			}
		}
	}
	return false
}

// Approve 在评审窗口内批准授权。
func (s *Service) Approve(now int64, id, approver string) error {
	return s.review(now, id, approver, true)
}

// Reject 在评审窗口内驳回授权。
func (s *Service) Reject(now int64, id, approver string) error {
	return s.review(now, id, approver, false)
}

// Access 判定 principal 在时刻 t 对 resource 是否有有效授权。
// 资源非法为参数错误，t 大于当前时钟为未来时刻错误；
// 未登记的 principal 恒为假。只考察 principal 自己的授权。
func (s *Service) Access(principal, resource string, t int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !policy.ValidPath(resource) || t < 0 {
		return false, ErrParam
	}
	if t > s.clock {
		return false, ErrFuture
	}
	if _, ok := s.principals[principal]; !ok {
		return false, nil
	}
	for _, g := range s.byRequester[principal] {
		s.accessChecks++
		if policy.Covers(g.Resource, resource) && g.activeAt(t) {
			return true, nil
		}
	}
	return false, nil
}
