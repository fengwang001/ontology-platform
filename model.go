package parking

import (
	"sort"
	"sync"
)

type Lot struct {
	mu           sync.RWMutex
	clock        int64
	zones        map[string]*zone
	reservations map[string]*Reservation
	activeRes    map[string]*Reservation
	active       map[string]string
	owners       map[string]*ownershipTree
}

type zone struct {
	config ZoneConfig
	spots  map[int]*Spot
	order  []int
	wait   []*waitEntry
}

func NewLot() *Lot {
	return &Lot{
		zones:        make(map[string]*zone),
		reservations: make(map[string]*Reservation),
		activeRes:    make(map[string]*Reservation),
		active:       make(map[string]string),
		owners:       make(map[string]*ownershipTree),
	}
}

func (l *Lot) AddZone(name string, cfg ZoneConfig, spots []Spot) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if name == "" || cfg.EarlyWindow < 0 || cfg.GraceWindow < 0 || cfg.BaseRatePerMinute < 0 ||
		cfg.ChargingPerMinute < 0 || cfg.OvertimePerMinute < 0 || cfg.NoShowFee < 0 ||
		cfg.OccupationPenalty < 0 || len(spots) == 0 {
		return ErrInvalidArgument
	}
	if _, exists := l.zones[name]; exists {
		return ErrInvalidArgument
	}
	prepared := make(map[int]*Spot, len(spots))
	numbers := make([]int, 0, len(spots))
	for _, spot := range spots {
		if spot.Number <= 0 || spot.Kind != NormalSpot && spot.Kind != ChargingSpot {
			return ErrInvalidArgument
		}
		if _, exists := prepared[spot.Number]; exists {
			return ErrInvalidArgument
		}
		copySpot := spot
		copySpot.Zone = name
		prepared[spot.Number] = &copySpot
		numbers = append(numbers, spot.Number)
		l.owners[spotKey(name, spot.Number)] = newOwnershipTree()
	}
	sort.Ints(numbers)
	l.zones[name] = &zone{config: cfg, spots: prepared, order: numbers}
	return nil
}

func (l *Lot) begin(at int64, expireEqual bool) error {
	if at < l.clock {
		return ErrClockRollback
	}
	l.clock = at
	l.settle(at, expireEqual)
	return nil
}

func (l *Lot) settle(at int64, expireEqual bool) {
	l.settleWaitlists(at)
	l.settleExpirations(at, expireEqual)
	l.extendOvertime(at)
}

func (l *Lot) settleWaitlists(at int64) {
	for _, z := range l.zones {
		kept := z.wait[:0]
		for _, entry := range z.wait {
			if entry.start >= at {
				kept = append(kept, entry)
			}
		}
		z.wait = kept
	}
}

func (l *Lot) settleExpirations(at int64, expireEqual bool) {
	var changed bool
	for _, zoneName := range l.sortedZoneNames() {
		z := l.zones[zoneName]
		deadline := at - z.config.GraceWindow
		var expired []*Reservation
		for _, res := range l.activeRes {
			expiredAtDeadline := res.Start < deadline
			if expireEqual {
				expiredAtDeadline = res.Start <= deadline
			}
			if res.Zone == zoneName && res.Status == StatusReserved && expiredAtDeadline {
				expired = append(expired, res)
			}
		}
		sort.Slice(expired, func(i, j int) bool {
			if expired[i].Start != expired[j].Start {
				return expired[i].Start < expired[j].Start
			}
			return expired[i].ID < expired[j].ID
		})
		for _, res := range expired {
			if res.Status != StatusReserved {
				continue
			}
			res.Status = StatusExpired
			l.assignOwner(res.Zone, res.SpotNumber, res.Start+z.config.GraceWindow, res.End, Owner{})
			delete(l.activeRes, res.ID)
			changed = true
		}
	}
	if changed {
		l.promoteAll(at)
	}
}

func (l *Lot) extendOvertime(at int64) {
	for _, res := range l.activeRes {
		if res.Status == StatusCheckedIn && res.End < at {
			l.assignOwner(res.Zone, res.SpotNumber, res.End, at+1, l.ownerOf(res))
		}
	}
}

func (l *Lot) sortedZoneNames() []string {
	names := make([]string, 0, len(l.zones))
	for name := range l.zones {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
