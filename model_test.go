package ontology

import (
	"errors"
	"math/rand"
	"sync"
	"testing"
)

type modelBooking struct {
	id, household, elevator string
	start, end              int64
	kind                    MoveKind
	status                  BookingStatus
	penalties               int
}

type modelPermit struct {
	id, household            string
	startDay, endDay         int64
	noisy, extended, checked bool
	status                   PermitStatus
	complaints               int
	revokedDay, lastComplain int64
}

type modelEntry struct {
	at     int64
	kind   LedgerEntryKind
	amount int64
	ref    string
}

type action struct {
	name      string
	now       int64
	household string
	elevator  string
	id        string
	a, b      int64
	flag      bool
	kind      MoveKind
}

type naiveModel struct {
	cfg       Config
	bookings  map[string]*modelBooking
	permits   map[string]*modelPermit
	ledgers   map[string][]modelEntry
	holds     map[string]int
	quiet     []MinuteRange
	holidays  map[int64]struct{}
	moveSeq   int
	permitSeq int
}

func newNaiveModel(cfg Config) *naiveModel {
	return &naiveModel{
		cfg:      cfg,
		bookings: map[string]*modelBooking{},
		permits:  map[string]*modelPermit{},
		ledgers:  map[string][]modelEntry{},
		holds:    map[string]int{},
		holidays: map[int64]struct{}{},
	}
}

func errCode(err error) ErrorCode {
	if err == nil {
		return ""
	}
	var serviceError Error
	if errors.As(err, &serviceError) {
		return serviceError.Code
	}
	return ErrIllegalArgument
}

func TestRandomNaiveComparison(t *testing.T) {
	for seed := int64(1); seed <= 50; seed++ {
		t.Run(fmtSeed(seed), func(t *testing.T) {
			cfg := testConfig()
			service, err := NewService(cfg)
			if err != nil {
				t.Fatal(err)
			}
			model := newNaiveModel(cfg)
			households := []string{"h1", "h2", "h3"}
			elevators := []string{"e1", "e2"}
			for _, household := range households {
				must(t, service.RegisterHousehold(0, household))
				must(t, service.TopUp(0, household, 350))
				model.ledgers[household] = append(model.ledgers[household], modelEntry{0, LedgerTopUp, 350, "top-up"})
			}
			for _, elevator := range elevators {
				must(t, service.RegisterElevator(0, elevator))
			}
			rng := rand.New(rand.NewSource(seed))
			now := int64(0)
			for step := 0; step < 120; step++ {
				now += int64(rng.Intn(30))
				act := randomAction(rng, now, households, elevators, model)
				model.normalize(act.now)
				wantID, wantCode, why := model.apply(act)
				id, callErr := dispatch(service, act)
				if callErr == nil && act.name == "book" {
					model.replaceBookingID(wantID, id)
				}
				if callErr == nil && act.name == "apply" {
					model.replacePermitID(wantID, id)
				}
				t.Logf("seed=%d step=%d input=%+v output={id:%q code:%s err:%v} model={id:%q code:%s} basis=%s",
					seed, step, act, id, errCode(callErr), callErr, wantID, wantCode, why)
				if errCode(callErr) != wantCode {
					if act.name == "book" {
						t.Logf("DIAG service day=%v slots=%v", service.dayBookings[householdDay{act.household, act.a / MinutesPerDay}], service.slots[slotKey{act.elevator, act.a}])
						t.Logf("DIAG booking heap=%v", service.bookingEnds)
						for _, booking := range model.bookings {
							if booking.household == act.household && booking.start/MinutesPerDay == act.a/MinutesPerDay {
								t.Logf("DIAG model same-day=%+v", booking)
							}
						}
					}
					t.Fatalf("step %d %s: got %v want %s (%s)", step, act.name, callErr, wantCode, why)
				}
			}
			model.normalize(now + 600)
			for _, record := range service.bookings {
				got, queryErr := service.GetBooking(now+600, record.ID)
				if queryErr != nil {
					t.Fatal(queryErr)
				}
				want := model.bookings[record.ID]
				if got.Status != want.status || got.Penalties != want.penalties {
					t.Fatalf("booking %s mismatch", record.ID)
				}
			}
			for _, record := range service.permits {
				got, queryErr := service.GetPermit(now+600, record.ID)
				if queryErr != nil {
					t.Fatal(queryErr)
				}
				want := model.permits[record.ID]
				if got.Status != want.status || got.Complaints != want.complaints || got.Extended != want.extended {
					t.Fatalf("permit %s mismatch got=%+v want=%+v", record.ID, got, want)
				}
			}
			for _, household := range households {
				entries, queryErr := service.Ledger(now+600, household)
				if queryErr != nil {
					t.Fatal(queryErr)
				}
				if len(entries) != len(model.ledgers[household]) {
					t.Fatalf("ledger %s length mismatch", household)
				}
				for i := range entries {
					want := model.ledgers[household][i]
					if entries[i].Kind != want.kind || entries[i].Amount != want.amount || entries[i].At != want.at {
						t.Fatalf("ledger %s entry %d mismatch", household, i)
					}
				}
			}
		})
	}
}

