package booking

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
)

func testSystem(t *testing.T) *System {
	t.Helper()
	s, err := NewSystem(Config{
		LongThreshold:  200,
		ShortThreshold: 100,
		RefundRates:    Rates{Long: 5, Mid: 10, Short: 20},
		ChangeRates:    Rates{Long: 3, Mid: 7, Short: 15},
		ChangeLimit:    2,
		VoucherTTL:     50,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func addFlight(t *testing.T, s *System, id string, price, depart int64) {
	t.Helper()
	if err := s.AddFlight(Flight{ID: id, Price: price, DepartAt: depart}); err != nil {
		t.Fatal(err)
	}
}

func issue(t *testing.T, s *System, ticketID, owner, flightID string, now int64) {
	t.Helper()
	if err := s.IssueTicket(ticketID, owner, flightID, now); err != nil {
		t.Fatal(err)
	}
}

func ptr[T any](value T) *T { return &value }

func TestTierBoundariesAndCeilFee(t *testing.T) {
	tests := []struct {
		name     string
		now      int64
		wantTier string
		wantFee  int64
	}{
		{"long exactly", 800, "long", 50},
		{"one second inside middle", 801, "middle", 100},
		{"short exactly", 900, "middle", 100},
		{"one second inside short", 901, "short", 200},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := testSystem(t)
			addFlight(t, s, "F", 1000, 1000)
			issue(t, s, "T", "alice", "F", 0)
			quote, err := s.QuoteRefund("T", tc.now)
			if err != nil {
				t.Fatal(err)
			}
			if quote.Tier != wantTier(tc.wantTier) || quote.Fee != tc.wantFee {
				t.Fatalf("got tier=%s fee=%d, want %s fee=%d", quote.Tier, quote.Fee, tc.wantTier, tc.wantFee)
			}
		})
	}
}

func wantTier(value string) string { return value }

func TestPositiveZeroNegativeDifferenceCashAndVoucherFlows(t *testing.T) {
	s, err := NewSystem(Config{200, 100, Rates{5, 10, 20}, Rates{3, 7, 15}, 4, 50})
	if err != nil {
		t.Fatal(err)
	}
	addFlight(t, s, "F", 1000, 1000)
	addFlight(t, s, "P", 1300, 1500)
	addFlight(t, s, "Z", 1300, 1600)
	addFlight(t, s, "N", 700, 1700)
	addFlight(t, s, "BACK", 1000, 1800)
	issue(t, s, "T", "alice", "F", 0)

	positive, err := s.Change("T", "P", nil, 330, 100)
	if err != nil {
		t.Fatal(err)
	}
	if positive.ChangeFee != 30 || positive.PriceDifference != 300 || positive.CashDue != 330 {
		t.Fatalf("unexpected positive change: %+v", positive)
	}

	zero, err := s.Change("T", "Z", nil, 39, 1300)
	if err != nil || zero.PriceDifference != 0 || zero.CashDue != 39 || zero.GeneratedVoucher != 0 {
		t.Fatalf("unexpected zero change: %+v err=%v", zero, err)
	}

	negative, err := s.Change("T", "N", nil, 39, 1400)
	if err != nil {
		t.Fatal(err)
	}
	if negative.PriceDifference != -600 || negative.CashDue != 39 || negative.GeneratedVoucher != 600 || negative.GeneratedVoucherID == "" {
		t.Fatalf("unexpected negative change: %+v", negative)
	}
	voucher, ok := s.Voucher(negative.GeneratedVoucherID)
	if !ok || voucher.Amount != 600 || voucher.Owner != "alice" || voucher.ExpiresAt != 1450 {
		t.Fatalf("unexpected generated voucher: %+v ok=%v", voucher, ok)
	}

	addFlight(t, s, "EXP", 1300, 1700)
	back, err := s.Change("T", "BACK", ptr(negative.GeneratedVoucherID), 0, 1449)
	if err != nil || back.VoucherDeduction != 321 || back.CashDue != 0 || back.PriceDifference != 300 {
		t.Fatalf("voucher did not cover fee and positive difference: %+v err=%v", back, err)
	}
	if used, _ := s.Voucher(negative.GeneratedVoucherID); used.Used || used.Amount != 279 {
		t.Fatalf("voucher should be exhausted: %+v", used)
	}
}

func TestVoucherAmountRelations(t *testing.T) {
	cases := []struct {
		name       string
		amount     int64
		wantDeduct int64
		wantCash   int64
		wantRemain int64
		wantUsed   bool
	}{
		{"greater", 400, 330, 0, 70, false},
		{"equal", 330, 330, 0, 0, true},
		{"less", 200, 200, 130, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := testSystem(t)
			addFlight(t, s, "F", 1000, 1000)
			addFlight(t, s, "P", 1300, 1200)
			issue(t, s, "T", "alice", "F", 0)
			s.vouchers["C"] = &Voucher{ID: "C", Owner: "alice", Amount: tc.amount, ExpiresAt: 500}
			result, err := s.Change("T", "P", ptr("C"), tc.wantCash, 100)
			if err != nil {
				t.Fatal(err)
			}
			if result.VoucherDeduction != tc.wantDeduct || result.CashDue != tc.wantCash {
				t.Fatalf("unexpected result: %+v", result)
			}
			voucher, _ := s.Voucher("C")
			if voucher.Amount != tc.wantRemain || voucher.Used != tc.wantUsed {
				t.Fatalf("unexpected voucher: %+v", voucher)
			}
		})
	}
}

