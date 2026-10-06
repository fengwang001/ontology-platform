package parking

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

const diffHorizon = 180
const naiveHorizon = 360

type naiveReservation struct {
	id, vehicle string
	spot        int
	start, end  int64
	charge      bool
	status      ReservationStatus
	checkIn     int64
	depart      int64
	created     int64
	penalty     int64
	award       int64
}

type naiveWait struct {
	id, vehicle string
	start, end  int64
	charge      bool
	created     int64
}

type naiveLot struct {
	cfg       ZoneConfig
	spots     map[int]SpotKind
	order     []int
	res       map[string]*naiveReservation
	wait      []*naiveWait
	clock     int64
	ownerGrid [naiveHorizon][3]string
	active    map[string]string
}

func newNaive(cfg ZoneConfig, spots []Spot) *naiveLot {
	n := &naiveLot{cfg: cfg, spots: map[int]SpotKind{}, res: map[string]*naiveReservation{}, active: map[string]string{}}
	for _, spot := range spots {
		n.spots[spot.Number] = spot.Kind
		n.order = append(n.order, spot.Number)
	}
	sort.Ints(n.order)
	return n
}

func spotIndex(number int) int {
	if number == 1 {
		return 0
	}
	if number == 2 {
		return 1
	}
	return 2
}

func (n *naiveLot) setOwner(from, to int64, spot int, id string) {
	for second := int(from); second < int(to) && second < naiveHorizon; second++ {
		if second >= 0 {
			value := "#"
			if id != "" {
				value = id
			}
			n.ownerGrid[second][spotIndex(spot)] = value
		}
	}
}

func (n *naiveLot) free(spot int, from, to int64) bool {
	for second := int(from); second < int(to); second++ {
		if second < 0 {
			return false
		}
		if second >= naiveHorizon {
			return true
		}
		value := n.ownerGrid[second][spotIndex(spot)]
		if value != "" && value != "#" {
			return false
		}
	}
	return true
}

func (n *naiveLot) choose(start, end int64, charge bool) (int, bool) {
	kinds := []SpotKind{NormalSpot, ChargingSpot}
	if charge {
		kinds = []SpotKind{ChargingSpot}
	}
	for _, kind := range kinds {
		for _, number := range n.order {
			if n.spots[number] == kind && n.free(number, start, end) {
				return number, true
			}
		}
	}
	return 0, false
}

func (n *naiveLot) settle(at int64, equalExpiry bool) {
	n.clock = at
	kept := n.wait[:0]
	for _, entry := range n.wait {
		if entry.start >= at {
			kept = append(kept, entry)
		}
	}
	n.wait = kept
	var expired []*naiveReservation
	for _, res := range n.res {
		if res.status == StatusReserved {
			deadline := res.start + n.cfg.GraceWindow
			if at > deadline || equalExpiry && at == deadline {
				expired = append(expired, res)
			}
		}
	}
	sort.Slice(expired, func(i, j int) bool {
		if expired[i].start != expired[j].start {
			return expired[i].start < expired[j].start
		}
		return expired[i].id < expired[j].id
	})
	for _, res := range expired {
		res.status = StatusExpired
		n.setOwner(res.start+n.cfg.GraceWindow, res.end, res.spot, "")
	}
	if len(expired) > 0 {
		n.promote(at)
	}
	for _, res := range n.res {
		if res.status == StatusCheckedIn && res.end < at {
			n.setOwner(res.end, at+1, res.spot, res.id)
		}
	}
}

func (n *naiveLot) reserve(id, vehicle string, start, end int64, charge bool, at int64) (int, error) {
	if id == "" || vehicle == "" || start < 0 || at < 0 || end <= start || start < at {
		return 0, ErrInvalidArgument
	}
	if at < n.clock {
		return 0, ErrClockRollback
	}
	n.settle(at, true)
	if _, exists := n.res[id]; exists {
		return 0, ErrInvalidArgument
	}
	spot, ok := n.choose(start, end, charge)
	if !ok {
		return 0, ErrNoAvailableSpot
	}
	n.res[id] = &naiveReservation{id: id, vehicle: vehicle, spot: spot, start: start, end: end,
		charge: charge, status: StatusReserved, created: at}
	n.setOwner(start, end, spot, id)
	return spot, nil
}

func (n *naiveLot) waitlist(id, vehicle string, start, end int64, charge bool, at int64) error {
	if id == "" || vehicle == "" || start < 0 || at < 0 || end <= start || start < at {
		return ErrInvalidArgument
	}
	if at < n.clock {
		return ErrClockRollback
	}
	n.settle(at, true)
	if _, exists := n.res[id]; exists {
		return ErrInvalidArgument
	}
	n.wait = append(n.wait, &naiveWait{id: id, vehicle: vehicle, start: start, end: end, charge: charge, created: at})
	n.promote(at)
	return nil
}

