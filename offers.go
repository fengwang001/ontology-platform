package staffing

type issueContext struct {
	now               int
	reserved          map[string]int
	released          map[string]int
	reservedCandidate map[string]bool
	exception         map[string]map[int]int
	pendingExpiry     []*Offer
}

func newIssueContext(now int) *issueContext {
	return &issueContext{
		now:               now,
		reserved:          make(map[string]int),
		released:          make(map[string]int),
		reservedCandidate: make(map[string]bool),
		exception:         make(map[string]map[int]int),
	}
}

func (s *Service) IssueOffer(in IssueOfferInput) (*Offer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := newIssueContext(in.Now)
	if err := s.validateIssue(in, ctx); err != nil {
		return nil, err
	}
	s.applyPendingExpiries(ctx)
	offer := s.commitIssue(in)
	return copyOffer(offer), nil
}

func (s *Service) BatchIssueOffers(in []IssueOfferInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(in) == 0 {
		return errorf(ErrInvalidArgument, "batch cannot be empty")
	}
	ctx := newIssueContext(in[0].Now)
	for i, item := range in {
		if err := s.validateIssue(item, ctx); err != nil {
			if serviceErr, ok := err.(*Error); ok {
				serviceErr.Index = i
			}
			return err
		}
	}
	s.applyPendingExpiries(ctx)
	for _, item := range in {
		s.commitIssue(item)
	}
	return nil
}

func (s *Service) validateIssue(in IssueOfferInput, ctx *issueContext) error {
	if in.Now < 0 || in.OfferID == "" || in.CandidateID == "" || in.PositionID == "" || in.Salary < 0 || in.Deadline < in.Now {
		return errorf(ErrInvalidArgument, "invalid offer input")
	}
	if in.Now != ctx.now {
		return errorf(ErrInvalidArgument, "batch must use one operation time")
	}
	if err := s.checkClock(in.Now); err != nil {
		return err
	}
	position, ok := s.positions[in.PositionID]
	if !ok {
		return errorf(ErrNotFound, "position %q not found", in.PositionID)
	}
	if _, ok := s.candidates[in.CandidateID]; !ok {
		return errorf(ErrNotFound, "candidate %q not found", in.CandidateID)
	}
	if _, exists := s.offers[in.OfferID]; exists {
		return errorf(ErrStatusNotAllowed, "offer %q already exists", in.OfferID)
	}
	if position.Status == PositionFrozen {
		return errorf(ErrPositionFrozen, "position %q is frozen", in.PositionID)
	}
	s.prepareCandidateForIssue(in.CandidateID, in.Now, ctx)
	if position.Occupied()+ctx.reserved[in.PositionID]-ctx.released[in.PositionID] >= position.Total {
		return errorf(ErrHeadcountFull, "position %q is full", in.PositionID)
	}
	outOfBand := in.Salary < position.MinSalary || in.Salary > position.MaxSalary
	if outOfBand {
		if err := s.reserveException(in, ctx); err != nil {
			return err
		}
	}
	if ctx.reservedCandidate[in.CandidateID] {
		return errorf(ErrCandidateHasPendingOffer, "candidate %q already has an active offer in this batch", in.CandidateID)
	}
	if activeOfferID := s.activeOfferID[in.CandidateID]; activeOfferID != "" && !s.isPreparedExpiry(activeOfferID, ctx) {
		return errorf(ErrCandidateHasPendingOffer, "candidate %q already has an active offer", in.CandidateID)
	}
	if byPosition, ok := s.lastExit[in.CandidateID]; ok {
		if day, rejected := byPosition[in.PositionID]; rejected && in.Now-day < s.cooldownDays {
			return errorf(ErrCoolingDown, "candidate %q is cooling down for position %q", in.CandidateID, in.PositionID)
		}
	}
	ctx.reserved[in.PositionID]++
	ctx.reservedCandidate[in.CandidateID] = true
	return nil
}

func (s *Service) prepareCandidateForIssue(candidateID string, now int, ctx *issueContext) {
	if ctx.reservedCandidate[candidateID] {
		return
	}
	offerID := s.activeOfferID[candidateID]
	if offerID == "" {
		return
	}
	offer := s.offers[offerID]
	if offer.Status == OfferPending && now > offer.Deadline {
		ctx.pendingExpiry = append(ctx.pendingExpiry, offer)
		ctx.released[offer.PositionID]++
	}
}

func (s *Service) isPreparedExpiry(offerID string, ctx *issueContext) bool {
	for _, offer := range ctx.pendingExpiry {
		if offer.ID == offerID {
			return true
		}
	}
	return false
}

func (s *Service) applyPendingExpiries(ctx *issueContext) {
	for _, offer := range ctx.pendingExpiry {
		if offer.Status == OfferPending {
			s.expireOffer(offer, ctx.now)
		}
	}
}

func (s *Service) reserveException(in IssueOfferInput, ctx *issueContext) error {
	if in.ExceptionApprovalID == "" {
		return errorf(ErrSalaryBandWithoutApproval, "salary %d is outside band", in.Salary)
	}
	grant, ok := s.exceptions[in.ExceptionApprovalID]
	if !ok || grant.positionID != in.PositionID {
		return errorf(ErrSalaryBandWithoutApproval, "salary %d is outside band without a valid approval", in.Salary)
	}
	quarter := quarterOf(in.Now)
	used := grant.usedInQuarter(quarter) + ctx.exception[grant.id][quarter]
	if used >= grant.fixedUses {
		return errorf(ErrSalaryBandWithoutApproval, "exception approval %q has no uses left for quarter %d", grant.id, quarter)
	}
	if _, ok := ctx.exception[grant.id]; !ok {
		ctx.exception[grant.id] = make(map[int]int)
	}
	ctx.exception[grant.id][quarter]++
	return nil
}

func (s *Service) commitIssue(in IssueOfferInput) *Offer {
	offer := &Offer{
		ID:            in.OfferID,
		CandidateID:   in.CandidateID,
		PositionID:    in.PositionID,
		Salary:        in.Salary,
		Deadline:      in.Deadline,
		Status:        OfferPending,
		ExceptionUsed: in.ExceptionApprovalID,
	}
	if in.Salary < s.positions[in.PositionID].MinSalary || in.Salary > s.positions[in.PositionID].MaxSalary {
		grant := s.exceptions[in.ExceptionApprovalID]
		grant.useInQuarter(quarterOf(in.Now))
	} else {
		offer.ExceptionUsed = ""
	}
	s.offers[offer.ID] = offer
	s.offersByCandidate[offer.CandidateID] = append(s.offersByCandidate[offer.CandidateID], offer)
	s.activeOfferID[offer.CandidateID] = offer.ID
	s.positions[offer.PositionID].Pending++
	s.lastAcceptedNow = in.Now
	return offer
}

func (g *exceptionGrant) usedInQuarter(quarter int) int {
	return g.uses[quarter]
}

func (g *exceptionGrant) useInQuarter(quarter int) {
	g.uses[quarter]++
}

func copyOffer(offer *Offer) *Offer {
	copied := *offer
	return &copied
}