func TestInvoluntaryRefundAndChange(t *testing.T) {
	s := testSystem(t)
	addFlight(t, s, "F", 1000, 1000)
	addFlight(t, s, "P", 1300, 1200)
	addFlight(t, s, "LOW", 700, 1300)
	addFlight(t, s, "LATE", 1200, 1600)
	issue(t, s, "T", "alice", "F", 0)
	var err error
	_, err = s.Change("T", "P", nil, 330, 100)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.CancelFlight("P", 500); err != nil {
		t.Fatal(err)
	}
	ticket, _ := s.Ticket("T")
	if !ticket.Involuntary {
		t.Fatal("ticket should be involuntary")
	}

	changed, err := s.Change("T", "LOW", nil, 0, 1001)
	if err != nil || changed.ChangeFee != 0 || changed.PriceDifference != -600 || changed.GeneratedVoucher != 0 || changed.CashDue != 0 {
		t.Fatalf("involuntary negative change should give nothing: %+v err=%v", changed, err)
	}
	ticket, _ = s.Ticket("T")
	if ticket.Involuntary || ticket.Changes != 1 {
		t.Fatalf("involuntary change should clear flag and not count: %+v", ticket)
	}

	voluntary, err := s.Change("T", "LATE", nil, 521, 1100)
	if err != nil || voluntary.ChangeFee != 21 || voluntary.PriceDifference != 500 {
		t.Fatalf("later voluntary change should be charged by current original flight: %+v err=%v", voluntary, err)
	}
	ticket, _ = s.Ticket("T")
	if ticket.Changes != 2 {
		t.Fatalf("later voluntary change must count, got %d", ticket.Changes)
	}

	s2 := testSystem(t)
	addFlight(t, s2, "F", 1000, 1000)
	addFlight(t, s2, "P", 1300, 1200)
	issue(t, s2, "T", "alice", "F", 0)
	if _, err := s2.Change("T", "P", nil, 330, 300); err != nil {
		t.Fatal(err)
	}
	if err := s2.CancelFlight("P", 500); err != nil {
		t.Fatal(err)
	}
	refund, err := s2.Refund("T", 1001)
	if err != nil || refund.CashRefund != 1330 || refund.Refund != 1300 || refund.PaidChangeFee != 30 {
		t.Fatalf("involuntary refund mismatch: %+v err=%v", refund, err)
	}
}

func errorKind(err error) ErrorKind {
	var bookingError Error
	if errors.As(err, &bookingError) {
		return bookingError.Kind
	}
	return ""
}

