// Package toll implements a highway gantry tolling service: path
// reconstruction from out-of-order and incomplete gantry records,
// settlement, late-record back-charges and refunds, duplicate
// suppression, vehicle-class changes and monthly caps.
package toll

import (
	"errors"
	"time"
)

// Money is an exact integer amount in the smallest currency unit.
type Money int64

// VehicleClass identifies a vehicle rating category (e.g. car, truck).
type VehicleClass string

// Distinguishable error kinds, reported in the fixed precedence order
// listed here (earliest kind wins when several apply).
var (
	ErrInvalidParam     = errors.New("toll: invalid parameter")
	ErrClockRollback    = errors.New("toll: clock rollback")
	ErrGantryNotFound   = errors.New("toll: gantry does not exist")
	ErrVehicleNotFound  = errors.New("toll: vehicle does not exist")
	ErrTripNotFound     = errors.New("toll: trip does not exist")
	ErrTripSettled      = errors.New("toll: trip already settled")
	ErrTripNotSettled   = errors.New("toll: trip not settled")
	ErrPathUnreachable  = errors.New("toll: path unreachable")
	ErrExitWithoutEntry = errors.New("toll: exit without entry record")
)

// Config carries the service-wide tunables.
type Config struct {
	// BackchargeWindow is the deadline after the exit time within which
	// a late record still triggers recomputation.
	BackchargeWindow time.Duration
	// DedupWindow: records of one vehicle at one gantry closer than this
	// window are duplicates; exactly the window means independent records.
	DedupWindow time.Duration
	// MonthlyCap caps the actually collected amount per vehicle per
	// natural month (split in Location).
	MonthlyCap Money
	// Location is the time zone used for natural-month boundaries.
	Location *time.Location
}

// TripState is the lifecycle state of a trip.
type TripState int

const (
	TripOpen     TripState = iota // entry seen, no exit yet
	TripFailed                    // exit recorded but path unreachable
	TripSettled                   // exit settled
	TripClosed                    // manually closed without settlement
)

func (s TripState) String() string {
	switch s {
	case TripOpen:
		return "open"
	case TripFailed:
		return "failed"
	case TripSettled:
		return "settled"
	case TripClosed:
		return "closed"
	}
	return "unknown"
}

// AdjKind classifies a ledger adjustment.
type AdjKind int

const (
	AdjSettlement AdjKind = iota
	AdjBackcharge
	AdjRefund
)

func (k AdjKind) String() string {
	switch k {
	case AdjSettlement:
		return "settlement"
	case AdjBackcharge:
		return "backcharge"
	case AdjRefund:
		return "refund"
	}
	return "unknown"
}

// Adjustment is one ledger entry of a trip.
type Adjustment struct {
	Seq       int64     `json:"seq"`
	At        time.Time `json:"at"`
	Kind      AdjKind   `json:"kind"`
	Amount    Money     `json:"amount"`
	Fee       Money     `json:"fee"`       // raw inferred fee after this entry
	Collected Money     `json:"collected"` // trip collected after this entry
	Reason    string    `json:"reason"`
}

// TripView is the query result for one trip.
type TripView struct {
	ID          string       `json:"id"`
	VehicleID   string       `json:"vehicleId"`
	State       TripState    `json:"state"`
	Path        []string     `json:"path"`
	Fee         Money        `json:"fee"`
	Collected   Money        `json:"collected"`
	Pending     Money        `json:"pending"` // owed but blocked by the monthly cap
	Refunded    Money        `json:"refunded"`
	Month       string       `json:"month"`
	Adjustments []Adjustment `json:"adjustments,omitempty"`
}

// MonthView is the per-vehicle per-month aggregate.
type MonthView struct {
	Month            string `json:"month"`
	Cap              Money  `json:"cap"`
	Collected        Money  `json:"collected"`
	Pending          Money  `json:"pending"`          // cap-blocked, still owed
	Refunded         Money  `json:"refunded"`         // total refunded this month
	UnrefundedExcess Money  `json:"unrefundedExcess"` // refund suppressed by the >=0 floor
}

// monthKey returns the natural-month key ("2006-01") of t in loc.
func monthKey(t time.Time, loc *time.Location) string {
	return t.In(loc).Format("2006-01")
}
