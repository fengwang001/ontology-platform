package waitlist

import (
	"fmt"
	"strconv"
)

type Priority uint8

const (
	PriorityLow Priority = iota
	PriorityNormal
	PriorityHigh
)

type Status uint8

const (
	StatusWaiting Status = iota
	StatusPending
	StatusConfirmed
	StatusWithdrawn
	StatusExpired
	StatusVoid
)

func (p Priority) Valid() bool { return p <= PriorityHigh }

func (s Status) String() string {
	switch s {
	case StatusWaiting:
		return "waiting"
	case StatusPending:
		return "pending"
	case StatusConfirmed:
		return "confirmed"
	case StatusWithdrawn:
		return "withdrawn"
	case StatusExpired:
		return "expired"
	case StatusVoid:
		return "void"
	default:
		return "invalid(" + strconv.Itoa(int(s)) + ")"
	}
}

type FlightKey struct {
	FlightID string
	Cabin    string
}

type Entry struct {
	ID           int64
	Flight       FlightKey
	Passenger    string
	PartySize    int
	Priority     Priority
	RegisteredAt int64
	Status       Status
	Deadline     int64
}

type Config struct {
	MaxQueueEntries    int
	ConfirmationWindow int64
}

type FlightState struct {
	Key            FlightKey
	Capacity       int
	Confirmed      int
	PendingCount   int
	Canceled       bool
	WaitingOrder   []int64
	Entries        map[int64]Entry
	LastAcceptedAt int64
}

func (s FlightState) Available() int {
	return s.Capacity - s.Confirmed - s.PendingCount
}

type ErrorCode string

const (
	ErrInvalidArgument ErrorCode = "invalid_argument"
	ErrClockRollback   ErrorCode = "clock_rollback"
	ErrFlightNotFound  ErrorCode = "flight_not_found"
	ErrFlightCanceled  ErrorCode = "flight_canceled"
	ErrEntryNotFound   ErrorCode = "entry_not_found"
	ErrInvalidStatus   ErrorCode = "invalid_status"
	ErrDuplicateEntry  ErrorCode = "duplicate_registration"
	ErrQueueFull       ErrorCode = "queue_full"
)

type CallError struct {
	Code   ErrorCode
	Actual Status
	Reason string
}

func (e *CallError) Error() string {
	if e.Reason != "" {
		return string(e.Code) + ": " + e.Reason
	}
	return string(e.Code)
}

func errorf(code ErrorCode, format string, args ...any) error {
	return &CallError{Code: code, Reason: fmt.Sprintf(format, args...)}
}

func statusError(actual Status, format string, args ...any) error {
	return &CallError{Code: ErrInvalidStatus, Actual: actual, Reason: fmt.Sprintf(format, args...)}
}
