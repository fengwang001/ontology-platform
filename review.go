package ontology

import (
	"ontology/ledger"
	"ontology/policy"
)

// Approve 批准授权。
func (s *Service) Approve(now int64, id, approver string) error {
	return s.review(now, id, approver, true)
}

// Reject 驳回授权。
func (s *Service) Reject(now int64, id, approver string) error {
	return s.review(now, id, approver, false)
}

func (s *Service) review(now int64, id, approver string, approve bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validNow(now) || id == "" || approver == "" {
		return ErrInvalidParam
	}
	if now < s.clock {
		return ErrClockRegression
	}
	g, ok := s.grants[id]
	if !ok {
		return ErrGrantNotFound
	}
	if approver == g.Requester {
		return ErrSelfReview
	}
	a, ok := s.actors[approver]
	if !ok || !canReview(a, g.Resource) {
		return ErrPermission
	}
	if g.Reviewed() {
		return ErrAlreadyDecided
	}
	if now > g.Start+g.Rw {
		return ErrOverdue
	}
	kind := ledger.KindReject
	if approve {
		g.Approve(now)
		kind = ledger.KindApprove
	} else {
		g.Reject(now)
	}
	s.clock = now
	s.book.Append(now, kind, id, approver, g.PolicyVer)
	return nil
}

func canReview(a actor, resource string) bool {
	if a.role == policy.Security {
		return true
	}
	if a.role != policy.Manager {
		return false
	}
	for _, sc := range a.scopes {
		if policy.Covers(sc, resource) {
			return true
		}
	}
	return false
}