func fmtSeed(seed int64) string { return "seed-" + itoa(seed) }

func randomAction(rng *rand.Rand, now int64, households, elevators []string, model *naiveModel) action {
	names := []string{"book", "cancel", "bin", "bout", "apply", "review", "extend", "pin", "pout", "complaint", "lift", "refund", "quiet", "holiday"}
	act := action{name: names[rng.Intn(len(names))], now: now, household: households[rng.Intn(len(households))], elevator: elevators[rng.Intn(len(elevators))], kind: MoveKind(choices(rng, string(MoveIn), string(MoveOut))), flag: rng.Intn(3) == 0}
	usesBooking := act.name == "cancel" || act.name == "bin" || act.name == "bout"
	usesPermit := act.name == "review" || act.name == "extend" || act.name == "pin" ||
		act.name == "pout" || act.name == "complaint" || act.name == "lift" || act.name == "refund"
	if usesBooking {
		act.id = knownID(model.randomBookingID(rng, act.household))
	}
	if usesPermit {
		act.id = knownID(model.randomPermitID(rng, act.household))
	}
	switch act.name {
	case "book":
		lead := []int64{9, 10, 15, 80, 100, 120}[rng.Intn(6)]
		act.a, act.b = now+lead, now+lead+1+int64(rng.Intn(5))
	case "apply":
		act.a = now/MinutesPerDay + 1 + int64(rng.Intn(4))
		act.b = act.a + 1 + int64(rng.Intn(4))
	case "extend":
		act.b = now/MinutesPerDay + 1 + int64(rng.Intn(5))
	case "quiet":
		act.a = int64(rng.Intn(int(MinutesPerDay)))
		act.b = (act.a + 1 + int64(rng.Intn(300))) % MinutesPerDay
	case "holiday":
		act.a = now/MinutesPerDay + 1 + int64(rng.Intn(4))
	}
	return act
}

func choices(rng *rand.Rand, values ...string) string { return values[rng.Intn(len(values))] }

func knownID(id string) string {
	if id == "" {
		return ""
	}
	return id
}

func dispatch(service *Service, act action) (string, error) {
	switch act.name {
	case "book":
		return service.BookMove(act.now, act.household, act.elevator, act.a, act.b, act.kind)
	case "cancel":
		return "", service.CancelBooking(act.now, act.id)
	case "bin":
		return "", service.BookingCheckIn(act.now, act.id)
	case "bout":
		return "", service.BookingCheckOut(act.now, act.id)
	case "apply":
		return service.ApplyPermit(act.now, act.household, act.a, act.b, act.flag)
	case "review":
		return "", service.ReviewPermit(act.now, act.id, true)
	case "extend":
		return "", service.ExtendPermit(act.now, act.id, act.b)
	case "pin":
		return "", service.PermitCheckIn(act.now, act.id)
	case "pout":
		return "", service.PermitCheckOut(act.now, act.id)
	case "complaint":
		return "", service.AddComplaint(act.now, act.id)
	case "lift":
		return "", service.LiftSuspension(act.now, act.id)
	case "refund":
		return "", service.RefundPermit(act.now, act.id)
	case "quiet":
		return "", service.SetQuietRanges(act.now, []MinuteRange{{Start: int(act.a), End: int(act.b)}})
	case "holiday":
		return "", service.AddHoliday(act.now, act.a)
	}
	return "", illegal("unknown action")
}

