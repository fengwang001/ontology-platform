package parking

type naiveLease struct {
	plate   string
	spot    int
	start   int
	end     int
	graceTo int
	share   Interval
	has     bool
}

type naiveVehicle struct {
	plate         string
	monthly       bool
	entry         int
	spot          int
	waiting       bool
	demand        int
	vacateStart   int
	deadline      int
	vacateEnd     int
	shareEndEntry int
	shareAtEntry  Interval
	hasShareEntry bool
	enteredShared bool
}

type naiveModel struct {
	spots    int
	cfg      Config
	leases   map[string]*naiveLease
	vehicles map[string]*naiveVehicle
	owners   map[int]string
	occupied map[int]string
	waiting  []string
	lastNow  int
}

type naiveResult struct {
	spot int
	fee  int
	err  error
}

func newNaive(spots int, cfg Config) *naiveModel {
	return &naiveModel{
		spots: spots, cfg: cfg,
		leases: map[string]*naiveLease{}, vehicles: map[string]*naiveVehicle{},
		owners: map[int]string{}, occupied: map[int]string{},
	}
}

func (n *naiveModel) active(lease *naiveLease, now int) bool {
	return now >= lease.start && now < lease.end
}

func (n *naiveModel) canRenew(lease *naiveLease, now int) bool {
	return now >= lease.start && now < lease.graceTo
}

func (n *naiveModel) shareOpen(lease *naiveLease, now int) bool {
	if lease == nil || !lease.has {
		return false
	}
	day := dayStart(now)
	start, end := intervalAt(lease.share, day)
	if now >= start && now < end {
		return true
	}
	start, end = intervalAt(lease.share, day-Day)
	return now >= start && now < end
}

func (n *naiveModel) shareBounds(lease *naiveLease, now int) (int, int, bool) {
	day := dayStart(now)
	start, end := intervalAt(lease.share, day)
	if now >= start && now < end {
		return start, end, true
	}
	start, end = intervalAt(lease.share, day-Day)
	if now >= start && now < end {
		return start, end, true
	}
	return 0, 0, false
}

func (n *naiveModel) advance(now int) {
	for plate, lease := range n.leases {
		if now >= lease.graceTo && n.occupied[lease.spot] == "" {
			delete(n.owners, lease.spot)
			delete(n.leases, plate)
		}
	}
	n.lastNow = now
}

func (n *naiveModel) firstFree(shared bool, now int) int {
	for spot := 0; spot < n.spots; spot++ {
		if n.occupied[spot] != "" {
			continue
		}
		owner := n.owners[spot]
		if !shared && owner == "" {
			return spot
		}
		if shared {
			if lease := n.leases[owner]; owner != "" && lease != nil && n.active(lease, now) && n.shareOpen(lease, now) {
				return spot
			}
		}
	}
	return -1
}

func (n *naiveModel) register(now int, plate string, start, end int) naiveResult {
	if now < 0 || plate == "" || !validLeaseRange(start, end) {
		return naiveResult{err: ErrInvalidArgument}
	}
	if now < n.lastNow {
		return naiveResult{err: ErrClockRolledBack}
	}
	if n.leases[plate] != nil {
		return naiveResult{err: ErrInvalidState}
	}
	n.advance(now)
	spot := n.firstFree(false, now)
	if spot < 0 {
		return naiveResult{err: ErrNoSpace}
	}
	lease := &naiveLease{plate: plate, spot: spot, start: start, end: end, graceTo: end + n.cfg.GraceDays*Day}
	n.leases[plate], n.owners[spot] = lease, plate
	return naiveResult{spot: spot}
}

func (n *naiveModel) renew(now int, plate string, end int) naiveResult {
	if now < 0 || plate == "" || end <= now {
		return naiveResult{err: ErrInvalidArgument}
	}
	if now < n.lastNow {
		return naiveResult{err: ErrClockRolledBack}
	}
	lease := n.leases[plate]
	if lease == nil {
		return naiveResult{err: ErrNotFound}
	}
	if !n.canRenew(lease, now) {
		return naiveResult{err: ErrLease}
	}
	n.advance(now)
	lease.end = end
	lease.graceTo = end + n.cfg.GraceDays*Day
	return naiveResult{}
}

