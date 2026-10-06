package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

type naiveReservation struct {
	id, house, elevator string
	start, end          int
	status              ReservationStatus
}

type naivePermit struct {
	id, house           string
	startDay, endDay    int
	noisy               bool
	status              PermitStatus
	extended, suspended bool
	checkedIn           bool
	complaints          int
	lastComplaint       int
	revokedDay          int
}

type naiveModel struct {
	cfg       Config
	now       int
	deposits  map[string]int
	reserved  map[string]*naiveReservation
	permits   map[string]*naivePermit
	active    map[string]*naivePermit
	revokedAt map[string]int
	quiet     []QuietRange
	holidays  map[int]bool
	log       []string
	ledger    []LedgerEntry
}

func newNaive(cfg Config) *naiveModel {
	return &naiveModel{
		cfg:       cfg,
		deposits:  map[string]int{},
		reserved:  map[string]*naiveReservation{},
		permits:   map[string]*naivePermit{},
		active:    map[string]*naivePermit{},
		revokedAt: map[string]int{},
		holidays:  map[int]bool{},
	}
}

func (m *naiveModel) advance(now int) {
	for _, r := range m.reserved {
		if r.status == ReservationReserved && now > r.end {
			r.status = ReservationNoShow
			paid := minInt(m.deposits[r.house], m.cfg.Penalty)
			m.deposits[r.house] -= paid
			m.ledger = append(m.ledger, LedgerEntry{Now: now, House: r.house, Amount: -paid, Reason: "penalty", Balance: m.deposits[r.house]})
		}
	}
	for house, p := range m.active {
		if !p.checkedIn && floorDay(now) >= p.endDay && p.status != PermitRevoked {
			p.status = PermitFinished
			delete(m.active, house)
		}
	}
	m.now = now
}

func (m *naiveModel) fail(op string, in interface{}, code ErrorCode, why string) error {
	m.log = append(m.log, fmt.Sprintf("%s in=%v OUT=%d WHY=%s", op, in, code, why))
	return fail(code, why)
}

func (m *naiveModel) ok(op string, in interface{}, why string) {
	m.log = append(m.log, fmt.Sprintf("%s in=%v OUT=OK WHY=%s", op, in, why))
}

func (m *naiveModel) deposit(now int, house string, amount int) error {
	if now < 0 || house == "" || amount < 0 {
		return m.fail("deposit", house, ErrInvalidArgument, "nonnegative input")
	}
	if now < m.now {
		return m.fail("deposit", house, ErrClockRewound, "monotonic")
	}
	m.deposits[house] += amount
	m.ledger = append(m.ledger, LedgerEntry{Now: now, House: house, Amount: amount, Reason: "deposit", Balance: m.deposits[house]})
	m.advance(now)
	m.ok("deposit", house, "credit")
	return nil
}

func (m *naiveModel) reserve(now int, id, house, elevator string, start, end int) error {
	if now < 0 || id == "" || house == "" || elevator == "" || start >= end {
		return m.fail("reserve", id, ErrInvalidArgument, "valid input")
	}
	if now < m.now {
		return m.fail("reserve", id, ErrClockRewound, "monotonic")
	}
	if _, exists := m.reserved[id]; exists {
		return m.fail("reserve", id, ErrInvalidArgument, "duplicate id")
	}
	lead := start - now
	if lead < m.cfg.EarlyMin || lead > m.cfg.LateMin {
		return m.fail("reserve", id, ErrTimeWindow, "lead window")
	}
	for _, r := range m.reserved {
		if r.status == ReservationReserved && r.house == house && floorDay(r.start) == floorDay(start) {
			return m.fail("reserve", id, ErrIllegalState, "one per day")
		}
	}
	for minute := start; minute < end; minute++ {
		for _, r := range m.reserved {
			if r.status == ReservationReserved && r.elevator == elevator && minute >= r.start && minute < r.end && now <= r.end {
				return m.fail("reserve", id, ErrIllegalState, "occupied")
			}
		}
	}
	if m.deposits[house] < m.cfg.Penalty {
		return m.fail("reserve", id, ErrInsufficientDeposit, "low deposit")
	}
	m.advance(now)
	m.reserved[id] = &naiveReservation{id: id, house: house, elevator: elevator, start: start, end: end, status: ReservationReserved}
	m.ok("reserve", id, "created")
	return nil
}

