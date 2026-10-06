package parking

import (
	"fmt"
	"sort"
	"sync"
)

type Service struct {
	mu         sync.RWMutex
	now        Time
	clockSet   bool
	zones      map[string]ZoneConfig
	spots      map[string]*spotState
	zonesSpots map[string][]*spotState
	orders     map[string]map[string]int
	res        map[string]*Reservation
	waiters    map[string][]*Reservation
	deadline   []*Reservation
	sequence   int
}

type spotState struct {
	definition Spot
	live       *intervalTree
	archive    *intervalTree
}

func NewService(zones []ZoneConfig, spots []Spot) (*Service, error) {
	s := &Service{
		zones:      make(map[string]ZoneConfig),
		spots:      make(map[string]*spotState),
		zonesSpots: make(map[string][]*spotState),
		orders:     make(map[string]map[string]int),
		res:        make(map[string]*Reservation),
		waiters:    make(map[string][]*Reservation),
		deadline:   make([]*Reservation, 0),
	}
	for _, zone := range zones {
		if zone.ID == "" || zone.EarlyWindow <= 0 || zone.GracePeriod <= 0 || zone.RatePerSecond < 0 ||
			zone.OvertimeRatePerMinute < 0 || zone.NoShowFee < 0 || zone.ReassignmentCompensation < 0 {
			return nil, Error{Code: ErrInvalidArgument, Msg: "invalid zone configuration"}
		}
		if _, exists := s.zones[zone.ID]; exists {
			return nil, Error{Code: ErrInvalidArgument, Msg: "duplicate zone"}
		}
		s.zones[zone.ID] = zone
		s.orders[zone.ID] = make(map[string]int)
	}
	seenSpots := make(map[string]bool)
	for index, spot := range spots {
		if spot.ID == "" {
			return nil, Error{Code: ErrInvalidArgument, Msg: "empty spot id"}
		}
		if seenSpots[spot.ID] {
			return nil, Error{Code: ErrInvalidArgument, Msg: "duplicate spot id"}
		}
		zone, ok := s.zones[spot.ZoneID]
		if !ok {
			return nil, Error{Code: ErrInvalidArgument, Msg: "spot references unknown zone"}
		}
		_ = zone
		if spot.Kind != SpotNormal && spot.Kind != SpotCharger {
			return nil, Error{Code: ErrInvalidArgument, Msg: "invalid spot kind"}
		}
		seenSpots[spot.ID] = true
		state := &spotState{definition: spot, live: newIntervalTree(), archive: newIntervalTree()}
		s.spots[spot.ID] = state
		s.zonesSpots[spot.ZoneID] = append(s.zonesSpots[spot.ZoneID], state)
		s.orders[spot.ZoneID][spot.ID] = index
	}
	for zoneID := range s.zones {
		sort.Slice(s.zonesSpots[zoneID], func(i, j int) bool {
			return s.zonesSpots[zoneID][i].definition.ID < s.zonesSpots[zoneID][j].definition.ID
		})
	}
	return s, nil
}

func (s *Service) snapshotNow(now Time) error {
	if s.clockSet && now < s.now {
		return Error{Code: ErrClockRolledBack, Msg: fmt.Sprintf("current=%d requested=%d", s.now, now)}
	}
	s.now = now
	s.clockSet = true
	return nil
}

func (s *Service) checkClock(now Time) error {
	if s.clockSet && now < s.now {
		return Error{Code: ErrClockRolledBack, Msg: fmt.Sprintf("current=%d requested=%d", s.now, now)}
	}
	return nil
}

func (s *Service) nextID(prefix string) string {
	s.sequence++
	return fmt.Sprintf("%s-%06d", prefix, s.sequence)
}

func (s *Service) archiveInterval(spot *spotState, value interval) {
	if value.end <= value.start {
		return
	}
	spot.archive.addRaw(value)
}

func (s *Service) SpotOccupantAt(spotID string, at Time) (SpotOccupancy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	spot, ok := s.spots[spotID]
	if !ok {
		return SpotOccupancy{}, Error{Code: ErrInvalidArgument, Msg: "unknown spot"}
	}
	result := SpotOccupancy{
		SpotID: spotID,
		ZoneID: spot.definition.ZoneID,
		Kind:   spot.definition.Kind,
		At:     at,
	}
	current := spot.live.findAt(at)
	if current == nil {
		current = spot.archive.findAt(at)
	}
	if current == nil {
		return result, nil
	}
	r := s.res[current.reservationID]
	if r == nil {
		return result, nil
	}
	result.Occupied = true
	result.ReservationID = r.ID
	result.Vehicle = r.Vehicle
	result.Status = r.Status
	return result, nil
}

func (s *Service) Reservation(reservationID string) (Reservation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.res[reservationID]
	if !ok {
		return Reservation{}, Error{Code: ErrReservationGone, Msg: reservationID}
	}
	return *r, nil
}

func (s *Service) Fee(reservationID string) (FeeBreakdown, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.res[reservationID]
	if !ok {
		return FeeBreakdown{}, Error{Code: ErrReservationGone, Msg: reservationID}
	}
	var overtimeMinutes int64
	if r.LeftAt > r.End {
		overtimeMinutes = (int64(r.LeftAt-r.End) + 59) / 60
	}
	fee := FeeBreakdown{
		ReservationID:    r.ID,
		Vehicle:          r.Vehicle,
		ZoneID:           r.ZoneID,
		SpotID:           r.SpotID,
		BaseFee:          r.BaseFee,
		OvertimeFee:      r.OvertimeFee,
		NoShowFee:        r.NoShowFee,
		CompensationPaid: r.Compensation,
		Total:            r.BaseFee + r.OvertimeFee + r.NoShowFee + r.Compensation,
		OvertimeStart:    r.End,
		LeftAt:           r.LeftAt,
		OvertimeMinutes:  overtimeMinutes,
	}
	return fee, nil
}
