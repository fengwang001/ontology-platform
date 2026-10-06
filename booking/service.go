package booking

import (
	"math"
	"strconv"
	"sync"
)

type System struct {
	mu       sync.Mutex
	cfg      Config
	flights  map[string]*Flight
	tickets  map[string]*Ticket
	vouchers map[string]*Voucher
	nextID   int64
	lastAt   int64
}

func NewSystem(cfg Config) (*System, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	return &System{cfg: cfg, flights: map[string]*Flight{}, tickets: map[string]*Ticket{}, vouchers: map[string]*Voucher{}}, nil
}

func (s *System) AddFlight(flight Flight) error {
	if flight.ID == "" || flight.Price < 0 || flight.DepartAt < 0 || flight.Price > math.MaxInt64/100 {
		return errorf(ErrInvalidArgument, "invalid flight")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.flights[flight.ID]; exists {
		return errorf(ErrInvalidArgument, "duplicate flight id")
	}
	copyFlight := flight
	copyFlight.Canceled = false
	s.flights[flight.ID] = &copyFlight
	return nil
}

func (s *System) IssueTicket(id, owner, flightID string, now int64) error {
	if id == "" || owner == "" || flightID == "" || now < 0 {
		return errorf(ErrInvalidArgument, "invalid ticket issue request")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.lastAt {
		return errorf(ErrClockMovedBack, "now %d is before %d", now, s.lastAt)
	}
	flight, ok := s.flights[flightID]
	if !ok || flight.Canceled {
		return errorf(ErrInvalidArgument, "flight is unavailable")
	}
	if _, exists := s.tickets[id]; exists {
		return errorf(ErrInvalidArgument, "duplicate ticket id")
	}
	s.tickets[id] = &Ticket{ID: id, Owner: owner, FlightID: flightID, Price: flight.Price, DepartAt: flight.DepartAt, LastEventAt: now}
	s.lastAt = now
	return nil
}

func (s *System) Refund(id string, now int64) (RefundResult, error) {
	if id == "" || now < 0 {
		return RefundResult{}, errorf(ErrInvalidArgument, "invalid refund request")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.lastAt {
		return RefundResult{}, errorf(ErrClockMovedBack, "now %d is before %d", now, s.lastAt)
	}
	result, err := s.refundQuoteLocked(id, now)
	if err != nil {
		return RefundResult{}, err
	}
	ticket := s.tickets[id]
	ticket.Refunded = true
	ticket.LastEventAt = now
	s.lastAt = now
	return result, nil
}

func (s *System) Change(id, targetFlightID string, voucherID *string, cash, now int64) (ChangeResult, error) {
	if id == "" || targetFlightID == "" || cash < 0 || now < 0 || voucherID != nil && *voucherID == "" {
		return ChangeResult{}, errorf(ErrInvalidArgument, "invalid change request")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.lastAt {
		return ChangeResult{}, errorf(ErrClockMovedBack, "now %d is before %d", now, s.lastAt)
	}
	result, err := s.changeQuoteLocked(id, targetFlightID, voucherID, now)
	if err != nil {
		return ChangeResult{}, err
	}
	ticket := s.tickets[id]
	target := s.flights[targetFlightID]
	var selectedVoucher *Voucher
	if voucherID != nil && result.VoucherDeduction > 0 {
		selectedVoucher = s.vouchers[*voucherID]
	}
	if cash != result.CashDue {
		return ChangeResult{}, Error{Kind: ErrCashMismatch, Msg: "cash must exactly match cash due", ExpectedCash: result.CashDue}
	}

	if selectedVoucher != nil && result.VoucherDeduction > 0 {
		selectedVoucher.apply(result.VoucherDeduction)
	}
	if result.GeneratedVoucher > 0 {
		result.GeneratedVoucherID = s.newVoucherID()
		s.vouchers[result.GeneratedVoucherID] = &Voucher{ID: result.GeneratedVoucherID, Owner: ticket.Owner, Amount: result.GeneratedVoucher, ExpiresAt: now + s.cfg.VoucherTTL}
	}
	ticket.FlightID = targetFlightID
	ticket.Price = target.Price
	ticket.DepartAt = target.DepartAt
	ticket.Involuntary = false
	if !result.Involuntary {
		ticket.Changes++
		ticket.PaidChangeFee += result.ChangeFee
	}
	result.Changes = ticket.Changes
	ticket.LastEventAt = now
	s.lastAt = now
	return result, nil
}

func (s *System) CancelFlight(flightID string, now int64) error {
	if flightID == "" || now < 0 {
		return errorf(ErrInvalidArgument, "invalid cancellation request")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.lastAt {
		return errorf(ErrClockMovedBack, "now %d is before %d", now, s.lastAt)
	}
	flight, ok := s.flights[flightID]
	if !ok {
		return errorf(ErrInvalidArgument, "flight %s does not exist", flightID)
	}
	if flight.Canceled {
		return errorf(ErrInvalidArgument, "flight %s already canceled", flightID)
	}
	flight.Canceled = true
	for _, ticket := range s.tickets {
		if ticket.alive() && ticket.FlightID == flightID {
			ticket.Involuntary = true
			ticket.LastEventAt = now
		}
	}
	s.lastAt = now
	return nil
}

func (s *System) QuoteRefund(id string, now int64) (RefundResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refundQuoteLocked(id, now)
}

func (s *System) QuoteChange(id, targetFlightID string, voucherID *string, now int64) (ChangeResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.changeQuoteLocked(id, targetFlightID, voucherID, now)
}

func (s *System) Ticket(id string) (Ticket, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ticket, ok := s.tickets[id]
	if !ok {
		return Ticket{}, false
	}
	return *ticket, true
}

func (s *System) Voucher(id string) (Voucher, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	voucher, ok := s.vouchers[id]
	if !ok {
		return Voucher{}, false
	}
	return *voucher, true
}

func (s *System) newVoucherID() string {
	s.nextID++
	return "V" + strconv.FormatInt(s.nextID, 10)
}