func (n *naiveModel) setShare(now int, plate string, spot int, interval Interval) naiveResult {
	if now < 0 || plate == "" || !validTimeRange(interval.Start, interval.End) {
		return naiveResult{err: ErrInvalidArgument}
	}
	if now < n.lastNow {
		return naiveResult{err: ErrClockRolledBack}
	}
	if spot < 0 || spot >= n.spots {
		return naiveResult{err: ErrNotFound}
	}
	lease := n.leases[plate]
	if lease == nil {
		return naiveResult{err: ErrNotFound}
	}
	if lease.spot != spot {
		return naiveResult{err: ErrInvalidState}
	}
	n.advance(now)
	lease.share, lease.has = interval, true
	return naiveResult{}
}

func (n *naiveModel) park(plate string, spot, now int, monthly bool, shareEnd int) {
	n.occupied[spot] = plate
	n.vehicles[plate] = &naiveVehicle{
		plate: plate, monthly: monthly, entry: now, spot: spot,
		vacateStart: -1, shareEndEntry: shareEnd,
	}
}

func (n *naiveModel) demand(lease *naiveLease, now int) {
	if plate := n.occupied[lease.spot]; plate != "" {
		if vehicle := n.vehicles[plate]; vehicle != nil && !vehicle.monthly && vehicle.vacateStart < 0 {
			vehicle.demand = 1
			vehicle.vacateStart = now
			vehicle.deadline = now + n.cfg.VacateMinutes
		}
	}
}

func (n *naiveModel) monthlyEnter(now int, plate string) naiveResult {
	if now < 0 || plate == "" {
		return naiveResult{err: ErrInvalidArgument}
	}
	if now < n.lastNow {
		return naiveResult{err: ErrClockRolledBack}
	}
	lease := n.leases[plate]
	if lease == nil {
		return naiveResult{err: ErrNotFound}
	}
	if n.vehicles[plate] != nil {
		return naiveResult{err: ErrInvalidState}
	}
	if !n.active(lease, now) {
		return naiveResult{err: ErrLease}
	}
	n.advance(now)
	if n.occupied[lease.spot] == "" {
		n.park(plate, lease.spot, now, true, 0)
		return naiveResult{spot: lease.spot}
	}
	n.demand(lease, now)
	if spot := n.firstFree(false, now); spot >= 0 {
		n.waiting = append(n.waiting, plate)
		n.park(plate, spot, now, true, 0)
		return naiveResult{spot: spot}
	}
	n.vehicles[plate] = &naiveVehicle{plate: plate, monthly: true, entry: now, spot: -1, waiting: true, vacateStart: -1}
	n.waiting = append(n.waiting, plate)
	return naiveResult{spot: -1}
}

func (n *naiveModel) visitorEnter(now int, plate string) naiveResult {
	if now < 0 || plate == "" {
		return naiveResult{err: ErrInvalidArgument}
	}
	if now < n.lastNow {
		return naiveResult{err: ErrClockRolledBack}
	}
	if n.vehicles[plate] != nil {
		return naiveResult{err: ErrInvalidState}
	}
	if lease := n.leases[plate]; lease != nil && n.active(lease, now) {
		return naiveResult{err: ErrLease}
	}
	n.advance(now)
	spot := n.firstFree(false, now)
	if spot < 0 {
		spot = n.firstFree(true, now)
	}
	if spot < 0 {
		return naiveResult{err: ErrNoSpace}
	}
	shareEnd := 0
	shareSnapshot, hasSnapshot := Interval{}, false
	owner := n.owners[spot]
	if owner != "" {
		if lease := n.leases[owner]; lease != nil {
			shareSnapshot, hasSnapshot = lease.share, lease.has
			if _, end, ok := n.shareBounds(lease, now); ok {
				shareEnd = end
			}
		}
	}
	n.park(plate, spot, now, false, shareEnd)
	n.vehicles[plate].shareAtEntry, n.vehicles[plate].hasShareEntry = shareSnapshot, hasSnapshot
	n.vehicles[plate].enteredShared = hasSnapshot
	return naiveResult{spot: spot}
}

