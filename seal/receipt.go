package seal

import "fmt"

// RegisterReceipt records the physical receipt for an executed application.
// Registering exactly at the deadline is valid. Late registration is allowed:
// it removes one overrun entry, and the freeze lifts automatically only once
// every overrun for the employee has been cleared. A lifted freeze does not
// retroactively undo any previously rejected operation.
func (s *Service) RegisterReceipt(now int64, appID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	input := fmt.Sprintf("app=%q now=%d", appID, now)
	if badID(appID) || now < 0 {
		s.emit(now, "RegisterReceipt", input, string(ErrInvalidParameter), "blank app id or negative time")
		return sealErr(ErrInvalidParameter, "app id required")
	}
	if e := s.checkClock(now); e != nil {
		s.emit(now, "RegisterReceipt", input, string(e.Code), e.Reason)
		return e
	}
	app := s.apps[appID]
	if app == nil {
		s.emit(now, "RegisterReceipt", input, string(ErrNotFound), "application does not exist")
		return sealErr(ErrNotFound, "unknown application "+appID)
	}
	if app.Status != AppExecuted && app.Status != AppVoided {
		s.emit(now, "RegisterReceipt", input, string(ErrStateNotAllowed),
			fmt.Sprintf("status=%s; receipt only follows execution", app.Status))
		return sealErr(ErrStateNotAllowed, "application has not been executed")
	}
	if app.ReceiptAt != 0 {
		s.emit(now, "RegisterReceipt", input, string(ErrStateNotAllowed),
			fmt.Sprintf("receipt already registered at %d", app.ReceiptAt))
		return sealErr(ErrStateNotAllowed, "receipt already registered")
	}

	onTime := now <= app.Deadline
	app.ReceiptAt = now
	s.heapRemoveOne(app.Applicant, app.Deadline)

	frozenBefore, cause := s.frozenAt(app.Applicant, now)
	stillFrozen := frozenBefore
	if len(s.overruns[app.Applicant]) == 0 {
		s.unfreezeLocked(app.Applicant)
		stillFrozen = false
	}

	s.advanceClock(now)
	basis := fmt.Sprintf("receipt recorded at %d; deadline=%d on_time=%v", now, app.Deadline, onTime)
	if onTime {
		s.emit(now, "RegisterReceipt", input, "OK", basis+"; no freeze triggered")
	} else if stillFrozen {
		basis += fmt.Sprintf("; late: freeze persists, %d other overdue receipt(s) remain (earliest cause=%d)",
			len(s.overruns[app.Applicant]), cause)
		s.emit(now, "RegisterReceipt", input, "OK", basis)
	} else {
		basis += "; late: all overdue receipts cleared, freeze lifted (earlier rejections stand)"
		s.emit(now, "RegisterReceipt", input, "OK", basis)
	}
	return nil
}

// MarkVoid lets a custodian mark an executed application void. A void cannot be
// undone and does not refund the daily count consumed at execution. Voiding does
// not remove the receipt obligation.
func (s *Service) MarkVoid(now int64, appID, custodian string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	input := fmt.Sprintf("app=%q custodian=%q now=%d", appID, custodian, now)
	if badID(appID) || badID(custodian) || now < 0 {
		s.emit(now, "MarkVoid", input, string(ErrInvalidParameter), "blank ids or negative time")
		return sealErr(ErrInvalidParameter, "app id and custodian required")
	}
	if e := s.checkClock(now); e != nil {
		s.emit(now, "MarkVoid", input, string(e.Code), e.Reason)
		return e
	}
	app := s.apps[appID]
	if app == nil {
		s.emit(now, "MarkVoid", input, string(ErrNotFound), "application does not exist")
		return sealErr(ErrNotFound, "unknown application "+appID)
	}
	if app.Status != AppExecuted {
		s.emit(now, "MarkVoid", input, string(ErrStateNotAllowed),
			fmt.Sprintf("status=%s; only executed uses can be voided", app.Status))
		return sealErr(ErrStateNotAllowed, "application is not executed")
	}
	seal := s.seals[app.SealID]
	if !s.isCustodian(seal, custodian) {
		s.emit(now, "MarkVoid", input, string(ErrInvalidParameter),
			fmt.Sprintf("%q is not a custodian of seal %q", custodian, app.SealID))
		return sealErr(ErrInvalidParameter, "only a custodian may mark a use void")
	}

	app.Status = AppVoided
	app.VoidedAt = now
	s.advanceClock(now)
	s.emit(now, "MarkVoid", input, "OK",
		"executed use marked VOID; daily count is NOT refunded; receipt obligation unchanged")
	return nil
}
