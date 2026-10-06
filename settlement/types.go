// Package settlement implements a time-of-use electricity settlement engine.
// It manages cumulative meter readings for multiple supply points, tariff
// versions with effective times, day types (workday/restday/holiday), and
// produces monthly bills.
//
// Conventions:
//   - instants are Unix seconds (int64); day/month boundaries use UTC+8;
//   - energy is integer Wh; price is cents per kWh; amount is cents;
//   - energy of a reading interval is allocated to slices proportional to
//     duration, rounded down, remainder goes to the last slice;
//   - slice amount = energy * price / 1000, rounded down; bill amount is
//     the sum of slice amounts.
package settlement

import (
	"errors"
	"fmt"
)

// Kind classifies errors. Rejection priority is fixed:
// invalid param > month sealed > out of order > reading rollback >
// insufficient readings > unpriceable.
type Kind int

const (
	KindInvalidParam Kind = iota
	KindMonthSealed
	KindOutOfOrder
	KindReadingRollback
	KindInsufficientReadings
	KindUnpriceable
)

var kindNames = map[Kind]string{
	KindInvalidParam:         "参数非法",
	KindMonthSealed:          "月份已封账",
	KindOutOfOrder:           "时序错误",
	KindReadingRollback:      "读数倒退",
	KindInsufficientReadings: "读数不足",
	KindUnpriceable:          "存在不可计价片",
}

func (k Kind) String() string { return kindNames[k] }

// Error is returned by the engine and carries a Kind for classification.
type Error struct {
	Kind Kind
	Op   string
	Msg  string
}

func (e *Error) Error() string {
	if e.Msg == "" {
		return fmt.Sprintf("%s: %s", e.Op, e.Kind)
	}
	return fmt.Sprintf("%s: %s: %s", e.Op, e.Kind, e.Msg)
}

func fail(k Kind, op, format string, args ...any) *Error {
	return &Error{Kind: k, Op: op, Msg: fmt.Sprintf(format, args...)}
}

// KindOf extracts the Kind of an error returned by the engine.
func KindOf(err error) (k Kind, ok bool) {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind, true
	}
	return 0, false
}

// DayType is the type of a calendar day. Holiday beats Restday beats Workday.
type DayType int

const (
	Workday DayType = iota
	Restday
	Holiday
)

func (d DayType) String() string {
	switch d {
	case Workday:
		return "工作日"
	case Restday:
		return "休息日"
	case Holiday:
		return "节假日"
	}
	return "未知"
}

// Slot is one segment of a daily schedule: [Start, End) in seconds of day
// (left-closed right-open), Price in cents per kWh.
type Slot struct {
	Start int
	End   int
	Price int64
}

// Valid parameter ranges.
const (
	MinTime       int64 = 0          // inclusive lower bound of instants
	MaxTime       int64 = 4102444800 // exclusive upper bound, 2100-01-01T00:00:00Z
	MaxCumulative int64 = 1_000_000_000_000
	MaxPrice      int64 = 1_000_000
	SecondsPerDay       = 86400
	whPerKWh            = 1000
)

// lineKey groups slices of one bill line: same day type, slot and price.
type lineKey struct {
	dayType   DayType
	slotStart int
	slotEnd   int
	price     int64
}

// Line is one bill line: total energy and amount for a slot/price.
type Line struct {
	DayType   DayType
	SlotStart int
	SlotEnd   int
	Price     int64
	Energy    int64
	Amount    int64
}

// Bill is the monthly bill of one supply point. TotalEnergy/TotalAmount
// cover priceable slices only; unpriceable energy is reported separately.
type Bill struct {
	Meter             string
	Year              int
	Month             int
	Lines             []Line
	TotalEnergy       int64
	TotalAmount       int64
	UnpriceableEnergy int64
	UnpriceableSlices int
	Sealed            bool
}
