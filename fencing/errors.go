package fencing

import (
	"errors"
	"fmt"
)

// ErrKind 标识可程序化区分的错误类别。
type ErrKind int

const (
	ErrKindInvalidParam ErrKind = iota
	ErrKindClockRollback
	ErrKindMerchantNotFound
	ErrKindOrderNotFound
	ErrKindEventNotFound
	ErrKindOrderAccepted
	ErrKindOrderDelivered
	ErrKindOrderCancelled
	ErrKindOrderNotAccepted
	ErrKindAddressChanged
	ErrKindEventTerminated
	ErrKindPermanentOutOfRange
	ErrKindTemporarilyUnreachable
)

var errKindNames = map[ErrKind]string{
	ErrKindInvalidParam:           "invalid_param",
	ErrKindClockRollback:          "clock_rollback",
	ErrKindMerchantNotFound:       "merchant_not_found",
	ErrKindOrderNotFound:          "order_not_found",
	ErrKindEventNotFound:          "event_not_found",
	ErrKindOrderAccepted:          "order_already_accepted",
	ErrKindOrderDelivered:         "order_already_delivered",
	ErrKindOrderCancelled:         "order_already_cancelled",
	ErrKindOrderNotAccepted:       "order_not_accepted",
	ErrKindAddressChanged:         "address_already_changed",
	ErrKindEventTerminated:        "event_already_terminated",
	ErrKindPermanentOutOfRange:    "permanent_out_of_range",
	ErrKindTemporarilyUnreachable: "temporarily_unreachable",
}

func (k ErrKind) String() string {
	if name, ok := errKindNames[k]; ok {
		return name
	}
	return "unknown"
}

// Error 是系统返回的唯一错误类型，Kind 字段支持程序化区分。
type Error struct {
	Kind ErrKind
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Kind, e.Msg)
}

func newErr(kind ErrKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

// ErrKindOf 提取错误的种类；非本系统错误返回 false。
func ErrKindOf(err error) (ErrKind, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind, true
	}
	return 0, false
}