func (n *naiveLot) promote(at int64) {
	for index := 0; index < len(n.wait); index++ {
		entry := n.wait[index]
		start := entry.start
		if start < at {
			start = at
		}
		if _, ok := n.choose(start, entry.end, entry.charge); ok {
			n.wait = append(n.wait[:index], n.wait[index+1:]...)
			index--
			spot, _ := n.choose(start, entry.end, entry.charge)
			res := &naiveReservation{id: entry.id, vehicle: entry.vehicle, spot: spot, start: start,
				end: entry.end, charge: entry.charge, status: StatusReserved, created: at}
			n.res[res.id] = res
			n.setOwner(start, res.end, spot, res.id)
		}
	}
}

func (n *naiveLot) checkedHolder(spot int, self string) *naiveReservation {
	var candidates []*naiveReservation
	for _, res := range n.res {
		if res.id != self && res.spot == spot && res.status == StatusCheckedIn {
			candidates = append(candidates, res)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].id < candidates[j].id })
	return candidates[0]
}

func (n *naiveLot) reassign(from, to int64, charge bool, occupied int) (int, bool) {
	kinds := []SpotKind{NormalSpot, ChargingSpot}
	if charge {
		kinds = []SpotKind{ChargingSpot}
	}
	for _, kind := range kinds {
		for _, number := range n.order {
			if number != occupied && n.spots[number] == kind && n.free(number, from, to) {
				return number, true
			}
		}
	}
	return 0, false
}

func (n *naiveLot) checkIn(id string, at int64) (int, error) {
	if id == "" || at < 0 {
		return 0, ErrInvalidArgument
	}
	if at < n.clock {
		return 0, ErrClockRollback
	}
	n.settle(at, false)
	res, ok := n.res[id]
	if !ok {
		return 0, ErrReservationGone
	}
	if res.status == StatusCanceled || res.status == StatusExpired || res.status == StatusDeparted ||
		at > res.start+n.cfg.GraceWindow {
		return 0, ErrReservationDead
	}
	if at < res.start-n.cfg.EarlyWindow {
		return 0, ErrEarlyArrival
	}
	if res.status == StatusCheckedIn {
		return 0, ErrDuplicateCheckIn
	}
	if activeID, exists := n.active[res.vehicle]; exists && activeID != res.id {
		return 0, ErrVehicleOccupied
	}
	trigger := at
	if trigger < res.start {
		trigger = res.start
	}
	if holder := n.checkedHolder(res.spot, res.id); holder != nil && holder.end <= trigger {
		spot, found := n.reassign(trigger, res.end, res.charge, res.spot)
		if !found {
			return 0, ErrNoReassignment
		}
		holder.penalty += n.cfg.OccupationPenalty
		res.award += n.cfg.OccupationPenalty
		n.setOwner(trigger, res.end, res.spot, holder.id)
		res.spot = spot
		n.setOwner(trigger, res.end, spot, res.id)
	}
	res.status = StatusCheckedIn
	res.checkIn = at
	n.active[res.vehicle] = res.id
	n.setOwner(at, res.end, res.spot, res.id)
	return res.spot, nil
}

func (n *naiveLot) cancel(id string, at int64) error {
	if id == "" || at < 0 {
		return ErrInvalidArgument
	}
	if at < n.clock {
		return ErrClockRollback
	}
	n.settle(at, true)
	res, ok := n.res[id]
	if !ok {
		return ErrReservationGone
	}
	if res.status != StatusReserved {
		return ErrReservationDead
	}
	res.status = StatusCanceled
	if at >= res.start {
		res.penalty += n.cfg.NoShowFee
	}
	n.setOwner(res.start, res.end, res.spot, "")
	n.promote(at)
	return nil
}

func (n *naiveLot) depart(id string, at int64) error {
	if id == "" || at < 0 {
		return ErrInvalidArgument
	}
	if at < n.clock {
		return ErrClockRollback
	}
	n.settle(at, true)
	res, ok := n.res[id]
	if !ok {
		return ErrReservationGone
	}
	if res.status != StatusCheckedIn || at < res.checkIn {
		return ErrReservationDead
	}
	res.status = StatusDeparted
	res.depart = at
	delete(n.active, res.vehicle)
	n.setOwner(at, res.end, res.spot, "")
	if at <= res.end {
		n.promote(at)
	}
	return nil
}

