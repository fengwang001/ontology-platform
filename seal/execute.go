package seal

import "fmt"

// ConfirmPresence records that one of the seal's two custodians is present.
// It is only meaningful for APPROVED applications whose amount reaches the
// dual-presence threshold. The same custodian may not confirm twice.
func (s *Service) ConfirmPresence(now int64, appID, custodian string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	input := fmt.Sprintf("app=%q custodian=%q now=%d", appID, custodian, now)
	if badID(appID) || badID(custodian) || now < 0 {
		s.emit(now, "ConfirmPresence", input, string(ErrInvalidParameter), "blank ids or negative time")
		return sealErr(ErrInvalidParameter, "app id and custodian required")
	}
	if e := s.checkClock(now); e != nil {
		s.emit(now, "ConfirmPresence", input, string(e.Code), e.Reason)
		return e
	}
	app := s.apps[appID]
	if app == nil {
		s.emit(now, "ConfirmPresence", input, string(ErrNotFound), "application does not exist")
		return sealErr(ErrNotFound, "unknown application "+appID)
	}
	if app.Status != AppApproved {
		s.emit(now, "ConfirmPresence", input, string(ErrStateNotAllowed), fmt.Sprintf("status=%s", app.Status))
		return sealErr(ErrStateNotAllowed, "presence can only be confirmed for an approved application")
	}
	if app.Amount < s.policy.DualPresence {
		s.emit(now, "ConfirmPresence", input, string(ErrStateNotAllowed),
			fmt.Sprintf("amount=%d below dual-presence threshold=%d", app.Amount, s.policy.DualPresence))
		return sealErr(ErrStateNotAllowed, "this amount does not require presence confirmation")
	}
	seal := s.seals[app.SealID]
	if !s.isCustodian(seal, custodian) {
		s.emit(now, "ConfirmPresence", input, string(ErrInvalidParameter),
			fmt.Sprintf("person %q is not a custodian of seal %q", custodian, app.SealID))
		return sealErr(ErrInvalidParameter, "only the two named custodians may confirm presence")
	}
	for _, prior := range app.Presence {
		if prior == custodian {
			s.emit(now, "ConfirmPresence", input, string(ErrStateNotAllowed), "same custodian confirmed twice")
			return sealErr(ErrStateNotAllowed, "the two confirmations must come from different custodians")
		}
	}
	if len(app.Presence) >= 2 {
		s.emit(now, "ConfirmPresence", input, string(ErrStateNotAllowed), "two confirmations already recorded")
		return sealErr(ErrStateNotAllowed, "presence already fully confirmed")
	}

	app.Presence = append(app.Presence, custodian)
	s.advanceClock(now)
	s.emit(now, "ConfirmPresence", input, "OK",
		fmt.Sprintf("custodian %q recorded (%d/2 distinct confirmations)", custodian, len(app.Presence)))
	return nil
}

