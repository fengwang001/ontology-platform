package parking

import (
	"fmt"
	"sort"
	"strings"
)

type naiveStatus string

const (
	nvWaiting  naiveStatus = "waiting"
	nvReserved naiveStatus = "reserved"
	nvOccupied naiveStatus = "occupied"
	nvDone     naiveStatus = "done"
	nvCancel   naiveStatus = "cancelled"
	nvExpired  naiveStatus = "expired"
)

type naiveModel struct{}

type naiveReservation struct {
	id, vehicle, zone, spot string
	start, end              int64
	charger                 bool
	status                  naiveStatus
	created, converted      int64
	checkedIn, left         int64
	base, overtime, cancel  Money
	comp                    Money
}

type naiveWorld struct {
	zones    map[string]ZoneConfig
	spots    []Spot
	res      map[string]*naiveReservation
	waiters  map[string][]*naiveReservation
	occupy   map[string]map[int64]string
	now      int64
	sequence int
	logs     []string
}

type naiveAction struct {
	kind    string
	at      int64
	id      string
	vehicle string
	zone    string
	start   int64
	end     int64
	charger bool
}

func newNaiveWorld(zones []ZoneConfig, spots []Spot) *naiveWorld {
	w := &naiveWorld{
		zones:   make(map[string]ZoneConfig),
		res:     make(map[string]*naiveReservation),
		waiters: make(map[string][]*naiveReservation),
		occupy:  make(map[string]map[int64]string),
	}
	for _, zone := range zones {
		w.zones[zone.ID] = zone
	}
	for _, spot := range spots {
		w.spots = append(w.spots, spot)
		w.occupy[spot.ID] = make(map[int64]string)
	}
	return w
}

func (w *naiveWorld) newID() string {
	w.sequence++
	return fmt.Sprintf("R-%06d", w.sequence)
}

func (w *naiveWorld) deadline(r *naiveReservation) int64 {
	base := r.start
	if r.converted > base {
		base = r.converted
	}
	return base + int64(w.zones[r.zone].GracePeriod)
}

func (w *naiveWorld) expire(at int64) {
	var list []*naiveReservation
	for _, r := range w.res {
		if r.status == nvReserved && r.checkedIn == 0 && w.deadline(r) <= at {
			list = append(list, r)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if w.deadline(list[i]) != w.deadline(list[j]) {
			return w.deadline(list[i]) < w.deadline(list[j])
		}
		return list[i].id < list[j].id
	})
	for _, r := range list {
		deadline := w.deadline(r)
		w.removeInterval(r.spot, deadline, r.end, r.id)
		r.status = nvExpired
		w.promote(r.zone, r.start, r.end, at)
	}
}

func (w *naiveWorld) freeSpot(zone string, start, end int64, charger bool, forbidden string) string {
	kinds := []SpotKind{SpotNormal, SpotCharger}
	if charger {
		kinds = []SpotKind{SpotCharger}
	}
	for _, kind := range kinds {
		var candidates []Spot
		for _, spot := range w.spots {
			if spot.ZoneID == zone && spot.Kind == kind && spot.ID != forbidden {
				candidates = append(candidates, spot)
			}
		}
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })
		for _, spot := range candidates {
			if w.canUse(spot.ID, start, end, "") {
				return spot.ID
			}
		}
	}
	return ""
}

func (w *naiveWorld) canUse(spot string, start, end int64, owner string) bool {
	for t := start; t < end; t++ {
		if w.occupy[spot][t] != "" && w.occupy[spot][t] != owner {
			return false
		}
	}
	return true
}

func (w *naiveWorld) addInterval(spot string, start, end int64, id string) {
	for t := start; t < end; t++ {
		w.occupy[spot][t] = id
	}
}

func (w *naiveWorld) removeInterval(spot string, start, end int64, id string) {
	for t := start; t < end; t++ {
		if w.occupy[spot][t] == id {
			delete(w.occupy[spot], t)
		}
	}
}