func (n *naiveLot) fee(id string) (FeeDetail, bool) {
	res, ok := n.res[id]
	if !ok {
		return FeeDetail{}, false
	}
	plain := &Reservation{ID: res.id, Start: res.start, End: res.end, NeedCharge: res.charge,
		Status: res.status, CheckInAt: res.checkIn, DepartAt: res.depart, Penalty: res.penalty, Award: res.award}
	return feeFor(plain, n.cfg), true
}

func (n *naiveLot) owner(spot int, at int64) Owner {
	id := n.ownerGrid[at][spotIndex(spot)]
	if id == "" || id == "#" {
		return Owner{}
	}
	return Owner{ReservationID: id, Vehicle: n.res[id].vehicle}
}

type diffOp struct {
	name           string
	id, vehicle    string
	start, end, at int64
	charge         bool
}

func compareErrors(got, want error) bool {
	return fmt.Sprint(got) == fmt.Sprint(want)
}

func TestRandomDifferentialModel(t *testing.T) {
	for seed := int64(1); seed <= 30; seed++ {
		rng := rand.New(rand.NewSource(seed))
		cfg := testConfig()
		lot := NewLot()
		if err := lot.AddZone("A", cfg, testSpots(1, 2, 10)); err != nil {
			t.Fatal(err)
		}
		naive := newNaive(cfg, testSpots(1, 2, 10))
		ids := []string{"a", "b", "c", "d", "e"}
		vehicles := []string{"va", "vb", "vc"}
		t.Logf("seed=%d input=init output=ok reason=2-normal-1-charging", seed)
		for step := 0; step < 80; step++ {
			at := int64(rng.Intn(diffHorizon + 4))
			id := ids[rng.Intn(len(ids))]
			vehicle := vehicles[rng.Intn(len(vehicles))]
			start := int64(rng.Intn(diffHorizon))
			if start < at || rng.Intn(3) == 0 {
				start = at
			}
			end := start + int64(1+rng.Intn(120))
			op := diffOp{name: "reserve", id: id, vehicle: vehicle, start: start, end: end,
				charge: rng.Intn(4) == 0, at: at}
			var gotSpot, wantSpot int
			var gotErr, wantErr error
			switch rng.Intn(5) {
			case 0:
				var result ReserveResult
				result, gotErr = lot.Reserve("A", vehicle, id, start, end, op.charge, at)
				gotSpot = result.SpotNumber
				wantSpot, wantErr = naive.reserve(id, vehicle, start, end, op.charge, at)
			case 1:
				op.name = "waitlist"
				gotErr = lot.RegisterWaitlist("A", vehicle, id, start, end, op.charge, at)
				wantErr = naive.waitlist(id, vehicle, start, end, op.charge, at)
			case 2:
				op.name = "checkin"
				gotSpot, gotErr = lot.CheckIn(id, at)
				wantSpot, wantErr = naive.checkIn(id, at)
			case 3:
				op.name = "cancel"
				gotErr = lot.Cancel(id, at)
				wantErr = naive.cancel(id, at)
			default:
				op.name = "depart"
				gotErr = lot.Depart(id, at)
				wantErr = naive.depart(id, at)
			}
			t.Logf("seed=%d step=%d input=%s(id=%s,vehicle=%s,start=%d,end=%d,charge=%t,at=%d) output=(spot=%d,err=%v) reason=%v",
				seed, step, op.name, op.id, op.vehicle, op.start, op.end, op.charge, op.at, gotSpot, gotErr, gotErr)
			if gotSpot != wantSpot || !compareErrors(gotErr, wantErr) {
				t.Fatalf("seed=%d step=%d op=%+v got=(%d,%v) want=(%d,%v)", seed, step, op, gotSpot, gotErr, wantSpot, wantErr)
			}
			for _, id := range ids {
				gotFee, gotErr := lot.Fee(id)
				wantFee, exists := naive.fee(id)
				if gotErr != nil && exists || gotErr == nil && !exists {
					t.Fatalf("fee existence for %s: got=%v exists=%v", id, gotErr, exists)
				}
				if exists && gotFee != wantFee {
					t.Fatalf("fee %s: got=%+v want=%+v", id, gotFee, wantFee)
				}
			}
			for second := int64(0); second < diffHorizon; second++ {
				for _, spot := range []int{1, 2, 10} {
					gotOwner, err := lot.SpotOwner("A", spot, second)
					if err != nil {
						t.Fatal(err)
					}
					wantOwner := naive.owner(spot, second)
					if gotOwner != wantOwner {
						t.Fatalf("seed=%d after step=%d owner zone=A spot=%d second=%d got=%+v want=%+v",
							seed, step, spot, second, gotOwner, wantOwner)
					}
				}
			}
		}
	}
}
