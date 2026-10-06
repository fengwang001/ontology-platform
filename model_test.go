package railway

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

type modelTicket struct {
	passenger   string
	origin      int
	destination int
	standing    bool
	seat        *Seat
	usedShared  bool
	refunded    bool
}

type naiveModel struct {
	departs  []int64
	seats    []Seat
	quota    [][]int
	shared   int
	advance  int64
	standCap int
	stand    []int
	tickets  map[string]*modelTicket
	lastTime int64
	hasTime  bool
}

func newNaiveModel(cfg TrainConfig) *naiveModel {
	seats := append([]Seat(nil), cfg.Seats...)
	sort.Slice(seats, func(i, j int) bool {
		if seats[i].Car != seats[j].Car {
			return seats[i].Car < seats[j].Car
		}
		return seats[i].No < seats[j].No
	})
	quota := make([][]int, len(cfg.Stations))
	for origin := range quota {
		quota[origin] = append([]int(nil), cfg.Quota[origin]...)
	}
	return &naiveModel{
		departs:  append([]int64(nil), cfg.Departs...),
		seats:    seats,
		quota:    quota,
		shared:   cfg.SharedQuota,
		advance:  cfg.AdvanceSeconds,
		standCap: int(float64(len(seats)) * cfg.StandingRatio),
		stand:    make([]int, len(cfg.Stations)-1),
		tickets:  make(map[string]*modelTicket),
	}
}

func (m *naiveModel) merge(now int64) {
	for origin := 0; origin < len(m.departs)-1; origin++ {
		if m.departs[origin]-m.advance > now {
			continue
		}
		for destination := origin + 1; destination < len(m.departs); destination++ {
			m.shared += m.quota[origin][destination]
			m.quota[origin][destination] = 0
		}
	}
}

func (m *naiveModel) overlaps(passenger string, origin, destination int) bool {
	for _, issued := range m.tickets {
		if issued.refunded || issued.passenger != passenger {
			continue
		}
		if origin < issued.destination && issued.origin < destination {
			return true
		}
	}
	return false
}

func (m *naiveModel) occupied(seatIndex, edge int) bool {
	for _, issued := range m.tickets {
		if issued.refunded || issued.standing {
			continue
		}
		seat := *issued.seat
		if seat != m.seats[seatIndex] {
			continue
		}
		if issued.origin <= edge && edge < issued.destination {
			return true
		}
	}
	return false
}

func (m *naiveModel) choose(origin, destination int) int {
	best := -1
	bestClass := 3
	for seatIndex := range m.seats {
		free := true
		for edge := origin; edge < destination; edge++ {
			if m.occupied(seatIndex, edge) {
				free = false
			}
		}
		if !free {
			continue
		}
		leftTouch := origin > 0 && m.occupied(seatIndex, origin-1)
		rightTouch := destination < len(m.departs)-1 && m.occupied(seatIndex, destination)
		class := seatClass(leftTouch, rightTouch)
		if class < bestClass {
			bestClass = class
			best = seatIndex
		}
	}
	return best
}

