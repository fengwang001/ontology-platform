package tolling

import (
	"time"
)

type ErrorCode string

const (
	InvalidArgument ErrorCode = "invalid_argument"
	ClockMovedBack  ErrorCode = "clock_moved_back"
	GateNotFound    ErrorCode = "gate_not_found"
	VehicleNotFound ErrorCode = "vehicle_not_found"
	TripNotFound    ErrorCode = "trip_not_found"
	TripSettled     ErrorCode = "trip_settled"
	TripNotSettled  ErrorCode = "trip_not_settled"
	PathUnreachable ErrorCode = "path_unreachable"
	MissingEntry    ErrorCode = "missing_entry"
)

type ServiceError struct {
	Code ErrorCode
	Msg  string
}

func (e *ServiceError) Error() string {
	if e == nil || e.Code == "" {
		return ""
	}
	if e.Msg == "" {
		return string(e.Code)
	}
	return string(e.Code) + ": " + e.Msg
}

func errorCode(err error) ErrorCode {
	var se *ServiceError
	if asError(err, &se) {
		return se.Code
	}
	return ""
}

type Money int64

type Config struct {
	DuplicateWindow time.Duration
	LateWindow      time.Duration
	MonthlyCap      Money
	TimeZone        *time.Location
}

type Edge struct {
	From     string
	To       string
	Distance float64
	Rates    map[string]Money
}

type GateRecord struct {
	VehicleID  string
	TripID     string
	GateID     string
	Kind       RecordKind
	RecordedAt time.Time
	ReceivedAt time.Time
}

type RecordKind int

const (
	Intermediate RecordKind = iota
	Entry
	Exit
)

type VehicleClassChange struct {
	Class       string
	EffectiveAt time.Time
}

type Adjustment struct {
	ID            int
	At            time.Time
	Kind          AdjustmentKind
	RawDelta      Money
	Amount        Money
	CashCharge    Money
	CashRefund    Money
	RefundBlocked Money
	CappedBefore  Money
	CappedAfter   Money
	Reason        string
}

type AdjustmentKind int

const (
	InitialCharge AdjustmentKind = iota
	Supplementary
	Refund
	NoAdjustment
)

type TripView struct {
	TripID            string
	VehicleID         string
	Status            TripStatus
	EntryGate         string
	ExitGate          string
	EntryAt           time.Time
	ExitAt            time.Time
	Path              []string
	RawAmount         Money
	Collected         Money
	Pending           Money
	Refunded          Money
	CappedUncollected Money
	Adjustments       []Adjustment
	CurrentMonth      string
}

type RecordAudit struct {
	Record      GateRecord
	Duplicate   bool
	Orphan      bool
	ExpiredLate bool
	Effective   bool
}

type MonthView struct {
	VehicleID      string
	Month          string
	Collected      Money
	CappedCharges  Money
	BlockedRefunds Money
}

type TripStatus int

const (
	TripOpen TripStatus = iota
	TripSettledStatus
	TripFailed
	TripClosed
)

type OperationResult struct {
	Accepted   bool
	Duplicate  bool
	Orphan     bool
	TripID     string
	Path       []string
	RawAmount  Money
	Collected  Money
	Adjustment *Adjustment
	Code       ErrorCode
}
