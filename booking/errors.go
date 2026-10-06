package booking

type ErrorKind string

const (
	ErrInvalidArgument ErrorKind = "invalid argument"
	ErrClockMovedBack  ErrorKind = "clock moved back"
	ErrTicketNotFound  ErrorKind = "ticket not found"
	ErrTicketRefunded  ErrorKind = "ticket already refunded"
	ErrDeparted        ErrorKind = "flight departed"
	ErrChangeLimit     ErrorKind = "change limit reached"
	ErrVoucherNotFound ErrorKind = "voucher not found"
	ErrVoucherOwner    ErrorKind = "voucher owner mismatch"
	ErrVoucherExpired  ErrorKind = "voucher expired"
	ErrVoucherSpent    ErrorKind = "voucher spent"
	ErrCashMismatch    ErrorKind = "cash amount mismatch"
)

type Error struct {
	Kind         ErrorKind
	Msg          string
	ExpectedCash int64
}

func (e Error) Error() string {
	return string(e.Kind) + ": " + e.Msg
}

func errorf(kind ErrorKind, format string, args ...any) error {
	return Error{Kind: kind, Msg: formatMsg(format, args...)}
}