func (m *naiveModel) normalize(now int64) {
	for _, booking := range m.bookings {
		if booking.status == BookingReserved && now > booking.end {
			booking.status = BookingNoShow
			booking.penalties++
			m.penalty(booking.household, booking.id, booking.end)
		}
	}
	today := now / MinutesPerDay
	for _, permit := range m.permits {
		if today >= permit.endDay &&
			(permit.status == PermitApplied || permit.status == PermitApproved ||
				permit.status == PermitActive || permit.status == PermitSuspended) {
			permit.status = PermitExpired
			permit.checked = false
		}
	}
}

func (m *naiveModel) apply(act action) (string, ErrorCode, string) {
	if requiresID(act.name) && act.id == "" {
		return "", ErrIllegalArgument, "missing id argument"
	}
	switch act.name {
	case "book":
		return m.book(act)
	case "apply":
		return m.applyPermit(act)
	case "cancel":
		booking := m.bookings[act.id]
		if booking == nil {
			return "", ErrNotFound, "booking missing"
		}
		if act.now >= booking.start {
			return "", ErrTimeWindow, "cancel before start"
		}
		if booking.status != BookingReserved {
			return "", ErrInvalidState, "booking state"
		}
		booking.status = BookingCanceled
		if booking.start-act.now < m.cfg.AdvanceMin {
			booking.penalties++
			m.penalty(booking.household, booking.id, act.now)
		} else {
			m.holds[booking.household]--
		}
	case "bin":
		booking := m.bookings[act.id]
		if booking == nil {
			return "", ErrNotFound, "booking missing"
		}
		if act.now < booking.start-m.cfg.CheckInLeadMin || act.now > booking.end {
			return "", ErrTimeWindow, "booking check-in window"
		}
		if booking.status != BookingReserved {
			return "", ErrInvalidState, "booking state"
		}
		for _, other := range m.bookings {
			if other.elevator == booking.elevator && other.status == BookingActive && other.id != booking.id {
				return "", ErrInvalidState, "elevator busy"
			}
		}
		booking.status = BookingActive
	case "bout":
		booking := m.bookings[act.id]
		if booking == nil {
			return "", ErrNotFound, "booking missing"
		}
		if booking.status != BookingActive {
			return "", ErrInvalidState, "booking inactive"
		}
		booking.status = BookingDone
		m.holds[booking.household]--
	case "review":
		permit := m.permits[act.id]
		if permit == nil {
			return "", ErrNotFound, "permit missing"
		}
		if act.now/MinutesPerDay >= permit.endDay {
			return "", ErrTimeWindow, "review before expiry"
		}
		if permit.status != PermitApplied {
			return "", ErrInvalidState, "permit review state"
		}
		permit.status = PermitApproved
	case "extend":
		if permit := m.permits[act.id]; permit != nil {
			if permit.extended {
				return "", ErrTimeWindow, "extension used"
			}
			if act.now/MinutesPerDay >= permit.endDay {
				return "", ErrTimeWindow, "extension before expiry"
			}
			if permit.status != PermitApplied && permit.status != PermitApproved &&
				permit.status != PermitActive && permit.status != PermitSuspended {
				return "", ErrInvalidState, "permit extension state"
			}
			if act.b <= permit.endDay || act.b-permit.startDay > m.cfg.MaxWorkDays {
				return "", ErrIllegalArgument, "extension interval"
			}
			permit.endDay = act.b
			permit.extended = true
		} else {
			return "", ErrNotFound, "permit missing"
		}
	case "pin":
		return m.permitCheckIn(act)
	case "pout":
		permit := m.permits[act.id]
		if permit == nil {
			return "", ErrNotFound, "permit missing"
		}
		if (permit.status != PermitActive && permit.status != PermitSuspended) || !permit.checked {
			return "", ErrInvalidState, "permit not checked in"
		}
		permit.checked = false
		if permit.status == PermitActive {
			permit.status = PermitApproved
		}
	case "complaint":
		permit := m.permits[act.id]
		if permit == nil {
			return "", ErrNotFound, "permit missing"
		}
		if permit.status == PermitRejected || permit.status == PermitRevoked || permit.status == PermitRefunded {
			return "", ErrInvalidState, "complaint state"
		}
		if permit.status == PermitExpired && permit.complaints == 0 {
			return "", ErrInvalidState, "unstarted expired permit"
		}
		permit.complaints++
		permit.lastComplain = act.now / MinutesPerDay
		if permit.complaints >= m.cfg.ComplaintLimit {
			permit.status = PermitRevoked
			permit.revokedDay = permit.lastComplain
			permit.checked = false
			m.holds[permit.household]--
		} else if permit.status != PermitExpired {
			permit.status = PermitSuspended
		}
	case "lift":
		permit := m.permits[act.id]
		if permit == nil {
			return "", ErrNotFound, "permit missing"
		}
		if permit.status != PermitSuspended {
			return "", ErrInvalidState, "not suspended"
		}
		if act.now/MinutesPerDay >= permit.endDay {
			return "", ErrInvalidState, "expired suspended"
		}
		if permit.checked {
			permit.status = PermitActive
		} else {
			permit.status = PermitApproved
		}
	case "refund":
		return m.refund(act)
	case "quiet":
		m.quiet = []MinuteRange{{Start: int(act.a), End: int(act.b)}}
	case "holiday":
		if act.a <= act.now/MinutesPerDay {
			return "", ErrTimeWindow, "holiday future date"
		}
		m.holidays[act.a] = struct{}{}
	}
	return "", "", "noop"
}