func (m *naiveModel) cancel(now int, id string) error {
	if now < 0 || id == "" {
		return m.fail("cancel", id, ErrInvalidArgument, "valid input")
	}
	if now < m.now {
		return m.fail("cancel", id, ErrClockRewound, "monotonic")
	}
	r := m.reserved[id]
	if r == nil {
		return m.fail("cancel", id, ErrNotFound, "missing")
	}
	if r.status != ReservationReserved {
		return m.fail("cancel", id, ErrIllegalState, "not cancellable")
	}
	if now > r.start {
		return m.fail("cancel", id, ErrTimeWindow, "after start")
	}
	if r.start-now < m.cfg.EarlyMin {
		paid := minInt(m.deposits[r.house], m.cfg.Penalty)
		m.deposits[r.house] -= paid
		m.ledger = append(m.ledger, LedgerEntry{Now: now, House: r.house, Amount: -paid, Reason: "penalty", Balance: m.deposits[r.house]})
	}
	r.status = ReservationCancelled
	m.advance(now)
	m.ok("cancel", id, "cancelled")
	return nil
}

func (m *naiveModel) elevatorCheckIn(now int, id string) error {
	if now < 0 || id == "" {
		return m.fail("elevator-check-in", id, ErrInvalidArgument, "valid input")
	}
	if now < m.now {
		return m.fail("elevator-check-in", id, ErrClockRewound, "monotonic")
	}
	r := m.reserved[id]
	if r == nil {
		return m.fail("elevator-check-in", id, ErrNotFound, "missing")
	}
	if r.status != ReservationReserved {
		return m.fail("elevator-check-in", id, ErrIllegalState, "state")
	}
	if now < r.start-m.cfg.CheckInMin || now > r.end {
		return m.fail("elevator-check-in", id, ErrTimeWindow, "window")
	}
	for _, other := range m.reserved {
		if other.elevator == r.elevator && other.id != id && other.status == ReservationCheckedIn {
			return m.fail("elevator-check-in", id, ErrIllegalState, "previous hold")
		}
	}
	m.advance(now)
	r.status = ReservationCheckedIn
	m.ok("elevator-check-in", id, "in")
	return nil
}

func (m *naiveModel) elevatorCheckOut(now int, id string) error {
	if now < 0 || id == "" {
		return m.fail("elevator-check-out", id, ErrInvalidArgument, "valid input")
	}
	if now < m.now {
		return m.fail("elevator-check-out", id, ErrClockRewound, "monotonic")
	}
	r := m.reserved[id]
	if r == nil {
		return m.fail("elevator-check-out", id, ErrNotFound, "missing")
	}
	if r.status != ReservationCheckedIn {
		return m.fail("elevator-check-out", id, ErrIllegalState, "state")
	}
	m.advance(now)
	r.status = ReservationCompleted
	m.ok("elevator-check-out", id, "out")
	return nil
}

func (m *naiveModel) applyPermit(now int, id, house string, start, end int, noisy bool) error {
	if now < 0 || id == "" || house == "" || start >= end || end-start > m.cfg.MaxDays {
		return m.fail("permit-apply", id, ErrInvalidArgument, "valid interval")
	}
	if now < m.now {
		return m.fail("permit-apply", id, ErrClockRewound, "monotonic")
	}
	if _, exists := m.permits[id]; exists {
		return m.fail("permit-apply", id, ErrInvalidArgument, "duplicate id")
	}
	if floorDay(now) >= start {
		return m.fail("permit-apply", id, ErrTimeWindow, "before start")
	}
	if day, ok := m.revokedAt[house]; ok && start < day+m.cfg.WaitDays {
		return m.fail("permit-apply", id, ErrTimeWindow, "W wait")
	}
	if m.active[house] != nil {
		return m.fail("permit-apply", id, ErrIllegalState, "active permit")
	}
	if m.deposits[house] < m.cfg.Penalty {
		return m.fail("permit-apply", id, ErrInsufficientDeposit, "low deposit")
	}
	m.advance(now)
	p := &naivePermit{id: id, house: house, startDay: start, endDay: end, noisy: noisy, status: PermitPending}
	m.permits[id] = p
	m.active[house] = p
	m.ok("permit-apply", id, "pending")
	return nil
}

func (m *naiveModel) approvePermit(now int, id string) error {
	if now < 0 || id == "" {
		return m.fail("permit-approve", id, ErrInvalidArgument, "valid input")
	}
	if now < m.now {
		return m.fail("permit-approve", id, ErrClockRewound, "monotonic")
	}
	p := m.permits[id]
	if p == nil {
		return m.fail("permit-approve", id, ErrNotFound, "missing")
	}
	if m.active[p.house] != p || p.status != PermitPending {
		return m.fail("permit-approve", id, ErrIllegalState, "state")
	}
	m.advance(now)
	p.status = PermitApproved
	m.ok("permit-approve", id, "approved")
	return nil
}