func TestRejectionOrderAdjacentCategories(t *testing.T) {
	t.Run("invalid before clock", func(t *testing.T) {
		s := testSystem(t)
		addFlight(t, s, "F", 1000, 1000)
		issue(t, s, "T", "alice", "F", 10)
		_, err := s.Refund("", 0)
		if errorKind(err) != ErrInvalidArgument {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("clock before ticket", func(t *testing.T) {
		s := testSystem(t)
		addFlight(t, s, "F", 1000, 1000)
		issue(t, s, "T", "alice", "F", 10)
		_, err := s.Refund("MISSING", 9)
		if errorKind(err) != ErrClockMovedBack {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("missing before refunded", func(t *testing.T) {
		s := testSystem(t)
		addFlight(t, s, "F", 1000, 1000)
		issue(t, s, "T", "alice", "F", 0)
		_, err := s.Refund("MISSING", 0)
		if errorKind(err) != ErrTicketNotFound {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("refunded before departed", func(t *testing.T) {
		s := testSystem(t)
		addFlight(t, s, "F", 1000, 1000)
		issue(t, s, "T", "alice", "F", 0)
		if _, err := s.Refund("T", 100); err != nil {
			t.Fatal(err)
		}
		_, err := s.Refund("T", 2000)
		if errorKind(err) != ErrTicketRefunded {
			t.Fatalf("got %v", err)
		}
	})

	setupChangeOrder := func(t *testing.T, voucher *Voucher, cash, now int64, cancel bool) (*System, *string) {
		t.Helper()
		s := testSystem(t)
		addFlight(t, s, "F", 1000, 1000)
		addFlight(t, s, "P", 1300, 2000)
		issue(t, s, "T", "alice", "F", 0)
		var voucherID *string
		if voucher != nil {
			s.vouchers[voucher.ID] = voucher
			id := voucher.ID
			voucherID = &id
		}
		if cancel {
			if err := s.CancelFlight("P", now); err != nil {
				t.Fatal(err)
			}
		}
		return s, voucherID
	}
	change := func(s *System, id string, voucherID *string, cash, now int64) error {
		_, err := s.Change(id, "P", voucherID, cash, now)
		return err
	}

	t.Run("departed before limit", func(t *testing.T) {
		s, voucherID := setupChangeOrder(t, nil, 0, 1000, false)
		if err := change(s, "T", voucherID, 0, 1000); errorKind(err) != ErrDeparted {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("limit before voucher missing", func(t *testing.T) {
		s, _ := setupChangeOrder(t, nil, 330, 100, false)
		for index := 0; index < 2; index++ {
			flightID := "L" + string(rune('A'+index))
			addFlight(t, s, flightID, int64(1300+index), int64(3000+index))
			cash := int64(330)
			if index == 1 {
				cash = 40
			}
			if _, err := s.Change("T", flightID, nil, cash, 100); err != nil {
				t.Fatal(err)
			}
		}
		if err := change(s, "T", ptr("MISSING"), 0, 200); errorKind(err) != ErrChangeLimit {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("missing voucher before owner", func(t *testing.T) {
		s, voucherID := setupChangeOrder(t, nil, 330, 100, false)
		voucherID = ptr("MISSING")
		if err := change(s, "T", voucherID, 330, 100); errorKind(err) != ErrVoucherNotFound {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("owner before expired", func(t *testing.T) {
		s, voucherID := setupChangeOrder(t, &Voucher{ID: "V", Owner: "bob", Amount: 100, ExpiresAt: 1}, 330, 100, false)
		if err := change(s, "T", voucherID, 330, 100); errorKind(err) != ErrVoucherOwner {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("expired before spent", func(t *testing.T) {
		s, voucherID := setupChangeOrder(t, &Voucher{ID: "V", Owner: "alice", Amount: 0, ExpiresAt: 100}, 330, 100, false)
		if err := change(s, "T", voucherID, 330, 100); errorKind(err) != ErrVoucherExpired {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("spent before cash", func(t *testing.T) {
		s, voucherID := setupChangeOrder(t, &Voucher{ID: "V", Owner: "alice", Amount: 0, ExpiresAt: 500}, 330, 100, false)
		if err := change(s, "T", voucherID, 330, 100); errorKind(err) != ErrVoucherSpent {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("voucher before cash", func(t *testing.T) {
		s, voucherID := setupChangeOrder(t, &Voucher{ID: "V", Owner: "alice", Amount: 100, ExpiresAt: 500}, 999, 100, false)
		err := change(s, "T", voucherID, 999, 100)
		if errorKind(err) != ErrCashMismatch {
			t.Fatalf("got %v", err)
		}
		var bookingError Error
		errors.As(err, &bookingError)
		if bookingError.ExpectedCash != 230 {
			t.Fatalf("expected cash 230, got %d", bookingError.ExpectedCash)
		}
	})
}

func TestRejectedOperationDoesNotMutateClockOrState(t *testing.T) {
	s := testSystem(t)
	addFlight(t, s, "F", 1000, 1000)
	issue(t, s, "T", "alice", "F", 10)
	_, err := s.Refund("T", 2000)
	if errorKind(err) != ErrDeparted {
		t.Fatal(err)
	}
	ticket, _ := s.Ticket("T")
	if ticket.Refunded || ticket.LastEventAt != 10 {
		t.Fatalf("rejected operation mutated ticket: %+v", ticket)
	}
	if _, err := s.Refund("T", 10); err != nil {
		t.Fatalf("clock should remain at 10 after rejection: %v", err)
	}
}

func TestConcurrentOperationsSerializeAndReplayDeterministically(t *testing.T) {
	s := testSystem(t)
	addFlight(t, s, "F", 1000, 1000)
	addFlight(t, s, "A", 1200, 1500)
	const goroutines = 32
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	start := make(chan struct{})
	wg.Add(goroutines)
	for index := 0; index < goroutines; index++ {
		id := "T" + string(rune('A'+index))
		issue(t, s, id, "alice", "F", 0)
		go func() {
			defer wg.Done()
			<-start
			_, err := s.Change(id, "A", nil, 230, 100)
			if err != nil {
				errs <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	first, err := buildReplaySystem(t).Change("T", "A", nil, 230, 100)
	if err != nil {
		t.Fatal(err)
	}
	second, err := buildReplaySystem(t).Change("T", "A", nil, 230, 100)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("replay mismatch: %+v != %+v", first, second)
	}
}

func buildReplaySystem(t *testing.T) *System {
	t.Helper()
	s := testSystem(t)
	addFlight(t, s, "F", 1000, 1000)
	addFlight(t, s, "A", 1200, 1500)
	issue(t, s, "T", "alice", "F", 0)
	return s
}

type naiveFlight struct {
	price    int64
	depart   int64
	canceled bool
}

type naiveTicket struct {
	owner       string
	flight      string
	price       int64
	depart      int64
	changes     int
	paidFees    int64
	refunded    bool
	involuntary bool
}

type naiveVoucher struct {
	owner     string
	amount    int64
	expiresAt int64
}

type naiveModel struct {
	cfg      Config
	flights  map[string]naiveFlight
	tickets  map[string]naiveTicket
	vouchers map[string]naiveVoucher
	next     int
	last     int64
}

type naiveOutcome struct {
	ok        bool
	kind      ErrorKind
	cashDue   int64
	cashBack  int64
	fee       int64
	diff      int64
	applied   string
	voucherID string
	voucher   int64
	deduction int64
	tier      string
}

func newNaiveModel(cfg Config) *naiveModel {
	return &naiveModel{cfg: cfg, flights: map[string]naiveFlight{}, tickets: map[string]naiveTicket{}, vouchers: map[string]naiveVoucher{}}
}

func naiveCeil(price int64, rate int) int64 {
	return (price*int64(rate) + 99) / 100
}

func naiveTierName(delta, long, short int64) string {
	if delta >= long {
		return "long"
	}
	if delta >= short {
		return "middle"
	}
	return "short"
}

func naiveRate(rates Rates, name string) int {
	switch name {
	case "long":
		return rates.Long
	case "middle":
		return rates.Mid
	default:
		return rates.Short
	}
}

func (m *naiveModel) fail(kind ErrorKind, cashDue int64) naiveOutcome {
	return naiveOutcome{kind: kind, cashDue: cashDue}
}

func (m *naiveModel) addFlight(id string, price, depart int64) {
	m.flights[id] = naiveFlight{price: price, depart: depart}
}

func (m *naiveModel) issue(id, owner, flightID string, now int64) naiveOutcome {
	if id == "" || owner == "" || flightID == "" || now < 0 {
		return m.fail(ErrInvalidArgument, 0)
	}
	if now < m.last {
		return m.fail(ErrClockMovedBack, 0)
	}
	flight, ok := m.flights[flightID]
	if !ok || flight.canceled {
		return m.fail(ErrInvalidArgument, 0)
	}
	if _, exists := m.tickets[id]; exists {
		return m.fail(ErrInvalidArgument, 0)
	}
	m.tickets[id] = naiveTicket{owner: owner, flight: flightID, price: flight.price, depart: flight.depart}
	m.last = now
	return naiveOutcome{ok: true}
}

func (m *naiveModel) refund(id string, now int64) naiveOutcome {
	if id == "" || now < 0 {
		return m.fail(ErrInvalidArgument, 0)
	}
	if now < m.last {
		return m.fail(ErrClockMovedBack, 0)
	}
	ticket, ok := m.tickets[id]
	if !ok {
		return m.fail(ErrTicketNotFound, 0)
	}
	if ticket.refunded {
		return m.fail(ErrTicketRefunded, 0)
	}
	out := naiveOutcome{}
	if ticket.involuntary {
		out.ok = true
		out.cashBack = ticket.price + ticket.paidFees
		ticket.refunded = true
		m.tickets[id] = ticket
		m.last = now
		return out
	}
	if now >= ticket.depart {
		return m.fail(ErrDeparted, 0)
	}
	name := naiveTierName(ticket.depart-now, m.cfg.LongThreshold, m.cfg.ShortThreshold)
	rate := naiveRate(m.cfg.RefundRates, name)
	fee := naiveCeil(ticket.price, rate)
	out.ok = true
	out.tier = name
	out.fee = fee
	out.cashBack = ticket.price - fee
	ticket.refunded = true
	m.tickets[id] = ticket
	m.last = now
	return out
}

func (m *naiveModel) change(id, targetID, voucherID string, hasVoucher bool, cash, now int64) naiveOutcome {
	if id == "" || targetID == "" || cash < 0 || now < 0 || hasVoucher && voucherID == "" {
		return m.fail(ErrInvalidArgument, 0)
	}
	if now < m.last {
		return m.fail(ErrClockMovedBack, 0)
	}
	target, targetOK := m.flights[targetID]
	if !targetOK || target.canceled {
		return m.fail(ErrInvalidArgument, 0)
	}
	ticket, ok := m.tickets[id]
	if !ok {
		return m.fail(ErrTicketNotFound, 0)
	}
	if ticket.refunded {
		return m.fail(ErrTicketRefunded, 0)
	}
	if !ticket.involuntary && now >= ticket.depart {
		return m.fail(ErrDeparted, 0)
	}
	if !ticket.involuntary && ticket.changes >= m.cfg.ChangeLimit {
		return m.fail(ErrChangeLimit, 0)
	}
	if targetID == ticket.flight {
		return m.fail(ErrInvalidArgument, 0)
	}
	var voucher *naiveVoucher
	if hasVoucher {
		candidate, found := m.vouchers[voucherID]
		if !found {
			return m.fail(ErrVoucherNotFound, 0)
		}
		if candidate.owner != ticket.owner {
			return m.fail(ErrVoucherOwner, 0)
		}
		if now >= candidate.expiresAt {
			return m.fail(ErrVoucherExpired, 0)
		}
		if candidate.amount <= 0 {
			return m.fail(ErrVoucherSpent, 0)
		}
		voucher = &candidate
	}

	out := naiveOutcome{diff: target.price - ticket.price}
	if ticket.involuntary {
		out.ok = true
		if cash != 0 {
			return m.fail(ErrCashMismatch, 0)
		}
		ticket.flight = targetID
		ticket.price = target.price
		ticket.depart = target.depart
		ticket.involuntary = false
		m.tickets[id] = ticket
		m.last = now
		return out
	}
	name := naiveTierName(ticket.depart-now, m.cfg.LongThreshold, m.cfg.ShortThreshold)
	rate := naiveRate(m.cfg.ChangeRates, name)
	fee := naiveCeil(ticket.price, rate)
	positive := int64(0)
	if out.diff > 0 {
		positive = out.diff
	} else {
		out.voucher = -out.diff
	}
	gross := fee + positive
	deduction := int64(0)
	if voucher != nil {
		deduction = voucher.amount
		if deduction > gross {
			deduction = gross
		}
	}
	cashDue := gross - deduction
	if cash != cashDue {
		return m.fail(ErrCashMismatch, cashDue)
	}
	out.ok = true
	out.tier = name
	out.fee = fee
	out.deduction = deduction
	out.cashDue = cashDue
	if voucher != nil {
		if deduction > 0 {
			out.applied = voucherID
		}
		candidate := m.vouchers[voucherID]
		candidate.amount -= deduction
		m.vouchers[voucherID] = candidate
	}
	if out.voucher > 0 {
		m.next++
		generatedID := fmt.Sprintf("V%d", m.next)
		out.voucherID = generatedID
		m.vouchers[generatedID] = naiveVoucher{owner: ticket.owner, amount: out.voucher, expiresAt: now + m.cfg.VoucherTTL}
	}
	ticket.flight = targetID
	ticket.price = target.price
	ticket.depart = target.depart
	ticket.changes++
	ticket.paidFees += fee
	m.tickets[id] = ticket
	m.last = now
	return out
}

func (m *naiveModel) cancel(flightID string, now int64) naiveOutcome {
	if flightID == "" || now < 0 {
		return m.fail(ErrInvalidArgument, 0)
	}
	if now < m.last {
		return m.fail(ErrClockMovedBack, 0)
	}
	flight, ok := m.flights[flightID]
	if !ok || flight.canceled {
		return m.fail(ErrInvalidArgument, 0)
	}
	flight.canceled = true
	m.flights[flightID] = flight
	for id, ticket := range m.tickets {
		if !ticket.refunded && ticket.flight == flightID {
			ticket.involuntary = true
			m.tickets[id] = ticket
		}
	}
	m.last = now
	return naiveOutcome{ok: true}
}

func TestRandomOperationsAgainstNaiveModel(t *testing.T) {
	cfg := Config{
		LongThreshold:  7200,
		ShortThreshold: 1800,
		RefundRates:    Rates{Long: 5, Mid: 15, Short: 30},
		ChangeRates:    Rates{Long: 2, Mid: 8, Short: 20},
		ChangeLimit:    3,
		VoucherTTL:     2500,
	}
	for seed := int64(1); seed <= 30; seed++ {
		t.Run(fmt.Sprintf("seed-%02d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			system, err := NewSystem(cfg)
			if err != nil {
				t.Fatal(err)
			}
			model := newNaiveModel(cfg)
			owners := []string{"alice", "bob", "carol"}
			flightIDs := make([]string, 0, 14)
			ticketIDs := make([]string, 0, 8)
			for index := 0; index < 14; index++ {
				id := fmt.Sprintf("F%d", index)
				price := int64(100 + rng.Intn(1901))
				depart := int64(1000 + rng.Intn(9000))
				if err := system.AddFlight(Flight{ID: id, Price: price, DepartAt: depart}); err != nil {
					t.Fatal(err)
				}
				model.addFlight(id, price, depart)
				flightIDs = append(flightIDs, id)
			}
			for index := 0; index < 8; index++ {
				id := fmt.Sprintf("T%d", index)
				owner := owners[index%len(owners)]
				flightID := flightIDs[index]
				if err := system.IssueTicket(id, owner, flightID, 0); err != nil {
					t.Fatal(err)
				}
				if outcome := model.issue(id, owner, flightID, 0); !outcome.ok {
					t.Fatalf("naive issue rejected: %s", outcome.kind)
				}
				ticketIDs = append(ticketIDs, id)
			}

			actualCash := map[string]int64{}
			expectedCash := map[string]int64{}
			now := int64(1)
			for step := 1; step <= 100; step++ {
				now += int64(1 + rng.Intn(600))
				ticketID := ticketIDs[rng.Intn(len(ticketIDs))]
				choice := rng.Intn(100)
				switch {
				case choice < 10:
					ticket, _ := system.Ticket(ticketID)
					input := fmt.Sprintf("step=%d op=refund ticket=%s now=%d", step, ticketID, now)
					result, callErr := system.Refund(ticketID, now)
					expected := model.refund(ticketID, now)
					t.Logf("%s => system={ok:%v kind:%v cashBack:%d tier:%s basis:%q} naive={ok:%v kind:%v cashBack:%d tier:%s}",
						input, callErr == nil, errorKind(callErr), result.CashRefund, result.Tier, result.Basis,
						expected.ok, expected.kind, expected.cashBack, expected.tier)
					if (callErr == nil) != expected.ok || errorKind(callErr) != expected.kind {
						t.Fatalf("%s: error mismatch system=%v naive=%v", input, callErr, expected.kind)
					}
					if callErr == nil {
						if result.CashRefund != expected.cashBack || result.Fee != expected.fee || result.Tier != expected.tier {
							t.Fatalf("%s: refund mismatch system=%+v naive=%+v", input, result, expected)
						}
						actualCash[ticketID] -= result.CashRefund
						expectedCash[ticketID] -= expected.cashBack
					}
					_ = ticket
				case choice < 22:
					flightID := flightIDs[rng.Intn(len(flightIDs))]
					input := fmt.Sprintf("step=%d op=cancel flight=%s now=%d", step, flightID, now)
					callErr := system.CancelFlight(flightID, now)
					expected := model.cancel(flightID, now)
					t.Logf("%s => system={ok:%v kind:%v} naive={ok:%v kind:%v}", input, callErr == nil, errorKind(callErr), expected.ok, expected.kind)
					if (callErr == nil) != expected.ok || errorKind(callErr) != expected.kind {
						t.Fatalf("%s: cancellation mismatch system=%v naive=%v", input, callErr, expected.kind)
					}
				default:
					ticket, _ := system.Ticket(ticketID)
					flightID := flightIDs[rng.Intn(len(flightIDs))]
					var voucherID *string
					if rng.Intn(100) < 45 && len(model.vouchers) > 0 {
						ids := make([]string, 0, len(model.vouchers))
						for id := range model.vouchers {
							ids = append(ids, id)
						}
						sort.Strings(ids)
						selected := ids[rng.Intn(len(ids))]
						voucherID = &selected
					}
					quote, quoteErr := system.QuoteChange(ticketID, flightID, voucherID, now)
					cash := int64(0)
					if quoteErr == nil {
						cash = quote.CashDue
						if rng.Intn(100) < 20 {
							cash += int64(rng.Intn(50) + 1)
						}
					}
					input := fmt.Sprintf("step=%d op=change ticket=%s target=%s voucher=%v cash=%d now=%d", step, ticketID, flightID, voucherID, cash, now)
					result, callErr := system.Change(ticketID, flightID, voucherID, cash, now)
					var selectedVoucher string
					hasVoucher := voucherID != nil
					if hasVoucher {
						selectedVoucher = *voucherID
					}
					expected := model.change(ticketID, flightID, selectedVoucher, hasVoucher, cash, now)
					t.Logf("%s => system={ok:%v kind:%v fee:%d diff:%d due:%d deduct:%d generated:%s:%d basis:%q} naive={ok:%v kind:%v fee:%d diff:%d due:%d deduct:%d generated:%s:%d}",
						input, callErr == nil, errorKind(callErr), result.ChangeFee, result.PriceDifference, result.CashDue, result.VoucherDeduction, result.GeneratedVoucherID, result.GeneratedVoucher, result.Basis,
						expected.ok, expected.kind, expected.fee, expected.diff, expected.cashDue, expected.deduction, expected.voucherID, expected.voucher)
					if (callErr == nil) != expected.ok || errorKind(callErr) != expected.kind {
						t.Fatalf("%s: error mismatch system=%v naive=%v", input, callErr, expected.kind)
					}
					if callErr == nil {
						if result.ChangeFee != expected.fee || result.PriceDifference != expected.diff || result.CashDue != expected.cashDue ||
							result.VoucherDeduction != expected.deduction || result.GeneratedVoucher != expected.voucher ||
							result.GeneratedVoucherID != expected.voucherID || result.VoucherID != expected.applied || result.Tier != expected.tier {
							t.Fatalf("%s: change mismatch system=%+v naive=%+v", input, result, expected)
						}
						actualCash[ticketID] += result.CashDue
						expectedCash[ticketID] += expected.cashDue
					} else if bookingError, ok := callErr.(Error); ok && bookingError.Kind == ErrCashMismatch {
						if expected.cashDue != quote.CashDue {
							t.Fatalf("%s: expected cash mismatch: naive=%d quote=%d", input, expected.cashDue, quote.CashDue)
						}
						if bookingError.ExpectedCash != quote.CashDue {
							t.Fatalf("%s: error cash %d differs quote %d", input, bookingError.ExpectedCash, quote.CashDue)
						}
					}
					_ = ticket
				}
				assertNaiveStateMatches(t, system, model)
			}
			for ticketID := range actualCash {
				if actualCash[ticketID] != expectedCash[ticketID] {
					t.Fatalf("ticket %s cash conservation mismatch actual=%d expected=%d", ticketID, actualCash[ticketID], expectedCash[ticketID])
				}
			}
		})
	}
}

func assertNaiveStateMatches(t *testing.T, system *System, model *naiveModel) {
	t.Helper()
	for id, expected := range model.tickets {
		actual, ok := system.Ticket(id)
		if !ok {
			t.Fatalf("ticket %s missing", id)
		}
		if actual.Owner != expected.owner || actual.FlightID != expected.flight || actual.Price != expected.price ||
			actual.DepartAt != expected.depart || actual.Changes != expected.changes ||
			actual.PaidChangeFee != expected.paidFees || actual.Refunded != expected.refunded ||
			actual.Involuntary != expected.involuntary {
			t.Fatalf("ticket %s mismatch actual=%+v naive=%+v", id, actual, expected)
		}
	}
	for id, expected := range model.vouchers {
		actual, ok := system.Voucher(id)
		if !ok {
			t.Fatalf("voucher %s missing", id)
		}
		used := expected.amount == 0
		if actual.Owner != expected.owner || actual.Amount != expected.amount || actual.ExpiresAt != expected.expiresAt || actual.Used != used {
			t.Fatalf("voucher %s mismatch actual=%+v naive=%+v", id, actual, expected)
		}
	}
}

func buildBenchmarkSystem(b *testing.B, history, noise int) (*System, string, string) {
	b.Helper()
	cfg := Config{
		LongThreshold:  100,
		ShortThreshold: 10,
		RefundRates:    Rates{},
		ChangeRates:    Rates{},
		ChangeLimit:    history + 1,
		VoucherTTL:     1000,
	}
	system, err := NewSystem(cfg)
	if err != nil {
		b.Fatal(err)
	}
	for index := 0; index <= history+1; index++ {
		if err := system.AddFlight(Flight{ID: fmt.Sprintf("H%d", index), Price: 1000, DepartAt: int64(10000 + history + 1)}); err != nil {
			b.Fatal(err)
		}
	}
	if err := system.IssueTicket("BENCH", "alice", "H0", 0); err != nil {
		b.Fatal(err)
	}
	for index := 1; index <= history; index++ {
		if _, err := system.Change("BENCH", fmt.Sprintf("H%d", index), nil, 0, 0); err != nil {
			b.Fatal(err)
		}
	}
	for index := 0; index < noise; index++ {
		id := fmt.Sprintf("N%d", index)
		if err := system.IssueTicket(id, "bob", "H0", 0); err != nil {
			b.Fatal(err)
		}
	}
	return system, "BENCH", fmt.Sprintf("H%d", history+1)
}

func BenchmarkQuoteRefund(b *testing.B) {
	for _, size := range []int{8, 4096} {
		b.Run(fmt.Sprintf("history-%d-noise-%d", size, size), func(b *testing.B) {
			system, ticketID, _ := buildBenchmarkSystem(b, size, size)
			b.ResetTimer()
			for index := 0; index < b.N; index++ {
				if _, err := system.QuoteRefund(ticketID, 0); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkQuoteChange(b *testing.B) {
	for _, size := range []int{8, 4096} {
		b.Run(fmt.Sprintf("history-%d-noise-%d", size, size), func(b *testing.B) {
			system, ticketID, targetID := buildBenchmarkSystem(b, size, size)
			b.ResetTimer()
			for index := 0; index < b.N; index++ {
				if _, err := system.QuoteChange(ticketID, targetID, nil, 0); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
