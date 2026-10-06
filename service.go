package ontology

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

type Config struct {
	EarlyMin, LateMin, CheckInMin int
	MaxDays, WaitDays, RefundDays int
	RevokeAt, Penalty             int
}

type Service struct {
	mu       sync.Mutex
	cfg      Config
	now      int
	hasClock bool
	elevator *elevatorScheduler
	office   *renovationOffice
	deposit  *depositBank
	log      []string
}

type DepositRequest struct {
	Now    int
	House  string
	Amount int
}

type ReservationRequest struct {
	Now                 int
	ID, House, Elevator string
	Start, End          int
}

type ReservationAction struct {
	Now int
	ID  string
}

type PermitRequest struct {
	Now              int
	ID, House        string
	StartDay, EndDay int
	Noisy            bool
}

type PermitAction struct {
	Now int
	ID  string
}

type PermitExtend struct {
	Now, NewEndDay int
	ID             string
}

type HouseAction struct {
	Now   int
	House string
}

type QuietRequest struct {
	Now    int
	Ranges []QuietRange
}

type HolidayRequest struct {
	Now, Day int
}

type State struct {
	Now          int
	Reservations []Reservation
	Permits      []Permit
	Ledger       []LedgerEntry
	Log          []string
}

func NewService(cfg Config) *Service {
	return &Service{
		cfg:      cfg,
		elevator: newElevatorScheduler(),
		office:   newRenovationOffice(),
		deposit:  newDepositBank(),
	}
}

func (s *Service) validateClock(now int, valid bool, ids ...string) error {
	if !valid || now < 0 {
		return fail(ErrInvalidArgument, "invalid argument")
	}
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			return fail(ErrInvalidArgument, "empty identifier")
		}
	}
	if s.hasClock && now < s.now {
		return fail(ErrClockRewound, "clock moved backwards")
	}
	return nil
}

func (s *Service) record(op string, in, result interface{}, why string) error {
	if err, ok := result.(error); ok && err != nil {
		s.log = append(s.log, fmt.Sprintf("%s in=%v OUT=ERROR %v WHY=%s", op, in, err, why))
		return err
	}
	s.log = append(s.log, fmt.Sprintf("%s in=%v OUT=OK WHY=%s", op, in, why))
	return nil
}

func (s *Service) advance(now int) {
	s.office.finishDue(now)
	s.elevator.advance(now, func(r *Reservation) {
		s.deposit.account(r.House).penalty(now, r.House, s.cfg.Penalty)
	})
}

func (s *Service) Snapshot(now int) State {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.advance(now)
	if now >= s.now {
		s.now = now
	}
	rs := s.elevator.snapshot()
	sort.Slice(rs, func(i, j int) bool { return rs[i].ID < rs[j].ID })
	ps := make([]Permit, 0, len(s.office.records))
	for _, p := range s.office.records {
		ps = append(ps, *p)
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].ID < ps[j].ID })
	ledger := []LedgerEntry{}
	for house := range s.deposit.accounts {
		ledger = append(ledger, s.deposit.accounts[house].ledger...)
	}
	sort.Slice(ledger, func(i, j int) bool {
		if ledger[i].Now != ledger[j].Now {
			return ledger[i].Now < ledger[j].Now
		}
		return ledger[i].House < ledger[j].House
	})
	return State{Now: s.now, Reservations: rs, Permits: ps, Ledger: ledger, Log: append([]string(nil), s.log...)}
}

func (s *Service) Deposit(req DepositRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	valid := req.Amount >= 0
	if err := s.validateClock(req.Now, valid, req.House); err != nil {
		return s.record("deposit", req, err, "validity before clock, then non-negative amount")
	}
	s.deposit.account(req.House).credit(req.Now, req.House, req.Amount, "deposit")
	s.now = req.Now
	s.hasClock = true
	return s.record("deposit", req, nil, "ledger entry appended")
}

