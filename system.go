package railway

import "sort"

// System serializes every accepted train operation. Callers may invoke its
// methods concurrently; all effects are equivalent to one serial order.
type System struct {
	mu       mutex
	lastTime int64
	hasTime  bool
	trains   map[string]*train
}

func NewSystem() *System {
	return &System{trains: make(map[string]*train)}
}

// AddTrain is itself synchronized and uses time zero for validation ordering.
// Train configurations are normally registered before sale starts.
func (s *System) AddTrain(cfg TrainConfig) error {
	tr, err := newTrain(cfg)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.trains[tr.id]; exists {
		return railwayError(ErrInvalidArgument, "duplicate train id %q", tr.id)
	}
	s.trains[tr.id] = tr
	return nil
}

func (s *System) Buy(req BuyRequest) (BuyResult, error) {
	if !validSegmentFields(req) {
		return BuyResult{}, railwayError(ErrInvalidArgument, "invalid purchase request")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hasTime && req.Time < s.lastTime {
		return BuyResult{}, railwayError(ErrClockRewind, "operation time %d is before %d", req.Time, s.lastTime)
	}
	tr := s.trains[req.TrainID]
	if tr == nil {
		return BuyResult{}, railwayError(ErrTrainNotFound, "train %q does not exist", req.TrainID)
	}
	if !validSegment(req.Origin, req.Destination, len(tr.stations)) {
		return BuyResult{}, railwayError(ErrInvalidArgument, "station indices are outside this train")
	}
	if req.Passenger == "" || req.TicketID == "" {
		return BuyResult{}, railwayError(ErrInvalidArgument, "ticket id and passenger are required")
	}
	if _, exists := tr.tickets[req.TicketID]; exists {
		return BuyResult{}, railwayError(ErrInvalidArgument, "ticket id %q already exists", req.TicketID)
	}
	if req.Time >= tr.departs[req.Origin] {
		return BuyResult{}, railwayError(ErrDeparted, "origin station has already departed")
	}
	if tr.passengerOverlaps(req.Passenger, req.Origin, req.Destination) {
		return BuyResult{}, railwayError(ErrPassengerOverlap, "passenger already holds an intersecting ticket")
	}

	tr.mergeDueOrigins(req.Time)
	usedShared, ok := tr.useQuota(req.Origin, req.Destination)
	if !ok {
		return BuyResult{}, railwayError(ErrQuotaExhausted, "no allocation or shared quota")
	}

	seatIndex := tr.seating.choose(req.Origin, req.Destination)
	if seatIndex >= 0 {
		tr.seating.mark(seatIndex, req.Origin, req.Destination, true)
		issued := tr.addTicket(req, false, seatIndex, usedShared)
		s.advanceTime(req.Time)
		return buyResultFor(tr, issued), nil
	}

	if !req.AcceptStanding || !tr.standingFree(req.Origin, req.Destination) {
		tr.refundQuota(req.Origin, req.Destination, usedShared)
		return BuyResult{}, railwayError(ErrNoSeatAvailable, "no seat or standing capacity")
	}
	tr.addStanding(req.Origin, req.Destination)
	issued := tr.addTicket(req, true, -1, usedShared)
	s.advanceTime(req.Time)
	return buyResultFor(tr, issued), nil
}

func (s *System) Refund(req RefundRequest) error {
	if req.TrainID == "" || req.TicketID == "" {
		return railwayError(ErrInvalidArgument, "train id and ticket id are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hasTime && req.Time < s.lastTime {
		return railwayError(ErrClockRewind, "operation time %d is before %d", req.Time, s.lastTime)
	}
	tr := s.trains[req.TrainID]
	if tr == nil {
		return railwayError(ErrTrainNotFound, "train %q does not exist", req.TrainID)
	}
	issued := tr.tickets[req.TicketID]
	if issued == nil {
		return railwayError(ErrTicketNotFound, "ticket %q does not exist", req.TicketID)
	}
	if issued.refunded {
		return railwayError(ErrTicketRefunded, "ticket %q has already been refunded", req.TicketID)
	}
	if req.Time >= tr.departs[issued.origin] {
		return railwayError(ErrDeparted, "origin station has already departed")
	}

	tr.mergeDueOrigins(req.Time)
	if issued.standing {
		tr.removeStanding(issued.origin, issued.destination)
	} else {
		tr.seating.mark(issued.seatIndex, issued.origin, issued.destination, false)
	}
	tr.refundQuota(issued.origin, issued.destination, issued.usedShared)
	issued.refunded = true
	s.advanceTime(req.Time)
	return nil
}

type RemainingQuota struct {
	Allocation int
	Shared     int
	Total      int
}

// RemainingQuota returns a read-only cutoff view at time now. It does not
// merge state and is O(stations^2) in station count, independent of ticket count.
func (s *System) RemainingQuota(trainID string, origin, destination int, now int64) (RemainingQuota, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tr := s.trains[trainID]
	if tr == nil {
		return RemainingQuota{}, railwayError(ErrTrainNotFound, "train %q does not exist", trainID)
	}
	if !validSegment(origin, destination, len(tr.stations)) {
		return RemainingQuota{}, railwayError(ErrInvalidArgument, "invalid station pair")
	}
	allocation := tr.effectiveAllocation(origin, destination, tr.mergedBefore, now)
	shared := tr.sharedAt(tr.mergedBefore, now)
	return RemainingQuota{
		Allocation: allocation,
		Shared:     shared,
		Total:      allocation + shared,
	}, nil
}

func (s *System) advanceTime(now int64) {
	s.lastTime = now
	s.hasTime = true
}

func validSegmentFields(req BuyRequest) bool {
	return req.Time >= 0 && req.Origin >= 0 && req.Destination > req.Origin
}

func validSegment(origin, destination, stationCount int) bool {
	return origin >= 0 && destination > origin && destination < stationCount
}

func (tr *train) passengerOverlaps(passenger string, origin, destination int) bool {
	for _, issued := range tr.tickets {
		if issued.refunded || issued.passenger != passenger {
			continue
		}
		if origin < issued.destination && issued.origin < destination {
			return true
		}
	}
	return false
}

func (tr *train) addTicket(req BuyRequest, standing bool, seatIndex int, usedShared bool) *ticket {
	issued := &ticket{
		id:          req.TicketID,
		trainID:     req.TrainID,
		passenger:   req.Passenger,
		origin:      req.Origin,
		destination: req.Destination,
		standing:    standing,
		seatIndex:   seatIndex,
		usedShared:  usedShared,
	}
	tr.tickets[issued.id] = issued
	return issued
}

func buyResultFor(tr *train, issued *ticket) BuyResult {
	result := BuyResult{TicketID: issued.id, Standing: issued.standing, UsedSharedQuota: issued.usedShared}
	if !issued.standing {
		seat := tr.seats[issued.seatIndex]
		result.Seat = &seat
	}
	return result
}

// Snapshot is used by tests and deterministic diagnostics, not by sale logic.
type Snapshot struct {
	TrainID         string
	StationCount    int
	SeatCount       int
	SharedQuota     int
	StandingUsed    []int
	TicketCount     int
	ActiveTicketIDs []string
}

func (s *System) Snapshot(trainID string) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tr := s.trains[trainID]
	if tr == nil {
		return Snapshot{}, railwayError(ErrTrainNotFound, "train %q does not exist", trainID)
	}
	ids := make([]string, 0, len(tr.tickets))
	for id, issued := range tr.tickets {
		if !issued.refunded {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return Snapshot{
		TrainID:         tr.id,
		StationCount:    len(tr.stations),
		SeatCount:       len(tr.seats),
		SharedQuota:     tr.sharedQuota,
		StandingUsed:    append([]int(nil), tr.standUsed...),
		TicketCount:     len(ids),
		ActiveTicketIDs: ids,
	}, nil
}