func (n *naiveModel) fee(vehicle *naiveVehicle, now int) int {
	if vehicle.monthly || now <= vehicle.entry {
		return 0
	}
	daily := map[int]int{}
	overtime := 0
	for minute := vehicle.entry; minute < now; minute++ {
		over := false
		if vehicle.enteredShared {
			lease := &naiveLease{share: vehicle.shareAtEntry, has: vehicle.hasShareEntry}
			over = !n.shareOpen(lease, minute)
		}
		if vehicle.demand > 0 && minute >= vehicle.deadline {
			over = true
		}
		vacated := vehicle.vacateStart >= 0 && minute >= vehicle.vacateStart && minute < vehicle.deadline
		if vehicle.vacateEnd > 0 && minute >= vehicle.vacateEnd {
			vacated = false
		}
		if over {
			overtime++
		} else if !vacated {
			daily[minute/Day]++
		}
	}
	if minutes := daily[vehicle.entry/Day]; minutes > 0 {
		daily[vehicle.entry/Day] = max(0, minutes-n.cfg.FreeMinutes)
	}
	base := 0
	for _, minutes := range daily {
		fee := ceilUnits(minutes, n.cfg.BillingUnit) * n.cfg.UnitFee
		if n.cfg.DailyCap > 0 && fee > n.cfg.DailyCap {
			fee = n.cfg.DailyCap
		}
		base += fee
	}
	return base + overtime*n.cfg.OverTimeRate
}

func (n *naiveModel) removeWaiting(plate string) {
	for index, value := range n.waiting {
		if value == plate {
			n.waiting = append(n.waiting[:index], n.waiting[index+1:]...)
			return
		}
	}
}

func (n *naiveModel) cancel(plate string, now int) {
	lease := n.leases[plate]
	if lease == nil {
		return
	}
	if visitorPlate := n.occupied[lease.spot]; visitorPlate != "" {
		if vehicle := n.vehicles[visitorPlate]; vehicle != nil && !vehicle.monthly && vehicle.demand > 0 && vehicle.vacateEnd == 0 {
			vehicle.vacateEnd = now
			vehicle.demand = 0
		}
	}
}

func (n *naiveModel) auto(spot, now int) {
	for index, plate := range n.waiting {
		vehicle := n.vehicles[plate]
		lease := n.leases[plate]
		if vehicle == nil || lease == nil || !vehicle.waiting || lease.spot != spot || !n.active(lease, now) {
			continue
		}
		n.waiting = append(n.waiting[:index], n.waiting[index+1:]...)
		n.occupied[spot] = plate
		vehicle.waiting = false
		vehicle.spot = spot
		return
	}
}

func (n *naiveModel) exit(now int, plate string) naiveResult {
	if now < 0 || plate == "" {
		return naiveResult{err: ErrInvalidArgument}
	}
	if now < n.lastNow {
		return naiveResult{err: ErrClockRolledBack}
	}
	vehicle := n.vehicles[plate]
	if vehicle == nil {
		return naiveResult{err: ErrNotFound}
	}
	n.advance(now)
	if vehicle.waiting {
		delete(n.vehicles, plate)
		n.removeWaiting(plate)
		n.cancel(plate, now)
		return naiveResult{spot: -1}
	}
	spot, fee := vehicle.spot, n.fee(vehicle, now)
	delete(n.occupied, spot)
	delete(n.vehicles, plate)
	n.removeWaiting(plate)
	n.cancel(plate, now)
	n.auto(spot, now)
	return naiveResult{spot: spot, fee: fee}
}