func (s *Service) Reserve(req ReservationRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	valid := req.Start < req.End
	if err := s.validateClock(req.Now, valid, req.ID, req.House, req.Elevator); err != nil {
		return s.record("reserve", req, err, "validity and monotonic clock")
	}
	if _, exists := s.elevator.records[req.ID]; exists {
		err := fail(ErrInvalidArgument, "duplicate reservation id")
		return s.record("reserve", req, err, "id must be unique")
	}
	lead := req.Start - req.Now
	if lead < s.cfg.EarlyMin || lead > s.cfg.LateMin {
		err := fail(ErrTimeWindow, "reservation lead time out of range")
		return s.record("reserve", req, err, "lead must be within [L,U], both ends inclusive")
	}
	if code := s.elevator.canReserve(Reservation{
		ID: req.ID, House: req.House, Elevator: req.Elevator,
		Start: req.Start, End: req.End,
	}, req.Now); code != OK {
		err := fail(code, "reservation slot rejected")
		return s.record("reserve", req, err, "occupied minute or same household/day booking")
	}
	if s.deposit.account(req.House).balanceValue() < s.cfg.Penalty {
		err := fail(ErrInsufficientDeposit, "deposit cannot cover one penalty")
		return s.record("reserve", req, err, "balance is checked before state changes")
	}
	s.advance(req.Now)
	s.elevator.reserve(Reservation{
		ID: req.ID, House: req.House, Elevator: req.Elevator,
		Start: req.Start, End: req.End, Status: ReservationReserved,
	})
	s.now = req.Now
	s.hasClock = true
	return s.record("reserve", req, nil, "O(1) active slot lookup and O(length) requested range")
}

func (s *Service) Cancel(req ReservationAction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validateClock(req.Now, true, req.ID); err != nil {
		return s.record("cancel", req, err, "validity and monotonic clock")
	}
	r, ok := s.elevator.get(req.ID)
	if !ok {
		err := fail(ErrNotFound, "reservation not found")
		return s.record("cancel", req, err, "identifier lookup")
	}
	if s.elevator.active[req.ID] != r || r.Status != ReservationReserved {
		err := fail(ErrIllegalState, "reservation cannot be cancelled")
		return s.record("cancel", req, err, "only reserved active booking can be cancelled")
	}
	if req.Now > r.Start {
		err := fail(ErrTimeWindow, "cancellation after start")
		return s.record("cancel", req, err, "cancellation deadline already passed")
	}
	charged := r.Start-req.Now < s.cfg.EarlyMin
	s.advance(req.Now)
	if charged {
		s.deposit.account(r.House).penalty(req.Now, r.House, s.cfg.Penalty)
	}
	s.elevator.cancel(req.ID)
	s.now = req.Now
	s.hasClock = true
	return s.record("cancel", req, nil, fmt.Sprintf("exact-L cancellation charges penalty=%v", charged))
}

func (s *Service) CheckInElevator(req ReservationAction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validateClock(req.Now, true, req.ID); err != nil {
		return s.record("elevator-check-in", req, err, "validity and monotonic clock")
	}
	r, ok := s.elevator.get(req.ID)
	if !ok {
		err := fail(ErrNotFound, "reservation not found")
		return s.record("elevator-check-in", req, err, "identifier lookup")
	}
	if s.elevator.active[req.ID] != r || r.Status != ReservationReserved {
		err := fail(ErrIllegalState, "reservation cannot check in")
		return s.record("elevator-check-in", req, err, "cancelled, expired, no-show, or already used")
	}
	if req.Now < r.Start-s.cfg.CheckInMin || req.Now > r.End {
		err := fail(ErrTimeWindow, "elevator check-in window")
		return s.record("elevator-check-in", req, err, "window is [start-E,end] inclusive")
	}
	if code := s.elevator.canCheckIn(req.ID, req.Now, s.cfg.CheckInMin); code != OK {
		err := fail(code, "elevator check-in state rejected")
		return s.record("elevator-check-in", req, err, "window or previous household hold")
	}
	s.advance(req.Now)
	if code := s.elevator.checkIn(req.ID); code != OK {
		err := fail(code, "elevator check-in state rejected")
		return s.record("elevator-check-in", req, err, "previous household has not checked out")
	}
	s.now = req.Now
	s.hasClock = true
	return s.record("elevator-check-in", req, nil, "slot is in use")
}

func (s *Service) CheckOutElevator(req ReservationAction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validateClock(req.Now, true, req.ID); err != nil {
		return s.record("elevator-check-out", req, err, "validity and monotonic clock")
	}
	if _, ok := s.elevator.get(req.ID); !ok {
		err := fail(ErrNotFound, "reservation not found")
		return s.record("elevator-check-out", req, err, "identifier lookup")
	}
	s.advance(req.Now)
	if code := s.elevator.checkOut(req.ID); code != OK {
		err := fail(code, "elevator check-out rejected")
		return s.record("elevator-check-out", req, err, "only checked-in reservation can sign out")
	}
	s.now = req.Now
	s.hasClock = true
	return s.record("elevator-check-out", req, nil, "overstay hold released")
}

