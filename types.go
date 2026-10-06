package microgrid

import (
	"errors"
	"fmt"
)

// Action is the per-slot dispatch action kind.
type Action uint8

const (
	Idle Action = iota
	Charge
	Discharge
)

func (a Action) String() string {
	switch a {
	case Idle:
		return "idle"
	case Charge:
		return "charge"
	case Discharge:
		return "discharge"
	default:
		return fmt.Sprintf("invalid(%d)", uint8(a))
	}
}

// Mode is the current operating mode.
type Mode uint8

const (
	GridTied Mode = iota
	Island
)

func (m Mode) String() string {
	switch m {
	case GridTied:
		return "grid"
	case Island:
		return "island"
	default:
		return fmt.Sprintf("invalid(%d)", uint8(m))
	}
}

// SlotPlan is one scheduled slot.
type SlotPlan struct {
	Action Action
	Amount int
}

// Params configures the storage unit.
type Params struct {
	Capacity             int
	SoCLower             int
	SoCUpper             int
	MaxCharge            int
	MaxDischarge         int
	LossNumerator        int
	LossDenominator      int
	MaintenanceThreshold int
	ReserveSlots         int
	Tolerance            int
}

// Reason is the classified rejection category.
type Reason uint8

const (
	ReasonInvalid Reason = iota + 1
	ReasonSlot
	ReasonMaintenance
	ReasonMode
	ReasonBounds
	ReasonReserve
	ReasonNoForecast
)

func (r Reason) String() string {
	switch r {
	case ReasonInvalid:
		return "参数非法"
	case ReasonSlot:
		return "时隙错误"
	case ReasonMaintenance:
		return "维护锁定"
	case ReasonMode:
		return "模式不允许"
	case ReasonBounds:
		return "越界"
	case ReasonReserve:
		return "备用不足"
	case ReasonNoForecast:
		return "预测缺失"
	default:
		return fmt.Sprintf("未知(%d)", uint8(r))
	}
}

// RejectError reports the first failing slot and its reason.
type RejectError struct {
	Reason Reason
	Slot   int
	Detail string
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("reject at slot %d: %s (%s)", e.Slot, e.Reason, e.Detail)
}

func reject(r Reason, slot int, detail string) *RejectError {
	return &RejectError{Reason: r, Slot: slot, Detail: detail}
}

// ErrInvalid is returned for malformed parameters and empty plans.
var ErrInvalid = errors.New("invalid argument")

// Revocation records one suffix-removal of accepted plans.
type Revocation struct {
	From  int
	Cause string
	Slots []int
}

// Outcome is the result of operations that may revoke accepted plans.
type Outcome struct {
	Revoked   []Revocation
	Locked    bool
	Deviation bool
}

// appendRevocation records a non-empty suffix revocation.
func (o *Outcome) appendRevocation(from int, cause string, slots []int) {
	if len(slots) == 0 {
		return
	}
	cp := make([]int, len(slots))
	copy(cp, slots)
	o.Revoked = append(o.Revoked, Revocation{From: from, Cause: cause, Slots: cp})
}