func requiresID(name string) bool {
	return name == "cancel" || name == "bin" || name == "bout" || name == "review" ||
		name == "extend" || name == "pin" || name == "pout" || name == "complaint" ||
		name == "lift" || name == "refund"
}

func (m *naiveModel) book(act action) (string, ErrorCode, string) {
	if act.a < 0 || act.b <= act.a {
		return "", ErrIllegalArgument, "booking interval"
	}
	advance := act.a - act.now
	if advance < m.cfg.AdvanceMin || advance > m.cfg.MaxAdvanceMin {
		return "", ErrTimeWindow, "booking lead"
	}
	if m.cash(act.household)-int64(m.holds[act.household])*m.cfg.Penalty < m.cfg.Penalty {
		return "", ErrInsufficientDeposit, "booking deposit"
	}
	for _, booking := range m.bookings {
		if booking.household == act.household && booking.start/MinutesPerDay == act.a/MinutesPerDay &&
			(booking.status == BookingReserved || booking.status == BookingActive) {
			return "", ErrInvalidState, "one booking per day"
		}
		if booking.elevator == act.elevator && booking.status == BookingReserved &&
			act.a < booking.end && act.b > booking.start {
			return "", ErrInvalidState, "slot occupied"
		}
	}
	m.moveSeq++
	id := fmtMoveID(m.moveSeq)
	m.bookings[id] = &modelBooking{id: id, household: act.household, elevator: act.elevator, start: act.a, end: act.b, kind: act.kind, status: BookingReserved}
	m.holds[act.household]++
	return id, "", "created booking"
}

