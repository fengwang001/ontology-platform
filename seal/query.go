package seal

import "sort"

// AppView is a detached, point-in-time copy of an application.
type AppView struct {
	ID         string
	Applicant  string
	SealID     string
	Material   string
	Amount     int64
	GrantID    string
	Status     AppStatus
	Approvers  []string // approved voters, sorted
	Rejecter   string   // populated when REJECTED
	ApprovalAt int64
	ValidUntil int64
	Presence   []string
	ExecutedAt int64
	Deadline   int64
	ReceiptAt  int64
	VoidedAt   int64
}

// GetApp returns a detached copy of the application.
func (s *Service) GetApp(appID string) (AppView, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	app := s.apps[appID]
	if app == nil {
		return AppView{}, false
	}
	v := AppView{
		ID:         app.ID,
		Applicant:  app.Applicant,
		SealID:     app.SealID,
		Material:   app.Material,
		Amount:     app.Amount,
		GrantID:    app.GrantID,
		Status:     app.Status,
		ApprovalAt: app.ApprovalAt,
		ValidUntil: app.ValidUntil,
		ExecutedAt: app.ExecutedAt,
		Deadline:   app.Deadline,
		ReceiptAt:  app.ReceiptAt,
		VoidedAt:   app.VoidedAt,
	}
	for person, approved := range app.Approvals {
		if approved {
			v.Approvers = append(v.Approvers, person)
		} else {
			v.Rejecter = person
		}
	}
	sort.Strings(v.Approvers)
	v.Presence = append(v.Presence, app.Presence...)
	return v, true
}

// IsFrozen evaluates the freeze condition at time t (O(1) heap-root check).
// It does not advance the clock because it is a pure query.
func (s *Service) IsFrozen(t int64, employee string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	frozen, _ := s.frozenAt(employee, t)
	return frozen
}

// DailyUsed returns successful executions counted against a grant on a given
// natural day (day index = floor(t/86400)).
func (s *Service) DailyUsed(grantID string, dayIndex int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.daily[grantID][dayIndex]
}

// LastAcceptedNow returns the monotone service clock.
func (s *Service) LastAcceptedNow() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastNow
}
