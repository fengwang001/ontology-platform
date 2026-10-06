package staffing

import (
	"sync"
)

type PositionStatus string

const (
	PositionOpen   PositionStatus = "open"
	PositionFrozen PositionStatus = "frozen"
)

type OfferStatus string

const (
	OfferPending      OfferStatus = "pending"
	OfferAccepted     OfferStatus = "accepted"
	OfferRejected     OfferStatus = "rejected"
	OfferExpiredState OfferStatus = "expired"
	OfferWithdrawn    OfferStatus = "withdrawn"
	OfferCanceled     OfferStatus = "canceled"
	OfferAbandoned    OfferStatus = "abandoned"
	OfferOnboarded    OfferStatus = "onboarded"
	OfferTerminated   OfferStatus = "terminated"
)

type Position struct {
	ID        string
	Level     string
	MinSalary int
	MaxSalary int
	Total     int
	Status    PositionStatus
	OnDuty    int
	Pending   int
}

type exceptionGrant struct {
	id         string
	positionID string
	fixedUses  int
	uses       map[int]int
}

type IssueOfferInput struct {
	Now                 int
	OfferID             string
	CandidateID         string
	PositionID          string
	Salary              int
	Deadline            int
	ExceptionApprovalID string
}

type Offer struct {
	ID            string
	CandidateID   string
	PositionID    string
	Salary        int
	Deadline      int
	Status        OfferStatus
	StartDay      int
	RejectedDay   int
	AbandonedDay  int
	CanceledDay   int
	OnboardedDay  int
	TerminatedDay int
	ExceptionUsed string
}

type Service struct {
	mu                sync.Mutex
	lastAcceptedNow   int
	positions         map[string]*Position
	candidates        map[string]struct{}
	offers            map[string]*Offer
	offersByCandidate map[string][]*Offer
	activeOfferID     map[string]string
	lastExit          map[string]map[string]int
	exceptions        map[string]*exceptionGrant
	cooldownDays      int
	graceDays         int
}

func NewService(cooldownDays, graceDays int) *Service {
	if cooldownDays < 0 || graceDays < 0 {
		panic("staffing: cooldown and grace must be non-negative")
	}
	return &Service{
		positions:         make(map[string]*Position),
		candidates:        make(map[string]struct{}),
		offers:            make(map[string]*Offer),
		offersByCandidate: make(map[string][]*Offer),
		activeOfferID:     make(map[string]string),
		lastExit:          make(map[string]map[string]int),
		exceptions:        make(map[string]*exceptionGrant),
		cooldownDays:      cooldownDays,
		graceDays:         graceDays,
	}
}