func (m *naiveModel) applyPermit(act action) (string, ErrorCode, string) {
	if act.a < 0 || act.b <= act.a || act.b-act.a > m.cfg.MaxWorkDays {
		return "", ErrIllegalArgument, "permit interval"
	}
	today := act.now / MinutesPerDay
	if act.a <= today {
		return "", ErrTimeWindow, "permit future start"
	}
	if m.cash(act.household)-int64(m.holds[act.household])*m.cfg.Penalty < m.cfg.Penalty {
		return "", ErrInsufficientDeposit, "permit deposit"
	}
	for _, permit := range m.permits {
		if permit.household != act.household {
			continue
		}
		live := (permit.status == PermitActive || permit.status == PermitSuspended) ||
			((permit.status == PermitApplied || permit.status == PermitApproved) && today < permit.endDay)
		if live {
			return "", ErrInvalidState, "existing valid permit"
		}
		if permit.status == PermitRevoked && act.a < permit.revokedDay+m.cfg.ReapplyWaitDays {
			return "", ErrTimeWindow, "reapplication wait"
		}
	}
	m.permitSeq++
	id := fmtPermitID(m.permitSeq)
	m.permits[id] = &modelPermit{id: id, household: act.household, startDay: act.a, endDay: act.b, noisy: act.flag, status: PermitApplied}
	m.holds[act.household]++
	return id, "", "created permit"
}

func (m *naiveModel) cash(household string) int64 {
	if len(m.ledgers[household]) == 0 {
		return 0
	}
	var balance int64
	for _, entry := range m.ledgers[household] {
		balance += entry.amount
	}
	return balance
}

func (m *naiveModel) penalty(household, id string, now int64) {
	m.holds[household]--
	m.ledgers[household] = append(m.ledgers[household], modelEntry{now, LedgerPenalty, -m.cfg.Penalty, id})
}

func fmtMoveID(seq int) string   { return "model-move-" + itoa(int64(seq)) }
func fmtPermitID(seq int) string { return "model-permit-" + itoa(int64(seq)) }

func (m *naiveModel) permitCheckIn(act action) (string, ErrorCode, string) {
	permit := m.permits[act.id]
	if permit == nil {
		return "", ErrNotFound, "permit missing"
	}
	today := act.now / MinutesPerDay
	if today < permit.startDay || today >= permit.endDay {
		return "", ErrTimeWindow, "permit work interval"
	}
	if permit.status == PermitSuspended {
		return "", ErrInvalidState, "permit suspended"
	}
	if permit.status == PermitRevoked {
		return "", ErrInvalidState, "permit revoked"
	}
	if permit.status != PermitApproved && permit.status != PermitActive {
		return "", ErrInvalidState, "permit not approved"
	}
	if permit.noisy && m.isQuiet(act.now) {
		return "", ErrQuietConflict, "quiet time"
	}
	permit.status = PermitActive
	permit.checked = true
	return "", "", "permit checked in"
}

func (m *naiveModel) refund(act action) (string, ErrorCode, string) {
	permit := m.permits[act.id]
	if permit == nil {
		return "", ErrNotFound, "permit missing"
	}
	today := act.now / MinutesPerDay
	effective := permit.status
	if today >= permit.endDay && (effective == PermitApproved || effective == PermitActive) {
		effective = PermitExpired
	}
	if effective != PermitExpired && effective != PermitActive {
		return "", ErrInvalidState, "refund permit state"
	}
	if today < permit.endDay {
		return "", ErrInvalidState, "construction not terminated"
	}
	latest := permit.endDay
	if permit.lastComplain > latest {
		latest = permit.lastComplain
	}
	if today < latest+m.cfg.RefundDays {
		return "", ErrTimeWindow, "refund waiting period"
	}
	permit.status = PermitRefunded
	permit.checked = false
	m.holds[permit.household]--
	m.ledgers[permit.household] = append(m.ledgers[permit.household],
		modelEntry{act.now, LedgerRefund, -m.cfg.Penalty, permit.id})
	return "", "", "refunded"
}

func (m *naiveModel) isQuiet(now int64) bool {
	if _, holiday := m.holidays[now/MinutesPerDay]; holiday {
		return true
	}
	minute := int(now % MinutesPerDay)
	for _, quietRange := range m.quiet {
		if quietRange.Start < quietRange.End {
			if minute >= quietRange.Start && minute < quietRange.End {
				return true
			}
		} else if minute >= quietRange.Start || minute < quietRange.End {
			return true
		}
	}
	return false
}

