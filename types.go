package congestion

import (
	"errors"
	"time"
)

var (
	ErrInvalidArgument      = errors.New("invalid argument")
	ErrClockMovedBack       = errors.New("clock moved back")
	ErrZoneNotFound         = errors.New("zone not found")
	ErrVehicleNotFound      = errors.New("vehicle not found")
	ErrInvalidNesting       = errors.New("invalid zone nesting")
	ErrEntitlementOverlap   = errors.New("entitlement interval overlaps")
	ErrRetroactiveWindow    = errors.New("outside retroactive window")
	ErrDisputeAlreadyExists = errors.New("dispute already exists")
	ErrDisputeNotFound      = errors.New("dispute not found")
)

type Rect struct {
	MinLat float64
	MinLng float64
	MaxLat float64
	MaxLng float64
}

type TimeOfDay struct {
	Hour   int
	Minute int
}

type ZoneConfig struct {
	ID       string
	ParentID string
	Bounds   Rect
	Fee      int64
	Start    TimeOfDay
	End      TimeOfDay
}

type EntitlementKind int

const (
	Resident EntitlementKind = iota + 1
	Disabled
	NewEnergy
)

type Entitlement struct {
	Kind             EntitlementKind
	ZoneID           string
	Start            time.Time
	End              time.Time
	DiscountBasisPts int64
	RegisteredAt     time.Time
}

type Entry struct {
	VehicleID string
	Plate     string
	ZoneID    string
	At        time.Time
}

type Adjustment struct {
	ID        int64
	VehicleID string
	Day       string
	Reason    string
	At        time.Time
	Before    int64
	After     int64
	Delta     int64
	Evidence  []string
}

type DayReport struct {
	VehicleID   string
	Day         string
	Payable     int64
	Adjustments []Adjustment
	Frozen      bool
	Evidence    []string
}

type Config struct {
	Location         *time.Location
	DailyCap         int64
	MinorUnitDivisor int64
	RetroactiveDays  int
}

type dayLedger struct {
	payable      int64
	adjustments  []Adjustment
	evidence     []string
	frozen       bool
	frozenAmount int64
	initialized  bool
}

func (l *dayLedger) setEvidence(lines []string) {
	l.evidence = append(l.evidence[:0], lines...)
}

func (l *dayLedger) evidenceLines() []string {
	if l.evidence == nil {
		return []string{}
	}
	return l.evidence
}