func (s *Service) AddCandidate(now int, candidateID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now < 0 || candidateID == "" {
		return errorf(ErrInvalidArgument, "candidate id and non-negative time are required")
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	if _, exists := s.candidates[candidateID]; exists {
		return errorf(ErrStatusNotAllowed, "candidate %q already exists", candidateID)
	}
	s.candidates[candidateID] = struct{}{}
	s.lastAcceptedNow = now
	return nil
}

func (s *Service) AddPosition(now int, p Position) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now < 0 || p.ID == "" || p.Level == "" || p.MinSalary < 0 || p.MaxSalary < p.MinSalary || p.Total < 0 {
		return errorf(ErrInvalidArgument, "invalid position definition")
	}
	if p.Status == "" {
		p.Status = PositionOpen
	}
	if p.Status != PositionOpen && p.Status != PositionFrozen {
		return errorf(ErrInvalidArgument, "unknown position status %q", p.Status)
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	if _, exists := s.positions[p.ID]; exists {
		return errorf(ErrStatusNotAllowed, "position %q already exists", p.ID)
	}
	p.OnDuty = 0
	p.Pending = 0
	s.positions[p.ID] = &p
	s.lastAcceptedNow = now
	return nil
}

func (s *Service) SetPositionStatus(now int, positionID string, status PositionStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now < 0 || positionID == "" || (status != PositionOpen && status != PositionFrozen) {
		return errorf(ErrInvalidArgument, "invalid status change")
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	position, ok := s.positions[positionID]
	if !ok {
		return errorf(ErrNotFound, "position %q not found", positionID)
	}
	position.Status = status
	s.lastAcceptedNow = now
	return nil
}

func (s *Service) AdjustHeadcount(now int, positionID string, total int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now < 0 || positionID == "" || total < 0 {
		return errorf(ErrInvalidArgument, "invalid headcount adjustment")
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	position, ok := s.positions[positionID]
	if !ok {
		return errorf(ErrNotFound, "position %q not found", positionID)
	}
	if total < position.Occupied() {
		return errorf(ErrHeadcountFull, "total %d is below occupied %d", total, position.Occupied())
	}
	position.Total = total
	s.lastAcceptedNow = now
	return nil
}

func (s *Service) GrantException(now int, positionID, approvalID string, uses int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now < 0 || positionID == "" || approvalID == "" || uses < 0 {
		return errorf(ErrInvalidArgument, "invalid exception approval")
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	if _, ok := s.positions[positionID]; !ok {
		return errorf(ErrNotFound, "position %q not found", positionID)
	}
	if _, exists := s.exceptions[approvalID]; exists {
		return errorf(ErrStatusNotAllowed, "exception approval %q already exists", approvalID)
	}
	grant := &exceptionGrant{
		id:         approvalID,
		positionID: positionID,
		fixedUses:  uses,
		uses:       make(map[int]int),
	}
	s.exceptions[approvalID] = grant
	s.lastAcceptedNow = now
	return nil
}

func (s *Service) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	positions := make(map[string]Position, len(s.positions))
	for id, position := range s.positions {
		positions[id] = *position
	}
	candidates := make(map[string]struct{}, len(s.candidates))
	for id := range s.candidates {
		candidates[id] = struct{}{}
	}
	offers := make(map[string]Offer, len(s.offers))
	for id, offer := range s.offers {
		offers[id] = *offer
	}
	activeOffers := make(map[string]string, len(s.activeOfferID))
	for id, offerID := range s.activeOfferID {
		activeOffers[id] = offerID
	}
	lastExit := make(map[string]map[string]int, len(s.lastExit))
	for candidateID, byPosition := range s.lastExit {
		lastExit[candidateID] = make(map[string]int, len(byPosition))
		for positionID, day := range byPosition {
			lastExit[candidateID][positionID] = day
		}
	}
	exceptions := make(map[string]ExceptionSnapshot, len(s.exceptions))
	for id, grant := range s.exceptions {
		used := make(map[int]int, len(grant.uses))
		for quarter, count := range grant.uses {
			used[quarter] = count
		}
		exceptions[id] = ExceptionSnapshot{
			ID:         grant.id,
			PositionID: grant.positionID,
			FixedUses:  grant.fixedUses,
			Used:       used,
		}
	}
	return Snapshot{
		LastAcceptedNow: s.lastAcceptedNow,
		Positions:       positions,
		Candidates:      candidates,
		Offers:          offers,
		ActiveOffers:    activeOffers,
		LastExit:        lastExit,
		Exceptions:      exceptions,
		CooldownDays:    s.cooldownDays,
		GraceDays:       s.graceDays,
	}
}

type Snapshot struct {
	LastAcceptedNow int
	Positions       map[string]Position
	Candidates      map[string]struct{}
	Offers          map[string]Offer
	ActiveOffers    map[string]string
	LastExit        map[string]map[string]int
	Exceptions      map[string]ExceptionSnapshot
	CooldownDays    int
	GraceDays       int
}

type ExceptionSnapshot struct {
	ID         string
	PositionID string
	FixedUses  int
	Used       map[int]int
}

func (p Position) Occupied() int { return p.OnDuty + p.Pending }

func quarterOf(day int) int {
	if day < 0 {
		return -1
	}
	return day / 90
}

func (s *Service) advanceTime(now int) error {
	if now < s.lastAcceptedNow {
		return errorf(ErrClockRolledBack, "now %d is before last accepted now %d", now, s.lastAcceptedNow)
	}
	s.lastAcceptedNow = now
	return nil
}

func (s *Service) checkClock(now int) error {
	if now < s.lastAcceptedNow {
		return errorf(ErrClockRolledBack, "now %d is before last accepted now %d", now, s.lastAcceptedNow)
	}
	return nil
}