func (s *Service) ApplyPermit(req PermitRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	valid := req.StartDay < req.EndDay && req.EndDay-req.StartDay <= s.cfg.MaxDays
	if err := s.validateClock(req.Now, valid, req.ID, req.House); err != nil {
		return s.record("permit-apply", req, err, "interval must be positive and within M days")
	}
	if _, exists := s.office.records[req.ID]; exists {
		err := fail(ErrInvalidArgument, "duplicate permit id")
		return s.record("permit-apply", req, err, "id must be unique")
	}
	if floorDay(req.Now) >= req.StartDay {
		err := fail(ErrTimeWindow, "permit must be applied before first construction day")
		return s.record("permit-apply", req, err, "application date must precede start day")
	}
	if revokedDay, ok := s.office.lastRevokeDay[req.House]; ok && req.StartDay < revokedDay+s.cfg.WaitDays {
		err := fail(ErrTimeWindow, "revocation waiting period")
		return s.record("permit-apply", req, err, "new start must be at least W days after revocation day")
	}
	if p := s.office.active[req.House]; p != nil {
		err := fail(ErrIllegalState, "household already has an effective permit")
		return s.record("permit-apply", req, err, "at most one effective permit at a time")
	}
	if s.deposit.account(req.House).balanceValue() < s.cfg.Penalty {
		err := fail(ErrInsufficientDeposit, "deposit cannot cover one penalty")
		return s.record("permit-apply", req, err, "shared deposit balance insufficient")
	}
	s.office.finishDue(req.Now)
	code := s.office.apply(Permit{
		ID: req.ID, House: req.House, StartDay: req.StartDay,
		EndDay: req.EndDay, Noisy: req.Noisy,
	}, s.cfg.WaitDays)
	if code != OK {
		err := fail(code, "permit application rejected")
		return s.record("permit-apply", req, err, "effective permit or W-day wait")
	}
	s.now = req.Now
	s.hasClock = true
	return s.record("permit-apply", req, nil, "pending permit recorded")
}

func (s *Service) ApprovePermit(req PermitAction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validateClock(req.Now, true, req.ID); err != nil {
		return s.record("permit-approve", req, err, "validity and monotonic clock")
	}
	if _, ok := s.office.get(req.ID); !ok {
		err := fail(ErrNotFound, "permit not found")
		return s.record("permit-approve", req, err, "identifier lookup")
	}
	p := s.office.records[req.ID]
	if s.office.active[p.House] != p || p.Status != PermitPending {
		err := fail(ErrIllegalState, "permit cannot be approved")
		return s.record("permit-approve", req, err, "only pending effective permit can be approved")
	}
	s.office.finishDue(req.Now)
	if code := s.office.approve(req.ID); code != OK {
		err := fail(code, "permit approval rejected")
		return s.record("permit-approve", req, err, "state changed under effective permit")
	}
	s.now = req.Now
	s.hasClock = true
	return s.record("permit-approve", req, nil, "approved; quiet and dates are checked at check-in")
}

func (s *Service) CheckInPermit(req PermitAction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validateClock(req.Now, true, req.ID); err != nil {
		return s.record("permit-check-in", req, err, "validity and monotonic clock")
	}
	p, ok := s.office.get(req.ID)
	if !ok {
		err := fail(ErrNotFound, "permit not found")
		return s.record("permit-check-in", req, err, "identifier lookup")
	}
	day := floorDay(req.Now)
	if day < p.StartDay || day >= p.EndDay {
		err := fail(ErrTimeWindow, "construction date outside permit")
		return s.record("permit-check-in", req, err, "start day included, end day excluded")
	}
	if s.office.active[p.House] != p {
		err := fail(ErrIllegalState, "permit not effective")
		return s.record("permit-check-in", req, err, "revoked or finished permit")
	}
	if p.Suspended || p.Status == PermitSuspended {
		err := fail(ErrIllegalState, "construction suspended")
		return s.record("permit-check-in", req, err, "suspension must be lifted first")
	}
	if p.Status != PermitApproved && p.Status != PermitActive {
		err := fail(ErrIllegalState, "permit not approved")
		return s.record("permit-check-in", req, err, "pending or terminal permit")
	}
	if p.Noisy && s.office.isQuiet(req.Now) {
		err := fail(ErrQuietConflict, "noisy construction in quiet time")
		return s.record("permit-check-in", req, err, "daily cross-midnight ranges or all-day holiday")
	}
	s.office.checkIn(req.ID, req.Now)
	s.now = req.Now
	s.hasClock = true
	return s.record("permit-check-in", req, nil, "noise quiet rule evaluated at check-in only")
}

func (s *Service) CheckOutPermit(req PermitAction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validateClock(req.Now, true, req.ID); err != nil {
		return s.record("permit-check-out", req, err, "validity and monotonic clock")
	}
	if _, ok := s.office.get(req.ID); !ok {
		err := fail(ErrNotFound, "permit not found")
		return s.record("permit-check-out", req, err, "identifier lookup")
	}
	if code := s.office.checkOut(req.ID); code != OK {
		err := fail(code, "permit check-out rejected")
		return s.record("permit-check-out", req, err, "only checked-in permit can sign out")
	}
	s.now = req.Now
	s.hasClock = true
	return s.record("permit-check-out", req, nil, "construction sign-out recorded")
}