func (m *naiveModel) permitCheckIn(now int, id string) error {
	if now < 0 || id == "" {
		return m.fail("permit-check-in", id, ErrInvalidArgument, "valid input")
	}
	if now < m.now {
		return m.fail("permit-check-in", id, ErrClockRewound, "monotonic")
	}
	p := m.permits[id]
	if p == nil {
		return m.fail("permit-check-in", id, ErrNotFound, "missing")
	}
	day := floorDay(now)
	if day < p.startDay || day >= p.endDay {
		return m.fail("permit-check-in", id, ErrTimeWindow, "date window")
	}
	if m.active[p.house] != p {
		return m.fail("permit-check-in", id, ErrIllegalState, "not effective")
	}
	if p.suspended {
		return m.fail("permit-check-in", id, ErrIllegalState, "suspended")
	}
	if p.status != PermitApproved && p.status != PermitActive {
		return m.fail("permit-check-in", id, ErrIllegalState, "not approved")
	}
	if p.noisy && m.isQuiet(now) {
		return m.fail("permit-check-in", id, ErrQuietConflict, "quiet")
	}
	m.advance(now)
	p.status = PermitActive
	p.checkedIn = true
	m.ok("permit-check-in", id, "active")
	return nil
}

func (m *naiveModel) permitCheckOut(now int, id string) error {
	if now < 0 || id == "" {
		return m.fail("permit-check-out", id, ErrInvalidArgument, "valid input")
	}
	if now < m.now {
		return m.fail("permit-check-out", id, ErrClockRewound, "monotonic")
	}
	p := m.permits[id]
	if p == nil {
		return m.fail("permit-check-out", id, ErrNotFound, "missing")
	}
	if !p.checkedIn {
		return m.fail("permit-check-out", id, ErrIllegalState, "not in")
	}
	m.advance(now)
	p.checkedIn = false
	if p.suspended {
		p.status = PermitSuspended
	} else {
		p.status = PermitApproved
	}
	m.ok("permit-check-out", id, "out")
	return nil
}

func (m *naiveModel) complain(now int, house string) error {
	if now < 0 || house == "" {
		return m.fail("complain", house, ErrInvalidArgument, "valid input")
	}
	if now < m.now {
		return m.fail("complain", house, ErrClockRewound, "monotonic")
	}
	p := m.active[house]
	if p == nil {
		return m.fail("complain", house, ErrNotFound, "missing active")
	}
	m.advance(now)
	p.complaints++
	p.lastComplaint = floorDay(now)
	if p.complaints >= m.cfg.RevokeAt {
		p.status = PermitRevoked
		p.suspended = false
		p.checkedIn = false
		p.revokedDay = floorDay(now)
		m.revokedAt[house] = p.revokedDay
		delete(m.active, house)
	} else {
		p.suspended = true
		p.status = PermitSuspended
	}
	m.ok("complain", house, "recorded")
	return nil
}