func (w *naiveWorld) promote(zone string, freeStart, freeEnd, at int64) {
	for index, waiter := range w.waiters[zone] {
		if waiter.status != nvWaiting || waiter.start < freeStart || waiter.end > freeEnd {
			continue
		}
		spot := w.freeSpot(zone, waiter.start, waiter.end, waiter.charger, "")
		if spot == "" {
			continue
		}
		waiter.status, waiter.spot, waiter.converted = nvReserved, spot, at
		waiter.base = w.zones[zone].RatePerSecond * Money(waiter.end-waiter.start)
		w.addInterval(spot, waiter.start, waiter.end, waiter.id)
		w.waiters[zone] = append(w.waiters[zone][:index], w.waiters[zone][index+1:]...)
		return
	}
}

func (w *naiveWorld) apply(action naiveAction) (string, string) {
	if action.at < w.now {
		return "", string(ErrClockRolledBack)
	}
	zone, zoneOK := w.zones[action.zone]
	r, exists := w.res[action.id]
	switch action.kind {
	case "reserve", "wait":
		if action.end <= action.start || action.vehicle == "" {
			return "", string(ErrInvalidArgument)
		}
		if !zoneOK {
			return "", string(ErrZoneNotFound)
		}
		if action.at > action.start {
			return "", string(ErrInvalidArgument)
		}
		if action.id != "" {
			if _, exists := w.res[action.id]; exists {
				return "", string(ErrInvalidArgument)
			}
		}
		w.now = action.at
		w.expire(action.at)
		spot := w.freeSpot(action.zone, action.start, action.end, action.charger, "")
		if action.kind == "reserve" {
			if spot == "" {
				return "", string(ErrNoAvailableSpot)
			}
			id := action.id
			if id == "" {
				id = w.newID()
			}
			made := &naiveReservation{id: id, vehicle: action.vehicle, zone: action.zone, spot: spot, start: action.start, end: action.end, charger: action.charger, status: nvReserved, created: action.at, base: zone.RatePerSecond * Money(action.end-action.start)}
			w.res[id] = made
			w.addInterval(spot, action.start, action.end, id)
			return id, spot
		}
		if spot != "" {
			return "", string(ErrInvalidArgument)
		}
		id := action.id
		if id == "" {
			id = w.newID()
		}
		made := &naiveReservation{id: id, vehicle: action.vehicle, zone: action.zone, start: action.start, end: action.end, charger: action.charger, status: nvWaiting, created: action.at}
		w.res[id] = made
		w.waiters[action.zone] = append(w.waiters[action.zone], made)
		sort.SliceStable(w.waiters[action.zone], func(i, j int) bool {
			if w.waiters[action.zone][i].created != w.waiters[action.zone][j].created {
				return w.waiters[action.zone][i].created < w.waiters[action.zone][j].created
			}
			return w.waiters[action.zone][i].vehicle < w.waiters[action.zone][j].vehicle
		})
		return id, "waiting"
	case "checkin":
		if !exists {
			return "", string(ErrReservationGone)
		}
		if r.status == nvOccupied {
			return "", string(ErrDuplicateCheckIn)
		}
		if r.status != nvReserved && r.status != nvWaiting {
			return "", string(ErrReservationStale)
		}
		for _, other := range w.res {
			if other.id != r.id && other.vehicle == r.vehicle && other.status == nvOccupied {
				return "", string(ErrVehicleCheckedIn)
			}
		}
		zone = w.zones[r.zone]
		windowStart := r.start - int64(zone.EarlyWindow)
		if r.converted > windowStart {
			windowStart = r.converted
		}
		if action.at < windowStart {
			return "", string(ErrEarlyArrival)
		}
		w.now = action.at
		w.expire(action.at - 1)
		if r.status != nvReserved {
			return "", string(ErrReservationStale)
		}
		if action.at > w.deadline(r) {
			return "", string(ErrReservationStale)
		}
		forbidden := ""
		if r.spot != "" && !w.canUse(r.spot, action.at, r.end, r.id) {
			forbidden = r.spot
			w.removeInterval(r.spot, action.at, r.end, r.id)
		}
		spot := r.spot
		if forbidden != "" || spot == "" {
			spot = w.freeSpot(r.zone, action.at, r.end, r.charger, forbidden)
			if spot == "" {
				if forbidden != "" {
					r.spot = ""
				}
				return "", string(ErrNoReassignment)
			}
		}
		if spot != r.spot {
			w.addInterval(spot, action.at, r.end, r.id)
		}
		if spot == r.spot && action.at < r.start {
			w.addInterval(spot, action.at, r.start, r.id)
		}
		r.spot, r.status, r.checkedIn = spot, nvOccupied, action.at
		return r.id, spot
	case "cancel":
		if !exists {
			return "", string(ErrReservationGone)
		}
		if r.status == nvOccupied {
			return "", string(ErrReservationStale)
		}
		if r.status != nvReserved && r.status != nvWaiting {
			return "", string(ErrReservationStale)
		}
		w.now = action.at
		w.expire(action.at)
		if r.status != nvReserved && r.status != nvWaiting {
			return "", string(ErrReservationStale)
		}
		if r.status == nvReserved && action.at >= r.start {
			r.cancel = w.zones[r.zone].NoShowFee
		}
		if r.spot != "" {
			if action.at <= r.start {
				w.removeInterval(r.spot, r.start, r.end, r.id)
			} else {
				w.removeInterval(r.spot, action.at, r.end, r.id)
			}
		}
		if r.status == nvWaiting {
			w.removeWaiter(r)
		}
		r.status = nvCancel
		promotionStart := r.start
		w.promote(r.zone, promotionStart, r.end, action.at)
		return r.id, "cancelled"
	case "leave":
		if !exists {
			return "", string(ErrReservationGone)
		}
		if r.status != nvOccupied {
			return "", string(ErrReservationStale)
		}
		w.now = action.at
		if action.at > r.end {
			minutes := (action.at - r.end + 59) / 60
			r.overtime = Money(minutes) * w.zones[r.zone].OvertimeRatePerMinute
			blockedSet := map[string]bool{}
			for t := r.end; t < action.at; t++ {
				if id := w.occupy[r.spot][t]; id != "" && id != r.id {
					blockedSet[id] = true
				}
			}
			var blocked []string
			for id := range blockedSet {
				blocked = append(blocked, id)
			}
			sort.Strings(blocked)
			for _, nextID := range blocked {
				next := w.res[nextID]
				if next == nil || next.status != nvReserved {
					continue
				}
				spot := w.freeSpot(next.zone, action.at, next.end, next.charger, r.spot)
				w.removeInterval(next.spot, action.at, next.end, next.id)
				if spot != "" {
					next.spot = spot
					w.addInterval(spot, action.at, next.end, next.id)
					w.promote(next.zone, action.at, next.end, action.at)
				} else {
					next.spot = ""
				}
				r.comp = w.zones[r.zone].ReassignmentCompensation
			}
			w.addInterval(r.spot, r.end, action.at, r.id)
		}
		if action.at < r.end {
			w.removeInterval(r.spot, action.at, r.end, r.id)
		}
		r.status, r.left = nvDone, action.at
		return r.id, r.spot
	}
	return "", string(ErrInvalidArgument)
}

