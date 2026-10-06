package seal

import "fmt"

// CreateSeal registers a seal with exactly two distinct custodians.
func (s *Service) CreateSeal(now int64, id, category, custodianA, custodianB string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	input := fmt.Sprintf("id=%q category=%q custodians=(%q,%q) now=%d", id, category, custodianA, custodianB, now)
	switch {
	case badID(id), category == "", badID(custodianA), badID(custodianB), custodianA == custodianB, now < 0:
		s.emit(now, "CreateSeal", input, string(ErrInvalidParameter), "missing/blank fields, identical custodians or negative time")
		return sealErr(ErrInvalidParameter, "seal id/category required and custodians must differ")
	}
	if e := s.checkClock(now); e != nil {
		s.emit(now, "CreateSeal", input, string(e.Code), e.Reason)
		return e
	}
	if _, exists := s.seals[id]; exists {
		s.emit(now, "CreateSeal", input, string(ErrInvalidParameter), "seal id already exists")
		return sealErr(ErrInvalidParameter, "seal id already exists")
	}

	s.seals[id] = &Seal{ID: id, Category: category, Status: SealNormal, CustodianA: custodianA, CustodianB: custodianB}
	s.advanceClock(now)
	s.emit(now, "CreateSeal", input, "OK", "seal registered in NORMAL status with two custodians")
	return nil
}

// SetSealStatus enables or disables a seal. Disabling immediately invalidates
// every un-executed application on the seal (status DEAD); re-enabling does not
// revive them.
func (s *Service) SetSealStatus(now int64, sealID string, status SealStatus, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	input := fmt.Sprintf("seal=%q status=%q reason=%q now=%d", sealID, status, reason, now)
	if badID(sealID) || (status != SealNormal && status != SealDisabled) || now < 0 {
		s.emit(now, "SetSealStatus", input, string(ErrInvalidParameter), "unknown status or bad seal id")
		return sealErr(ErrInvalidParameter, "bad seal id or status")
	}
	if e := s.checkClock(now); e != nil {
		s.emit(now, "SetSealStatus", input, string(e.Code), e.Reason)
		return e
	}
	seal := s.seals[sealID]
	if seal == nil {
		s.emit(now, "SetSealStatus", input, string(ErrNotFound), "seal does not exist")
		return sealErr(ErrNotFound, "unknown seal "+sealID)
	}
	if seal.Status == status {
		s.emit(now, "SetSealStatus", input, string(ErrStateNotAllowed), fmt.Sprintf("seal already %s", status))
		return sealErr(ErrStateNotAllowed, "seal already in requested status")
	}

	seal.Status = status
	basis := fmt.Sprintf("seal is now %s", status)
	if status == SealDisabled {
		killed := 0
		for _, app := range s.apps {
			if app.SealID == sealID && app.Status != AppExecuted && app.Status != AppVoided &&
				app.Status != AppRejected && app.Status != AppDead {
				app.Status = AppDead
				killed++
			}
		}
		basis = fmt.Sprintf("seal disabled; %d un-executed applications moved to DEAD (no auto-revival on re-enable)", killed)
	}
	s.advanceClock(now)
	s.emit(now, "SetSealStatus", input, "OK", basis)
	return nil
}

// GrantAuthorization adds a grant. The supplied Grant must have a unique ID,
// reference an existing seal, contain at least one material, positive caps and a
// half-open window (ValidFrom < ValidUntil). It may be granted while the seal is
// currently disabled.
func (s *Service) GrantAuthorization(now int64, g Grant) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	input := fmt.Sprintf("grant=%q employee=%q seal=%q materials={%s} cap=%d daily=%d window=[%d,%d) now=%d",
		g.ID, g.Employee, g.SealID, joinMaterials(g.Materials), g.AmountCap, g.DailyCap, g.ValidFrom, g.ValidUntil, now)
	switch {
	case badID(g.ID), badID(g.Employee), badID(g.SealID), now < 0:
		s.emit(now, "GrantAuthorization", input, string(ErrInvalidParameter), "missing ids or negative time")
		return sealErr(ErrInvalidParameter, "grant/employee/seal id required")
	case g.AmountCap <= 0, g.DailyCap <= 0, len(g.Materials) == 0:
		s.emit(now, "GrantAuthorization", input, string(ErrInvalidParameter), "caps must be positive and materials non-empty")
		return sealErr(ErrInvalidParameter, "amount cap and daily cap must be positive; materials non-empty")
	case g.ValidFrom >= g.ValidUntil:
		s.emit(now, "GrantAuthorization", input, string(ErrInvalidParameter), "validity window must be half-open [from,until)")
		return sealErr(ErrInvalidParameter, "valid from must precede valid until")
	}
	for m := range g.Materials {
		if m == "" {
			s.emit(now, "GrantAuthorization", input, string(ErrInvalidParameter), "blank material category")
			return sealErr(ErrInvalidParameter, "material categories must be non-blank")
		}
	}
	if e := s.checkClock(now); e != nil {
		s.emit(now, "GrantAuthorization", input, string(e.Code), e.Reason)
		return e
	}
	if _, exists := s.grants[g.ID]; exists {
		s.emit(now, "GrantAuthorization", input, string(ErrInvalidParameter), "grant id already exists")
		return sealErr(ErrInvalidParameter, "grant id already exists")
	}
	if s.seals[g.SealID] == nil {
		s.emit(now, "GrantAuthorization", input, string(ErrNotFound), "seal does not exist")
		return sealErr(ErrNotFound, "unknown seal "+g.SealID)
	}

	materials := make(map[string]struct{}, len(g.Materials))
	for m := range g.Materials {
		materials[m] = struct{}{}
	}
	stored := g
	stored.Materials = materials
	stored.Revoked = false
	s.grants[g.ID] = &stored
	s.advanceClock(now)
	s.emit(now, "GrantAuthorization", input, "OK", "grant stored; status is independent of seal status")
	return nil
}

// RevokeGrant revokes a grant. Already executed uses are unaffected; un-executed
// applications bound to it will be refused at execution re-check.
func (s *Service) RevokeGrant(now int64, grantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	input := fmt.Sprintf("grant=%q now=%d", grantID, now)
	if badID(grantID) || now < 0 {
		s.emit(now, "RevokeGrant", input, string(ErrInvalidParameter), "blank grant id or negative time")
		return sealErr(ErrInvalidParameter, "grant id required")
	}
	if e := s.checkClock(now); e != nil {
		s.emit(now, "RevokeGrant", input, string(e.Code), e.Reason)
		return e
	}
	g := s.grants[grantID]
	if g == nil {
		s.emit(now, "RevokeGrant", input, string(ErrNotFound), "grant does not exist")
		return sealErr(ErrNotFound, "unknown grant "+grantID)
	}
	if g.Revoked {
		s.emit(now, "RevokeGrant", input, string(ErrStateNotAllowed), "grant already revoked")
		return sealErr(ErrStateNotAllowed, "grant already revoked")
	}

	g.Revoked = true
	s.advanceClock(now)
	s.emit(now, "RevokeGrant", input, "OK", "grant revoked; executed uses kept, pending applications rejected at execution")
	return nil
}