func (m *naiveModel) isQuiet(minute int) bool {
	day := floorDay(minute)
	if m.holidays[day] {
		return true
	}
	tod := minute - day*1440
	for _, r := range m.quiet {
		if r.StartMinute < r.EndMinute && tod >= r.StartMinute && tod < r.EndMinute {
			return true
		}
		if r.StartMinute > r.EndMinute && (tod >= r.StartMinute || tod < r.EndMinute) {
			return true
		}
	}
	return false
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type randomOp struct {
	kind  int
	now   int
	house string
	id    string
	start int
	end   int
	sday  int
	eday  int
	noisy bool
}

func dispatchNaive(s *Service, m *naiveModel, op randomOp) (ErrorCode, ErrorCode) {
	var ne error
	switch op.kind {
	case 0:
		ne = m.deposit(op.now, op.house, 200)
		return codeOf(s.Deposit(DepositRequest{Now: op.now, House: op.house, Amount: 200})), codeOf(ne)
	case 1:
		ne = m.reserve(op.now, op.id, op.house, "E1", op.start, op.end)
		return codeOf(s.Reserve(ReservationRequest{Now: op.now, ID: op.id, House: op.house, Elevator: "E1", Start: op.start, End: op.end})), codeOf(ne)
	case 2:
		ne = m.cancel(op.now, op.id)
		return codeOf(s.Cancel(ReservationAction{Now: op.now, ID: op.id})), codeOf(ne)
	case 3:
		ne = m.elevatorCheckIn(op.now, op.id)
		return codeOf(s.CheckInElevator(ReservationAction{Now: op.now, ID: op.id})), codeOf(ne)
	case 4:
		ne = m.elevatorCheckOut(op.now, op.id)
		return codeOf(s.CheckOutElevator(ReservationAction{Now: op.now, ID: op.id})), codeOf(ne)
	case 5:
		ne = m.applyPermit(op.now, op.id, op.house, op.sday, op.eday, op.noisy)
		return codeOf(s.ApplyPermit(PermitRequest{Now: op.now, ID: op.id, House: op.house, StartDay: op.sday, EndDay: op.eday, Noisy: op.noisy})), codeOf(ne)
	case 6:
		ne = m.approvePermit(op.now, op.id)
		return codeOf(s.ApprovePermit(PermitAction{Now: op.now, ID: op.id})), codeOf(ne)
	case 7:
		ne = m.permitCheckIn(op.now, op.id)
		return codeOf(s.CheckInPermit(PermitAction{Now: op.now, ID: op.id})), codeOf(ne)
	case 8:
		ne = m.permitCheckOut(op.now, op.id)
		return codeOf(s.CheckOutPermit(PermitAction{Now: op.now, ID: op.id})), codeOf(ne)
	default:
		ne = m.complain(op.now, op.house)
		return codeOf(s.Complain(HouseAction{Now: op.now, House: op.house})), codeOf(ne)
	}
}

func naiveState(m *naiveModel) map[string]interface{} {
	res := map[string][3]int{}
	for id, r := range m.reserved {
		res[id] = [3]int{int(r.status), r.start, r.end}
	}
	per := map[string][4]int{}
	for id, p := range m.permits {
		noisy := 0
		if p.noisy {
			noisy = 1
		}
		per[id] = [4]int{int(p.status), p.complaints, p.endDay, noisy}
	}
	keys := make([]string, 0, len(m.deposits))
	for house := range m.deposits {
		keys = append(keys, house)
	}
	sort.Strings(keys)
	balances := map[string]int{}
	for _, house := range keys {
		balances[house] = m.deposits[house]
	}
	ledger := append([]LedgerEntry(nil), m.ledger...)
	sort.Slice(ledger, func(i, j int) bool {
		if ledger[i].Now != ledger[j].Now {
			return ledger[i].Now < ledger[j].Now
		}
		if ledger[i].House != ledger[j].House {
			return ledger[i].House < ledger[j].House
		}
		return ledger[i].Reason < ledger[j].Reason
	})
	return map[string]interface{}{"res": res, "per": per, "bal": balances, "ledger": ledger}
}

func realState(s *Service, now int) map[string]interface{} {
	q := s.Snapshot(now)
	res := map[string][3]int{}
	for _, r := range q.Reservations {
		res[r.ID] = [3]int{int(r.Status), r.Start, r.End}
	}
	per := map[string][4]int{}
	for _, p := range q.Permits {
		noisy := 0
		if p.Noisy {
			noisy = 1
		}
		per[p.ID] = [4]int{int(p.Status), p.Complaints, p.EndDay, noisy}
	}
	balances := map[string]int{}
	for _, e := range q.Ledger {
		balances[e.House] = e.Balance
	}
	ledger := append([]LedgerEntry(nil), q.Ledger...)
	sort.Slice(ledger, func(i, j int) bool {
		if ledger[i].Now != ledger[j].Now {
			return ledger[i].Now < ledger[j].Now
		}
		if ledger[i].House != ledger[j].House {
			return ledger[i].House < ledger[j].House
		}
		return ledger[i].Reason < ledger[j].Reason
	})
	return map[string]interface{}{"res": res, "per": per, "bal": balances, "ledger": ledger}
}

func TestRandomNaiveEquivalence(t *testing.T) {
	cfg := testConfig()
	for seed := int64(1); seed <= 60; seed++ {
		rng := rand.New(rand.NewSource(seed))
		s := NewService(cfg)
		m := newNaive(cfg)
		s.SetQuietRanges(QuietRequest{Now: 0, Ranges: []QuietRange{{StartMinute: 1320, EndMinute: 360}}})
		m.quiet = []QuietRange{{StartMinute: 1320, EndMinute: 360}}
		now := 0
		for step := 0; step < 80; step++ {
			now += rng.Intn(4)
			house := fmt.Sprintf("H%d", rng.Intn(3))
			id := fmt.Sprintf("%s-%d", house, step)
			op := randomOp{
				kind: rng.Intn(10), now: now, house: house, id: id,
				start: now + 30 + rng.Intn(91), end: now + 40 + rng.Intn(91),
				sday:  floorDay(now) + 1 + rng.Intn(3),
				eday:  floorDay(now) + 2 + rng.Intn(6),
				noisy: rng.Intn(2) == 0,
			}
			if op.end <= op.start {
				op.end = op.start + 1
			}
			if op.eday <= op.sday {
				op.eday = op.sday + 1
			}
			realCode, naiveCode := dispatchNaive(s, m, op)
			if realCode != naiveCode {
				for _, line := range s.Log() {
					t.Log(line)
				}
				t.Fatalf("seed=%d step=%d op=%+v real=%d naive=%d", seed, step, op, realCode, naiveCode)
			}
			m.advance(now)
			if !reflect.DeepEqual(realState(s, now), naiveState(m)) {
				t.Fatalf("state mismatch seed=%d step=%d op=%+v real=%v naive=%v", seed, step, op, realState(s, now), naiveState(m))
			}
		}
	}
}
