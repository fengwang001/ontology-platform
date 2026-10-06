package ontology

import (
	"errors"
	"fmt"
)

// ErrorKind 按题目规定的统一拒绝次序排列，数值越小越靠前。
type ErrorKind int

const (
	KindInvalidArgument ErrorKind = iota
	KindClockRewind
	KindTicketNotFound
	KindTicketState
	KindDeparted
	KindChangeLimit
	KindVoucherNotFound
	KindVoucherOwner
	KindVoucherExpired
	KindVoucherUsed
	KindPaymentMismatch
	KindFlightNotFound
)

// OpError 携带可区分的错误类别与判定所需的数值（如应补现金金额）。
type OpError struct {
	Kind     ErrorKind
	Message  string
	CashDue  int64 // 仅 KindPaymentMismatch 使用：应付现金金额（分）
	Expected int64 // KindPaymentMismatch 时提交的现金金额
}

func (e *OpError) Error() string {
	if e.Kind == KindPaymentMismatch {
		return fmt.Sprintf("%s: cash paid=%d cash due=%d", e.Message, e.Expected, e.CashDue)
	}
	return e.Message
}

func errf(kind ErrorKind, format string, args ...any) error {
	return &OpError{Kind: kind, Message: fmt.Sprintf(format, args...)}
}

func asOpError(err error) (*OpError, bool) {
	var oe *OpError
	if errors.As(err, &oe) {
		return oe, true
	}
	return nil, false
}
