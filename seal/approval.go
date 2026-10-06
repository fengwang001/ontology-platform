package seal

import "fmt"

// Submit registers a seal-use application. Only format validation and the
// existence of an effective grant are performed here: no quota is consumed and
// material/amount limits are deliberately NOT checked at submission (they are
// re-checked at execution, by which time the grant may have changed).
func (s *Service) Submit(now int64, appID, applicant, sealID, material string, amount int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	input := fmt.Sprintf("app=%q applicant=%q seal=%q material=%q amount=%d now=%d",
		appID, applicant, sealID, material, amount, now)
	switch {
	case badID(appID), badID(applicant), badID(sealID), material == "", now < 0, amount <= 0:
		s.emit(now, "Submit", input, string(ErrInvalidParameter), "blank fields, non-positive amount or negative time")
		return sealErr(ErrInvalidParameter, "app id/applicant/seal/material required and amount must be positive")
	}
	if e := s.checkClock(now); e != nil {
		s.emit(now, "Submit", input, string(e.Code), e.Reason)
		return e
	}
	if _, exists := s.apps[appID]; exists {
		s.emit(now, "Submit", input, string(ErrInvalidParameter), "application id already exists")
		return sealErr(ErrInvalidParameter, "application id already exists")
	}
	seal := s.seals[sealID]
	if seal == nil {
		s.emit(now, "Submit", input, string(ErrNotFound), "seal does not exist")
		return sealErr(ErrNotFound, "unknown seal "+sealID)
	}
	if seal.Status != SealNormal {
		s.emit(now, "Submit", input, string(ErrStateNotAllowed), "seal is DISABLED")
		return sealErr(ErrStateNotAllowed, "seal disabled; new applications refused")
	}
	if frozen, deadline := s.frozenAt(applicant, now); frozen {
		s.emit(now, "Submit", input, string(ErrFrozen), fmt.Sprintf("earliest un-receipted deadline=%d < now=%d", deadline, now))
		return sealErr(ErrFrozen, fmt.Sprintf("applicant frozen; receipt overdue since %d", deadline))
	}
	g := s.effectiveGrant(applicant, sealID, now)
	if g == nil {
		s.emit(now, "Submit", input, string(ErrNoGrant), "no non-revoked grant covering [now]")
		return sealErr(ErrNoGrant, "applicant holds no effective grant for the seal")
	}

	s.apps[appID] = &Application{
		ID:        appID,
		Applicant: applicant,
		SealID:    sealID,
		Material:  material,
		Amount:    amount,
		GrantID:   g.ID,
		Status:    AppPending,
		Approvals: map[string]bool{},
	}
	s.advanceClock(now)
	s.emit(now, "Submit", input, "OK", fmt.Sprintf("format valid; bound to grant=%q; no quota consumed", g.ID))
	return nil
}

// Approve records an approval vote. Rejected applications are terminal.
func (s *Service) Approve(now int64, appID, approver string) error {
	return s.vote(now, appID, approver, true)
}

// Reject records a rejection vote. Any single rejection terminates the application.
func (s *Service) Reject(now int64, appID, approver string) error {
	return s.vote(now, appID, approver, false)
}

func (s *Service) vote(now int64, appID, approver string, approve bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	op := "Approve"
	if !approve {
		op = "Reject"
	}
	input := fmt.Sprintf("app=%q %s=%q now=%d", appID, op, approver, now)
	if badID(appID) || badID(approver) || now < 0 {
		s.emit(now, op, input, string(ErrInvalidParameter), "blank ids or negative time")
		return sealErr(ErrInvalidParameter, "app id and approver required")
	}
	if e := s.checkClock(now); e != nil {
		s.emit(now, op, input, string(e.Code), e.Reason)
		return e
	}
	app := s.apps[appID]
	if app == nil {
		s.emit(now, op, input, string(ErrNotFound), "application does not exist")
		return sealErr(ErrNotFound, "unknown application "+appID)
	}
	if app.Status != AppPending {
		s.emit(now, op, input, string(ErrStateNotAllowed), fmt.Sprintf("application status=%s", app.Status))
		return sealErr(ErrStateNotAllowed, "application is no longer pending")
	}
	if approver == app.Applicant {
		s.emit(now, op, input, string(ErrStateNotAllowed), "approver must differ from applicant")
		return sealErr(ErrStateNotAllowed, "the applicant may not approve their own application")
	}
	if _, voted := app.Approvals[approver]; voted {
		s.emit(now, op, input, string(ErrStateNotAllowed), "same approver already voted on this application")
		return sealErr(ErrStateNotAllowed, "approver may vote only once per application")
	}
	seal := s.seals[app.SealID]
	need, custodianRequired := s.policy.RequiredApprovals(app.Amount)

	if !approve {
		app.Approvals[approver] = false
		app.Status = AppRejected
		s.advanceClock(now)
		s.emit(now, op, input, "OK", "any single rejection terminates the application: status=REJECTED")
		return nil
	}

	// For the tier requiring a custodian, an approval set that could never
	// contain a custodian is refused without mutating the application.
	if custodianRequired && len(app.Approvals) == need-1 && !s.isCustodian(seal, approver) {
		hasCustodian := false
		for prior := range app.Approvals {
			if s.isCustodian(seal, prior) {
				hasCustodian = true
			}
		}
		if !hasCustodian {
			s.emit(now, op, input, string(ErrStateNotAllowed),
				fmt.Sprintf("tier %d requires a custodian; the final approver must be one of (%q,%q)",
					need, seal.CustodianA, seal.CustodianB))
			return sealErr(ErrStateNotAllowed, "this approval tier requires at least one seal custodian")
		}
	}

	app.Approvals[approver] = true
	if len(app.Approvals) < need {
		s.advanceClock(now)
		s.emit(now, op, input, "OK", fmt.Sprintf("approval %d/%d recorded; still pending", len(app.Approvals), need))
		return nil
	}
	if len(app.Approvals) > need {
		// Defensive: distinct-voter counting makes this unreachable in practice.
		s.emit(now, op, input, string(ErrStateNotAllowed), "more approvers than the tier requires")
		return sealErr(ErrStateNotAllowed, "too many approvers")
	}

	app.Status = AppApproved
	app.ApprovalAt = now
	app.ValidUntil = now + s.policy.ApprovalValidity
	s.advanceClock(now)
	s.emit(now, op, input, "OK", fmt.Sprintf(
		"all %d distinct approvals received (custodian required=%v); approved at %d; executable while now < %d",
		need, custodianRequired, now, app.ValidUntil))
	return nil
}
