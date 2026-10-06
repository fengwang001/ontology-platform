package lease

import "sync"

type Service struct {
	mu           sync.Mutex
	config       Config
	lastNow      int
	nextOfferID  uint64
	leases       map[string]Lease
	offers       map[string][]Offer
	offerByID    map[uint64]Offer
	offerIndex   map[uint64]int
	currentOffer map[string]uint64
}

func NewService(config Config) (*Service, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	return &Service{
		config:       config,
		leases:       map[string]Lease{},
		offers:       map[string][]Offer{},
		offerByID:    map[uint64]Offer{},
		offerIndex:   map[uint64]int{},
		currentOffer: map[string]uint64{},
	}, nil
}

func (s *Service) CreateLease(nowDay int, input CreateLeaseInput) (Snapshot, error) {
	if nowDay < 0 {
		return Snapshot{}, invalidArgument("time must be non-negative")
	}
	if err := validateCreateLeaseInput(input); err != nil {
		return Snapshot{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(nowDay); err != nil {
		return Snapshot{}, err
	}
	if _, exists := s.leases[input.ID]; exists {
		return Snapshot{}, Error{Code: InvalidState, Reason: "lease already exists"}
	}
	lease := Lease{
		ID:               input.ID,
		StartDay:         input.StartDay,
		EndDay:           input.EndDay,
		MonthlyRentCents: input.MonthlyRentCents,
		LastAdjustmentAt: input.LastAdjustmentAt,
	}
	s.leases[input.ID] = lease
	s.lastNow = nowDay
	return s.snapshotLocked(lease, nowDay), nil
}

func (s *Service) IssueOffer(nowDay int, input IssueOfferInput) (Snapshot, error) {
	if nowDay < 0 || input.LeaseID == "" || input.RentCents <= 0 || input.NewEndDay <= 0 {
		return Snapshot{}, invalidArgument("invalid offer rent, end day, or lease id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(nowDay); err != nil {
		return Snapshot{}, err
	}
	lease, ok := s.leases[input.LeaseID]
	if !ok {
		return Snapshot{}, Error{Code: LeaseNotFound, Reason: "lease does not exist"}
	}
	if input.NewEndDay <= lease.EndDay {
		return Snapshot{}, invalidArgument("renewal end day must follow current end day")
	}
	if !withinOfferWindow(nowDay, lease.EndDay, s.config.MinOfferDaysBeforeEnd, s.config.MaxOfferDaysBeforeEnd) {
		return Snapshot{}, Error{Code: InvalidState, Reason: "offer is outside renewal window"}
	}
	view := s.snapshotLocked(lease, nowDay)
	if view.CurrentOffer != nil {
		return Snapshot{}, Error{Code: InvalidState, Reason: "offer is not allowed in current lease state"}
	}
	if view.Status == StatusMonthToMonth || view.Status == StatusTerminated {
		return Snapshot{}, Error{Code: InvalidState, Reason: "offer is not allowed after continuation or termination"}
	}
	years := fullYearsSince(lease.LastAdjustmentAt, nowDay)
	limit := maximumRentCents(lease.MonthlyRentCents, years, s.config.AnnualStepBasisPoints, s.config.CapBasisPoints)
	if input.RentCents > limit {
		return Snapshot{}, Error{Code: RentAboveCap, Reason: "proposed rent exceeds statutory increase cap"}
	}
	s.nextOfferID++
	offer := Offer{
		ID:          s.nextOfferID,
		LeaseID:     input.LeaseID,
		RentCents:   input.RentCents,
		NewEndDay:   input.NewEndDay,
		IssuedAt:    nowDay,
		ResponseDue: nowDay + s.config.ResponseDays,
		Awaiting:    Tenant,
	}
	s.offers[input.LeaseID] = append(s.offers[input.LeaseID], offer)
	s.offerByID[offer.ID] = offer
	s.offerIndex[offer.ID] = len(s.offers[input.LeaseID]) - 1
	s.currentOffer[input.LeaseID] = offer.ID
	lease.EverReceivedOffer = true
	s.leases[input.LeaseID] = lease
	s.lastNow = nowDay
	return s.snapshotLocked(lease, nowDay), nil
}

func (s *Service) WithdrawOffer(nowDay int, input WithdrawInput) (Snapshot, error) {
	if nowDay < 0 || input.LeaseID == "" || input.OfferID == 0 {
		return Snapshot{}, invalidArgument("invalid lease or offer id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(nowDay); err != nil {
		return Snapshot{}, err
	}
	lease, ok := s.leases[input.LeaseID]
	if !ok {
		return Snapshot{}, Error{Code: LeaseNotFound, Reason: "lease does not exist"}
	}
	offer, offerIndex, ok := s.currentOfferLocked(input.LeaseID, input.OfferID)
	if !ok || offer.Awaiting != Tenant || offer.ResponseDue < nowDay {
		return Snapshot{}, Error{Code: InvalidState, Reason: "offer cannot be withdrawn in current state"}
	}
	offer.Resolved = true
	s.offers[input.LeaseID][offerIndex] = offer
	s.offerByID[offer.ID] = offer
	delete(s.currentOffer, input.LeaseID)
	s.lastNow = nowDay
	return s.snapshotLocked(lease, nowDay), nil
}

func (s *Service) TenantRespond(nowDay int, input RespondInput) (Snapshot, error) {
	if nowDay < 0 || input.LeaseID == "" || input.OfferID == 0 {
		return Snapshot{}, invalidArgument("invalid lease or offer id")
	}
	switch input.Decision {
	case Accept, Reject, Counter:
	default:
		return Snapshot{}, invalidArgument("tenant decision must be accept, reject, or counter")
	}
	if input.Decision == Counter && input.CounterRentCents <= 0 {
		return Snapshot{}, invalidArgument("counter rent must be non-negative")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(nowDay); err != nil {
		return Snapshot{}, err
	}
	lease, ok := s.leases[input.LeaseID]
	if !ok {
		return Snapshot{}, Error{Code: LeaseNotFound, Reason: "lease does not exist"}
	}
	offer, offerIndex, ok := s.currentOfferLocked(input.LeaseID, input.OfferID)
	if !ok || offer.Awaiting != Tenant {
		return Snapshot{}, Error{Code: InvalidState, Reason: "offer is not awaiting tenant response"}
	}
	if nowDay > offer.ResponseDue {
		return Snapshot{}, Error{Code: LateResponse, Reason: "tenant response is past deadline"}
	}
	switch input.Decision {
	case Accept:
		lease = s.renewLeaseLocked(lease, offer.RentCents, offer.NewEndDay)
		offer.Accepted = true
		offer.Resolved = true
	case Reject:
		offer.Resolved = true
	case Counter:
		if offer.CounterUsed || !strictlyBetweenRent(input.CounterRentCents, lease.MonthlyRentCents, offer.RentCents) {
			return Snapshot{}, Error{Code: InvalidState, Reason: "counter offer is unavailable or outside strict rent interval"}
		}
		offer.CounterUsed = true
		offer.CounterRentCents = input.CounterRentCents
		offer.CounterAt = nowDay
		offer.CounterDue = nowDay + s.config.ResponseDays
		offer.Awaiting = Landlord
	}
	s.offers[input.LeaseID][offerIndex] = offer
	s.offerByID[offer.ID] = offer
	if offer.Resolved {
		delete(s.currentOffer, input.LeaseID)
	}
	s.leases[input.LeaseID] = lease
	s.lastNow = nowDay
	return s.snapshotLocked(lease, nowDay), nil
}

func (s *Service) LandlordRespondToCounter(nowDay int, input RespondInput) (Snapshot, error) {
	if nowDay < 0 || input.LeaseID == "" || input.OfferID == 0 {
		return Snapshot{}, invalidArgument("invalid lease or offer id")
	}
	switch input.Decision {
	case Accept, Reject:
	default:
		return Snapshot{}, invalidArgument("landlord decision must be accept or reject")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(nowDay); err != nil {
		return Snapshot{}, err
	}
	lease, ok := s.leases[input.LeaseID]
	if !ok {
		return Snapshot{}, Error{Code: LeaseNotFound, Reason: "lease does not exist"}
	}
	offer, offerIndex, ok := s.currentOfferLocked(input.LeaseID, input.OfferID)
	if !ok || offer.Awaiting != Landlord || !offer.CounterUsed {
		return Snapshot{}, Error{Code: InvalidState, Reason: "counter offer is not awaiting landlord response"}
	}
	if nowDay > offer.CounterDue {
		return Snapshot{}, Error{Code: LateResponse, Reason: "landlord counter response is past deadline"}
	}
	if input.Decision == Accept {
		lease = s.renewLeaseLocked(lease, offer.CounterRentCents, offer.NewEndDay)
		offer.Accepted = true
	}
	offer.Resolved = true
	s.offers[input.LeaseID][offerIndex] = offer
	s.offerByID[offer.ID] = offer
	delete(s.currentOffer, input.LeaseID)
	s.leases[input.LeaseID] = lease
	s.lastNow = nowDay
	return s.snapshotLocked(lease, nowDay), nil
}

func (s *Service) GiveTerminationNotice(nowDay int, input NoticeInput) (Snapshot, error) {
	if nowDay < 0 || input.LeaseID == "" {
		return Snapshot{}, invalidArgument("lease id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(nowDay); err != nil {
		return Snapshot{}, err
	}
	lease, ok := s.leases[input.LeaseID]
	if !ok {
		return Snapshot{}, Error{Code: LeaseNotFound, Reason: "lease does not exist"}
	}
	view := s.snapshotLocked(lease, nowDay)
	if view.Status != StatusMonthToMonth || lease.TerminationNoticeAt != 0 {
		return Snapshot{}, Error{Code: InvalidState, Reason: "termination notice is only allowed once in month-to-month continuation"}
	}
	lease.TerminationNoticeAt = nowDay
	lease.TerminationEffectiveAt = nowDay + s.config.TerminationNoticeDays
	s.leases[input.LeaseID] = lease
	s.lastNow = nowDay
	return s.snapshotLocked(lease, nowDay), nil
}

func (s *Service) Snapshot(nowDay int, leaseID string) (Snapshot, error) {
	if leaseID == "" || nowDay < 0 {
		return Snapshot{}, invalidArgument("invalid lease id or time")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, ok := s.leases[leaseID]
	if !ok {
		return Snapshot{}, Error{Code: LeaseNotFound, Reason: "lease does not exist"}
	}
	return s.snapshotLocked(lease, nowDay), nil
}

func (s *Service) checkClock(nowDay int) error {
	if nowDay < s.lastNow {
		return Error{Code: ClockRollback, Reason: "operation time is before the latest accepted operation"}
	}
	return nil
}

func (s *Service) currentOfferLocked(leaseID string, offerID uint64) (Offer, int, bool) {
	currentID, ok := s.currentOffer[leaseID]
	if !ok || currentID != offerID {
		return Offer{}, -1, false
	}
	index, ok := s.offerIndex[offerID]
	if !ok || index >= len(s.offers[leaseID]) || s.offers[leaseID][index].ID != offerID {
		return Offer{}, -1, false
	}
	return s.offerByID[offerID], index, true
}

func (s *Service) renewLeaseLocked(lease Lease, rentCents, newEndDay int) Lease {
	lease.StartDay = lease.EndDay
	lease.EndDay = newEndDay
	if rentCents != lease.MonthlyRentCents {
		lease.LastAdjustmentAt = lease.StartDay
	}
	lease.MonthlyRentCents = rentCents
	lease.EverReceivedOffer = false
	lease.TerminationNoticeAt = 0
	lease.TerminationEffectiveAt = 0
	return lease
}

func (s *Service) snapshotLocked(lease Lease, nowDay int) Snapshot {
	status := StatusActive
	if !lease.EverReceivedOffer && nowDay >= lease.EndDay-s.config.MinOfferDaysBeforeEnd {
		status = StatusProtected
	}
	if nowDay >= lease.EndDay {
		status = StatusMonthToMonth
	}

	var currentOffer *Offer
	offerStatus := OfferStatus("")
	if offerID, ok := s.currentOffer[lease.ID]; ok {
		offer := s.offerByID[offerID]
		due := offer.ResponseDue
		if offer.Awaiting == Landlord {
			due = offer.CounterDue
		}
		if nowDay > due {
			offerStatus = OfferExpired
			if nowDay >= lease.EndDay {
				status = StatusMonthToMonth
			}
		} else {
			offerStatus = OfferOpen
			if nowDay >= lease.EndDay {
				status = StatusActive
			}
		}
		currentOffer = &offer
	}
	if lease.TerminationEffectiveAt != 0 && nowDay >= lease.TerminationEffectiveAt {
		status = StatusTerminated
	}
	return Snapshot{Lease: lease, Status: status, CurrentOffer: currentOffer, OfferStatus: offerStatus}
}
