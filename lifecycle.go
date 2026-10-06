package staffing

func (s *Service) RespondToOffer(now int, offerID string, accept bool, startDay int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now < 0 || offerID == "" || (accept && startDay < now) {
		return errorf(ErrInvalidArgument, "invalid offer response")
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	offer, err := s.touchOffer(offerID, now)
	if err != nil {
		return err
	}
	if offer.Status != OfferPending {
		return errorf(ErrStatusNotAllowed, "offer %q is %s", offerID, offer.Status)
	}
	if accept {
		offer.Status = OfferAccepted
		offer.StartDay = startDay
		s.lastAcceptedNow = now
		return nil
	}
	s.releaseReservedOffer(offer)
	offer.Status = OfferRejected
	offer.RejectedDay = now
	s.recordExit(offer, now)
	s.lastAcceptedNow = now
	return nil
}

func (s *Service) WithdrawOffer(now int, offerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now < 0 || offerID == "" {
		return errorf(ErrInvalidArgument, "invalid withdrawal")
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	offer, err := s.touchOffer(offerID, now)
	if err != nil {
		return err
	}
	if offer.Status != OfferPending {
		return errorf(ErrStatusNotAllowed, "offer %q is %s and cannot be withdrawn", offerID, offer.Status)
	}
	s.releaseReservedOffer(offer)
	offer.Status = OfferWithdrawn
	s.lastAcceptedNow = now
	return nil
}

func (s *Service) CancelAcceptedOffer(now int, offerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now < 0 || offerID == "" {
		return errorf(ErrInvalidArgument, "invalid cancellation")
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	offer, err := s.touchOffer(offerID, now)
	if err != nil {
		return err
	}
	if offer.Status != OfferAccepted {
		return errorf(ErrStatusNotAllowed, "offer %q is %s and cannot be canceled", offerID, offer.Status)
	}
	s.releaseReservedOffer(offer)
	offer.Status = OfferCanceled
	offer.CanceledDay = now
	s.lastAcceptedNow = now
	return nil
}

func (s *Service) Onboard(now int, offerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now < 0 || offerID == "" {
		return errorf(ErrInvalidArgument, "invalid onboarding")
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	offer, err := s.touchOffer(offerID, now)
	if err != nil {
		return err
	}
	if offer.Status != OfferAccepted {
		return errorf(ErrStatusNotAllowed, "offer %q is %s and cannot be onboarded", offerID, offer.Status)
	}
	if now < offer.StartDay {
		return errorf(ErrStatusNotAllowed, "offer %q is not due for onboarding", offerID)
	}
	position := s.positions[offer.PositionID]
	position.Pending--
	position.OnDuty++
	offer.Status = OfferOnboarded
	offer.OnboardedDay = now
	delete(s.activeOfferID, offer.CandidateID)
	s.lastAcceptedNow = now
	return nil
}

func (s *Service) TerminateEmployment(now int, offerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now < 0 || offerID == "" {
		return errorf(ErrInvalidArgument, "invalid termination")
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	offer, ok := s.offers[offerID]
	if !ok {
		return errorf(ErrNotFound, "offer %q not found", offerID)
	}
	if offer.Status != OfferOnboarded {
		return errorf(ErrStatusNotAllowed, "offer %q holder is not employed", offerID)
	}
	position := s.positions[offer.PositionID]
	position.OnDuty--
	offer.Status = OfferTerminated
	offer.TerminatedDay = now
	s.lastAcceptedNow = now
	return nil
}

func (s *Service) touchOffer(offerID string, now int) (*Offer, error) {
	offer, ok := s.offers[offerID]
	if !ok {
		return nil, errorf(ErrNotFound, "offer %q not found", offerID)
	}
	if offer.Status == OfferPending && now > offer.Deadline {
		s.expireOffer(offer, now)
		s.lastAcceptedNow = now
		return nil, errorf(ErrOfferExpired, "offer %q expired on day %d", offerID, offer.Deadline)
	}
	if offer.Status == OfferAccepted && now > offer.StartDay+s.graceDays {
		s.abandonOffer(offer, now)
		s.lastAcceptedNow = now
		return nil, errorf(ErrOfferExpired, "accepted offer %q was abandoned", offerID)
	}
	return offer, nil
}

func (s *Service) expireOffer(offer *Offer, now int) {
	s.releaseReservedOffer(offer)
	offer.Status = OfferExpiredState
}

func (s *Service) abandonOffer(offer *Offer, now int) {
	s.releaseReservedOffer(offer)
	offer.Status = OfferAbandoned
	offer.AbandonedDay = now
	s.recordExit(offer, now)
}

func (s *Service) releaseReservedOffer(offer *Offer) {
	if offer.Status != OfferPending && offer.Status != OfferAccepted {
		return
	}
	s.positions[offer.PositionID].Pending--
	delete(s.activeOfferID, offer.CandidateID)
}

func (s *Service) recordExit(offer *Offer, day int) {
	byPosition, ok := s.lastExit[offer.CandidateID]
	if !ok {
		byPosition = make(map[string]int)
		s.lastExit[offer.CandidateID] = byPosition
	}
	byPosition[offer.PositionID] = day
}