func (w *naiveWorld) removeWaiter(target *naiveReservation) {
	queue := w.waiters[target.zone]
	for i, waiter := range queue {
		if waiter == target {
			w.waiters[target.zone] = append(queue[:i], queue[i+1:]...)
			return
		}
	}
}

func (w *naiveWorld) occ(spot string, at int64) (string, string) {
	id := w.occupy[spot][at]
	if id == "" {
		return "", ""
	}
	return id, w.res[id].vehicle
}

func (w *naiveWorld) fee(id string) (Money, Money, Money, Money) {
	r := w.res[id]
	return r.base, r.overtime, r.cancel, r.comp
}

func (a naiveAction) String() string {
	return fmt.Sprintf("kind=%s at=%d id=%s vehicle=%s zone=%s start=%d end=%d charger=%t", a.kind, a.at, a.id, a.vehicle, a.zone, a.start, a.end, a.charger)
}

func summarizeNaive(w *naiveWorld) string {
	var values []string
	for _, r := range w.res {
		values = append(values, fmt.Sprintf("%s:%s spot=%s base=%d ot=%d cancel=%d comp=%d", r.id, r.status, r.spot, r.base, r.overtime, r.cancel, r.comp))
	}
	sort.Strings(values)
	return strings.Join(values, "|")
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
