package matching

import "fmt"

type ErrorCode string

const (
	ErrInvalidArgument    ErrorCode = "invalid_argument"
	ErrClockRewind        ErrorCode = "clock_rewind"
	ErrNotFound           ErrorCode = "not_found"
	ErrSupplierBlocked    ErrorCode = "supplier_blocked"
	ErrInvalidState       ErrorCode = "invalid_state"
	ErrSupplierMismatch   ErrorCode = "supplier_mismatch"
	ErrOrderLineNotFound  ErrorCode = "order_line_not_found"
	ErrPriceMismatch      ErrorCode = "price_mismatch"
	ErrOverInvoiced       ErrorCode = "over_invoiced"
	ErrOverReceipt        ErrorCode = "over_receipt"
	ErrReceiptInvoiceHeld ErrorCode = "receipt_invoice_held"
	ErrNegativeReceipt    ErrorCode = "negative_receipt"
	ErrDuplicateInvoice   ErrorCode = "duplicate_invoice"
	ErrInvoiceAlreadyPaid ErrorCode = "invoice_already_paid"
)

type Error struct {
	Code    ErrorCode
	Message string
	Line    int
	HasLine bool
}

func (e *Error) Error() string {
	if e.HasLine {
		return fmt.Sprintf("%s: line %d: %s", e.Code, e.Line, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func errorf(code ErrorCode, format string, args ...any) error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

func lineErrorf(code ErrorCode, line int, format string, args ...any) error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...), Line: line, HasLine: true}
}

func GetCode(err error) ErrorCode {
	var target *Error
	if ok := errorAs(err, &target); ok {
		return target.Code
	}
	return ""
}