// Execute is the ONLY operation that consumes the daily quota. Every check is
// evaluated against the state at execution time, in the documented error
// priority; on any failure nothing changes (no quota, no application state, no
// clock) and the application may be retried while still valid.
func (s *Service) Execute(now int64, appID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	input := fmt.Sprintf("app=%q now=%d", appID, now)
	if badID(appID) || now < 0 {
		s.emit(now, "Execute", input, string(ErrInvalidParameter), "blank app id or negative time")
		return sealErr(ErrInvalidParameter, "app id required")
	}
	if e := s.checkClock(now); e != nil {
		s.emit(now, "Execute", input, string(e.Code), e.Reason)
		return e
	}
	app := s.apps[appID]
	if app == nil {
		s.emit(now, "Execute", input, string(ErrNotFound), "application does not exist")
		return sealErr(ErrNotFound, "unknown application "+appID)
	}

	// State checks: must be APPROVED and inside the fixed approval-validity window.
	// Executing exactly at ValidUntil is allowed ("到达末刻仍可执行"); one second
	// later is not.
	if app.Status != AppApproved {
		s.emit(now, "Execute", input, string(ErrStateNotAllowed),
			fmt.Sprintf("status=%s (an executed application can never execute twice)", app.Status))
		return sealErr(ErrStateNotAllowed, "application is not in APPROVED state")
	}
	if now > app.ValidUntil {
		s.emit(now, "Execute", input, string(ErrStateNotAllowed),
			fmt.Sprintf("approval validity ended at %d (now=%d)", app.ValidUntil, now))
		return sealErr(ErrStateNotAllowed, "approval validity expired")
	}

	seal := s.seals[app.SealID]
	if seal.Status != SealNormal {
		s.emit(now, "Execute", input, string(ErrStateNotAllowed), "seal is DISABLED")
		return sealErr(ErrStateNotAllowed, "seal disabled at execution time")
	}

	// Freeze blocks execution of all still-un-executed applications.
	if frozen, deadline := s.frozenAt(app.Applicant, now); frozen {
		s.emit(now, "Execute", input, string(ErrFrozen),
			fmt.Sprintf("applicant frozen; un-receipted deadline=%d < now=%d", deadline, now))
		return sealErr(ErrFrozen, "applicant is frozen")
	}

	// Grant re-check at execution time: the binding must still exist, be
	// non-revoked and cover now (half-open [ValidFrom, ValidUntil)).
	g := s.grants[app.GrantID]
	if g == nil || g.Employee != app.Applicant || g.SealID != app.SealID || !g.Effective(now) {
		s.emit(now, "Execute", input, string(ErrNoGrant), "bound grant missing/revoked/out of window at execution")
		return sealErr(ErrNoGrant, "authorization no longer effective")
	}

	// Material membership and per-use amount cap are enforced here, not at submit.
	if _, ok := g.Materials[app.Material]; !ok {
		s.emit(now, "Execute", input, string(ErrLimit),
			fmt.Sprintf("material=%q not in grant set {%s}", app.Material, joinMaterials(g.Materials)))
		return sealErr(ErrLimit, "material category not permitted by grant")
	}
	if app.Amount > g.AmountCap {
		s.emit(now, "Execute", input, string(ErrLimit),
			fmt.Sprintf("amount=%d exceeds per-use cap=%d", app.Amount, g.AmountCap))
		return sealErr(ErrLimit, "amount exceeds per-use cap")
	}

	// Daily quota: one integer lookup keyed by (grant, day index). "低于上限"
	// means count < cap, so reaching the cap exactly refuses the next use.
	day := dayIndex(now)
	counts := s.daily[g.ID]
	if counts == nil {
		counts = map[int64]int{}
		s.daily[g.ID] = counts
	}
	if counts[day] >= g.DailyCap {
		s.emit(now, "Execute", input, string(ErrDailyLimit),
			fmt.Sprintf("day=%d used=%d cap=%d (O(1) counter, no history scan)", day, counts[day], g.DailyCap))
		return sealErr(ErrDailyLimit, "daily usage limit reached")
	}

	// Dual presence is checked last per the mandated error priority.
	if app.Amount >= s.policy.DualPresence && len(app.Presence) != 2 {
		s.emit(now, "Execute", input, string(ErrMissingPresence),
			fmt.Sprintf("amount=%d >= threshold=%d requires two distinct custodians; recorded=%v",
				app.Amount, s.policy.DualPresence, app.Presence))
		return sealErr(ErrMissingPresence, "two distinct custodian confirmations required")
	}

	// Commit point: single mutex means this transition, the quota increment and
	// the receipt-deadline push are atomic -> no concurrent double execution.
	counts[day]++
	app.Status = AppExecuted
	app.ExecutedAt = now
	app.Deadline = now + s.policy.ReceiptWindow
	s.heapPush(app.Applicant, app.Deadline)
	s.advanceClock(now)
	s.emit(now, "Execute", input, "OK",
		fmt.Sprintf("all checks passed at now<=%d; status=EXECUTED; day=%d count=%d/%d; receipt due by %d (equal included)",
			app.ValidUntil, day, counts[day], g.DailyCap, app.Deadline))
	return nil
}