func (s *Service) ExtendPermit(req PermitExtend) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validateClock(req.Now, req.NewEndDay > 0, req.ID); err != nil {
		return s.record("permit-extend", req, err, "validity and monotonic clock")
	}
	p, ok := s.office.get(req.ID)
	if !ok {
		err := fail(ErrNotFound, "permit not found")
		return s.record("permit-extend", req, err, "identifier lookup")
	}
	if s.office.active[p.House] != p {
		err := fail(ErrIllegalState, "permit not effective")
		return s.record("permit-extend", req, err, "terminal permit cannot extend")
	}
	if p.Extended || floorDay(req.Now) >= p.EndDay {
		err := fail(ErrTimeWindow, "extension unavailable")
		return s.record("permit-extend", req, err, "one extension and only before expiry")
	}
	if req.NewEndDay <= p.EndDay || req.NewEndDay-p.StartDay > s.cfg.MaxDays {
		err := fail(ErrTimeWindow, "extension interval invalid")
		return s.record("permit-extend", req, err, "extension must extend and remain within M total days")
	}
	s.office.extend(req.ID, req.Now, req.NewEndDay, s.cfg.MaxDays)
	s.now = req.Now
	s.hasClock = true
	return s.record("permit-extend", req, nil, "single extension committed")
}

func (s *Service) Complain(req HouseAction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validateClock(req.Now, true, req.House); err != nil {
		return s.record("complain", req, err, "validity and monotonic clock")
	}
	p := s.office.active[req.House]
	if p == nil {
		err := fail(ErrNotFound, "active permit not found")
		return s.record("complain", req, err, "only household with effective permit can be complained about")
	}
	next := p.Complaints + 1
	s.office.complain(req.House, req.Now, s.cfg.RevokeAt)
	s.now = req.Now
	s.hasClock = true
	return s.record("complain", req, nil, fmt.Sprintf("count=%d; revoked=%v", next, next >= s.cfg.RevokeAt))
}

func (s *Service) LiftSuspension(req HouseAction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validateClock(req.Now, true, req.House); err != nil {
		return s.record("lift-suspension", req, err, "validity and monotonic clock")
	}
	if _, ok := s.office.active[req.House]; !ok {
		err := fail(ErrNotFound, "active permit not found")
		return s.record("lift-suspension", req, err, "revoked permit cannot be lifted")
	}
	if code := s.office.liftSuspension(req.House); code != OK {
		err := fail(code, "lift suspension rejected")
		return s.record("lift-suspension", req, err, "only suspended permit can be lifted")
	}
	s.now = req.Now
	s.hasClock = true
	return s.record("lift-suspension", req, nil, "suspension removed; complaint count retained")
}

func (s *Service) Refund(req HouseAction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validateClock(req.Now, true, req.House); err != nil {
		return s.record("refund", req, err, "validity and monotonic clock")
	}
	permit, eligible := s.office.refundEligible(req.House, req.Now, s.cfg.RefundDays)
	if permit == nil {
		err := fail(ErrNotFound, "finished permit not found")
		return s.record("refund", req, err, "requires a closed permit")
	}
	if !eligible {
		err := fail(ErrTimeWindow, "refund waiting period not elapsed")
		return s.record("refund", req, err, "latest complaint restarts the R-day wait")
	}
	amount := s.deposit.account(req.House).refund(req.Now, req.House)
	s.now = req.Now
	s.hasClock = true
	return s.record("refund", req, nil, fmt.Sprintf("refund amount=%d", amount))
}

func (s *Service) SetQuietRanges(req QuietRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.office.setQuietRanges(req.Ranges)
	if req.Now > s.now || !s.hasClock {
		s.now = req.Now
		s.hasClock = true
	}
	s.log = append(s.log, fmt.Sprintf("set-quiet in=%v OUT=OK WHY=future check-ins use new ranges", req))
}

func (s *Service) AddHoliday(req HolidayRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.Day <= floorDay(req.Now) {
		err := fail(ErrTimeWindow, "holiday must be strictly later than now")
		return s.record("add-holiday", req, err, "only future whole days can be added")
	}
	s.office.addHoliday(req.Day)
	s.now = req.Now
	s.hasClock = true
	return s.record("add-holiday", req, nil, "whole future day is quiet")
}

func (s *Service) Balance(house string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deposit.account(house).balanceValue()
}

func (s *Service) SlotFree(elevator string, minute int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.elevator.slotFree(elevator, minute)
}

func (s *Service) Log() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.log...)
}