func (m *naiveModel) randomBookingID(rng *rand.Rand, household string) string {
	var ids []string
	for _, booking := range m.bookings {
		if booking.household == household {
			ids = append(ids, booking.id)
		}
	}
	if len(ids) == 0 {
		return ""
	}
	return ids[rng.Intn(len(ids))]
}

func (m *naiveModel) randomPermitID(rng *rand.Rand, household string) string {
	var ids []string
	for _, permit := range m.permits {
		if permit.household == household {
			ids = append(ids, permit.id)
		}
	}
	if len(ids) == 0 {
		return ""
	}
	return ids[rng.Intn(len(ids))]
}

func (m *naiveModel) replaceBookingID(oldID, newID string) {
	record := m.bookings[oldID]
	delete(m.bookings, oldID)
	record.id = newID
	m.bookings[newID] = record
}

func (m *naiveModel) replacePermitID(oldID, newID string) {
	record := m.permits[oldID]
	delete(m.permits, oldID)
	record.id = newID
	m.permits[newID] = record
}

func TestConcurrentSlotAndDepositLinearization(t *testing.T) {
	service := setupService(t)
	const goroutines = 80
	var wait sync.WaitGroup
	start := make(chan struct{})
	successes := make(chan string, goroutines)
	for i := 0; i < goroutines; i++ {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			<-start
			id, err := service.BookMove(100, "h1", "e1", 200, 201, MoveIn)
			if err == nil {
				successes <- id
			}
		}(i)
	}
	close(start)
	wait.Wait()
	close(successes)
	count := 0
	for range successes {
		count++
	}
	if count != 1 {
		t.Fatalf("same slot accepted %d times", count)
	}
}

func TestConcurrentSharedDepositAndLinearization(t *testing.T) {
	service := setupService(t)
	permitID, err := service.ApplyPermit(0, "h1", 1, 2, false)
	if err != nil {
		t.Fatal(err)
	}
	must(t, service.ReviewPermit(10, permitID, true))
	refundTime := int64(4 * MinutesPerDay)
	bookingID, err := service.BookMove(refundTime-15, "h1", "e1", refundTime-5, refundTime-4, MoveIn)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		if err := service.RefundPermit(refundTime, permitID); err != nil {
			t.Errorf("refund: %v", err)
		}
	}()
	noShowDone := make(chan struct{})
	go func() {
		defer wait.Done()
		<-start
		close(noShowDone)
		if _, err := service.GetBooking(refundTime, bookingID); err != nil {
			t.Errorf("no-show query: %v", err)
		}
	}()
	close(start)
	<-noShowDone
	wait.Wait()
	balance, err := service.Balance(refundTime, "h1")
	if err != nil {
		t.Fatal(err)
	}
	if balance != 0 {
		t.Fatalf("shared balance after concurrent deductions = %d", balance)
	}
}

func BenchmarkSlotBookWithLargeHistory(b *testing.B) {
	service, err := NewService(testConfig())
	if err != nil {
		b.Fatal(err)
	}
	mustB(b, service.RegisterElevator(0, "e1"))
	for i := 0; i < b.N; i++ {
		household := "bench-" + itoa(int64(i))
		now := int64(i)
		mustB(b, service.RegisterHousehold(now, household))
		mustB(b, service.TopUp(now, household, 100))
		start := now + 10
		_, err := service.BookMove(now, household, "e1", start, start+1, MoveIn)
		if err != nil {
			b.Fatal(err)
		}
	}
	mustB(b, service.RegisterHousehold(int64(b.N), "bench-probe"))
	mustB(b, service.TopUp(int64(b.N), "bench-probe", 100))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := service.BookMove(int64(b.N+2000), "bench-probe", "e1", int64(b.N+2100), int64(b.N+2101), MoveIn); err != nil {
			code := errCode(err)
			if code != ErrInvalidState && code != ErrInsufficientDeposit {
				b.Fatal(err)
			}
		}
	}
}

func mustB(b *testing.B, err error) {
	b.Helper()
	if err != nil {
		b.Fatal(err)
	}
}