func (m *naiveModel) buy(req BuyRequest) (BuyResult, ErrorCode, string) {
	if req.Time < 0 || req.TrainID != "T" || req.TicketID == "" || req.Passenger == "" || req.Origin < 0 || req.Destination <= req.Origin || req.Destination >= len(m.departs) {
		return BuyResult{}, ErrInvalidArgument, "invalid argument"
	}
	if m.hasTime && req.Time < m.lastTime {
		return BuyResult{}, ErrClockRewind, "clock rewind"
	}
	if _, exists := m.tickets[req.TicketID]; exists {
		return BuyResult{}, ErrInvalidArgument, "duplicate ticket"
	}
	if req.Time >= m.departs[req.Origin] {
		return BuyResult{}, ErrDeparted, "departed"
	}
	if m.overlaps(req.Passenger, req.Origin, req.Destination) {
		return BuyResult{}, ErrPassengerOverlap, "passenger overlap"
	}
	m.merge(req.Time)
	usedShared := false
	if m.quota[req.Origin][req.Destination] > 0 {
		m.quota[req.Origin][req.Destination]--
	} else if m.shared > 0 {
		m.shared--
		usedShared = true
	} else {
		return BuyResult{}, ErrQuotaExhausted, "quota exhausted"
	}
	seatIndex := m.choose(req.Origin, req.Destination)
	if seatIndex >= 0 {
		seat := m.seats[seatIndex]
		m.tickets[req.TicketID] = &modelTicket{
			passenger: req.Passenger, origin: req.Origin, destination: req.Destination,
			seat: &seat, usedShared: usedShared,
		}
		m.lastTime = req.Time
		m.hasTime = true
		return BuyResult{TicketID: req.TicketID, Seat: &seat, UsedSharedQuota: usedShared}, 0, "assigned seat by touch/car/no order"
	}
	canStand := true
	for edge := req.Origin; edge < req.Destination; edge++ {
		if m.stand[edge] >= m.standCap {
			canStand = false
		}
	}
	if !req.AcceptStanding || !canStand {
		if usedShared {
			m.shared++
		} else {
			m.quota[req.Origin][req.Destination]++
		}
		return BuyResult{}, ErrNoSeatAvailable, "no seat and no standing capacity"
	}
	for edge := req.Origin; edge < req.Destination; edge++ {
		m.stand[edge]++
	}
	m.tickets[req.TicketID] = &modelTicket{
		passenger: req.Passenger, origin: req.Origin, destination: req.Destination,
		standing: true, usedShared: usedShared,
	}
	m.lastTime = req.Time
	m.hasTime = true
	return BuyResult{TicketID: req.TicketID, Standing: true, UsedSharedQuota: usedShared}, 0, "standing ticket"
}

func (m *naiveModel) refund(req RefundRequest) (ErrorCode, string) {
	if req.TrainID != "T" || req.TicketID == "" {
		return ErrInvalidArgument, "invalid argument"
	}
	if m.hasTime && req.Time < m.lastTime {
		return ErrClockRewind, "clock rewind"
	}
	issued := m.tickets[req.TicketID]
	if issued == nil {
		return ErrTicketNotFound, "ticket not found"
	}
	if issued.refunded {
		return ErrTicketRefunded, "ticket refunded"
	}
	if req.Time >= m.departs[issued.origin] {
		return ErrDeparted, "departed"
	}
	m.merge(req.Time)
	if issued.standing {
		for edge := issued.origin; edge < issued.destination; edge++ {
			m.stand[edge]--
		}
	}
	if issued.usedShared || m.quota[issued.origin][issued.destination] == 0 && m.departs[issued.origin]-m.advance <= req.Time {
		m.shared++
	} else {
		m.quota[issued.origin][issued.destination]++
	}
	issued.refunded = true
	m.lastTime = req.Time
	m.hasTime = true
	return 0, "refund released seat/standing and original quota source"
}

type modelState struct {
	quota  [][]int
	shared int
	stand  []int
	active map[string]modelTicket
}

func (m *naiveModel) state() modelState {
	quota := make([][]int, len(m.quota))
	for origin := range m.quota {
		quota[origin] = append([]int(nil), m.quota[origin]...)
	}
	active := make(map[string]modelTicket)
	for id, issued := range m.tickets {
		if !issued.refunded {
			active[id] = *issued
		}
	}
	return modelState{quota: quota, shared: m.shared, stand: append([]int(nil), m.stand...), active: active}
}

func actualState(tr *train) modelState {
	quota := make([][]int, len(tr.quota))
	for origin := range tr.quota {
		quota[origin] = append([]int(nil), tr.quota[origin]...)
	}
	active := make(map[string]modelTicket)
	for id, issued := range tr.tickets {
		if !issued.refunded {
			copyTicket := modelTicket{
				passenger: issued.passenger, origin: issued.origin, destination: issued.destination,
				standing: issued.standing, usedShared: issued.usedShared,
			}
			if !issued.standing {
				seat := tr.seats[issued.seatIndex]
				copyTicket.seat = &seat
			}
			active[id] = copyTicket
		}
	}
	return modelState{quota: quota, shared: tr.sharedQuota, stand: append([]int(nil), tr.standUsed...), active: active}
}

func canonicalTickets(tickets map[string]modelTicket) string {
	ids := make([]string, 0, len(tickets))
	for id := range tickets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var builder strings.Builder
	for _, id := range ids {
		issued := tickets[id]
		seat := "none"
		if issued.seat != nil {
			seat = fmt.Sprintf("%d-%d", issued.seat.Car, issued.seat.No)
		}
		fmt.Fprintf(&builder, "%s:%s:%d:%d:%t:%s:%t;", id, issued.passenger, issued.origin, issued.destination, issued.standing, seat, issued.usedShared)
	}
	return builder.String()
}

func TestRandomOperationsAgainstNaiveModel(t *testing.T) {
	const iterations = 400
	rng := rand.New(rand.NewSource(20261006))
	for iteration := 0; iteration < 80; iteration++ {
		cfg := TrainConfig{
			ID: "T", Stations: []string{"A", "B", "C", "D", "E"},
			Departs:        []int64{100, 200, 300, 400, 500},
			Seats:          []Seat{{1, 2}, {2, 1}, {1, 1}},
			Quota:          quotaMatrix(5, rng.Intn(3)),
			SharedQuota:    rng.Intn(5),
			AdvanceSeconds: int64(rng.Intn(40)),
			StandingRatio:  []float64{0, 0.34, 0.67, 1}[rng.Intn(4)],
		}
		sys := NewSystem()
		if err := sys.AddTrain(cfg); err != nil {
			t.Fatal(err)
		}
		model := newNaiveModel(cfg)
		var log strings.Builder

		for step := 0; step < iterations; step++ {
			now := int64(rng.Intn(520))
			origin := rng.Intn(4)
			destination := origin + 1 + rng.Intn(4-origin)
			ticketID := fmt.Sprintf("t%d", rng.Intn(90))
			passenger := fmt.Sprintf("p%d", rng.Intn(5))
			if rng.Intn(2) == 0 {
				req := BuyRequest{Time: now, TrainID: "T", TicketID: ticketID, Passenger: passenger, Origin: origin, Destination: destination, AcceptStanding: rng.Intn(2) == 0}
				actualResult, actualErr := sys.Buy(req)
				modelResult, modelCode, reason := model.buy(req)
				fmt.Fprintf(&log, "step=%d BUY req=%+v => actual=%+v/%v model=%+v/%d reason=%s\n", step, req, actualResult, actualErr, modelResult, modelCode, reason)
				if ErrorCodeOf(actualErr) != modelCode {
					t.Fatalf("iteration %d step %d buy code mismatch\n%s", iteration, step, log.String())
				}
				if actualErr == nil {
					if actualResult.TicketID != modelResult.TicketID || actualResult.Standing != modelResult.Standing || actualResult.UsedSharedQuota != modelResult.UsedSharedQuota || !reflect.DeepEqual(actualResult.Seat, modelResult.Seat) {
						t.Fatalf("iteration %d step %d buy result mismatch\nactual=%+v model=%+v\n%s", iteration, step, actualResult, modelResult, log.String())
					}
				}
			} else {
				req := RefundRequest{Time: now, TrainID: "T", TicketID: ticketID}
				actualErr := sys.Refund(req)
				modelCode, reason := model.refund(req)
				fmt.Fprintf(&log, "step=%d REFUND req=%+v => actual=%v model=%d reason=%s\n", step, req, actualErr, modelCode, reason)
				if ErrorCodeOf(actualErr) != modelCode {
					t.Fatalf("iteration %d step %d refund code mismatch\n%s", iteration, step, log.String())
				}
			}

			actual := actualState(sys.trains["T"])
			expected := model.state()
			if !reflect.DeepEqual(actual.quota, expected.quota) || actual.shared != expected.shared || !reflect.DeepEqual(actual.stand, expected.stand) || canonicalTickets(actual.active) != canonicalTickets(expected.active) {
				t.Fatalf("iteration %d state mismatch after step %d\nactual quota=%v shared=%d stand=%v tickets=%s\nmodel quota=%v shared=%d stand=%v tickets=%s\nlog:\n%s",
					iteration, step, actual.quota, actual.shared, actual.stand, canonicalTickets(actual.active),
					expected.quota, expected.shared, expected.stand, canonicalTickets(expected.active), log.String())
			}
		}
		t.Logf("random sequence iteration=%d\n%s", iteration, log.String())
	}
}
