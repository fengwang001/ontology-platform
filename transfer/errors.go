package transfer

type ErrorKind string

const (
	KindInvalidArgument   ErrorKind = "invalid_argument"
	KindClockRollback     ErrorKind = "clock_rollback"
	KindOrderNotFound     ErrorKind = "order_not_found"
	KindInvalidState      ErrorKind = "invalid_state"
	KindAlreadyShipped    ErrorKind = "already_shipped"
	KindNotShipped        ErrorKind = "not_shipped"
	KindAlreadyClosed     ErrorKind = "already_closed"
	KindAlreadyCanceled   ErrorKind = "already_canceled"
	KindInsufficientStock ErrorKind = "insufficient_stock"
	KindOverReceived      ErrorKind = "over_received"
	KindCloseTooEarly     ErrorKind = "close_too_early"
	KindRecoveryTooMuch   ErrorKind = "recovery_too_much"
	KindNoShortage        ErrorKind = "no_shortage"
)

type Failure struct {
	Kind      ErrorKind
	Detail    string
	LineIndex int
}

func (e Failure) Error() string {
	if e.Detail == "" {
		return string(e.Kind)
	}
	return string(e.Kind) + ": " + e.Detail
}

func (e Failure) Is(target error) bool {
	other, ok := target.(Failure)
	return ok && other.Kind == e.Kind
}

var (
	ErrInvalidArgument   = Failure{Kind: KindInvalidArgument}
	ErrClockRollback     = Failure{Kind: KindClockRollback}
	ErrOrderNotFound     = Failure{Kind: KindOrderNotFound}
	ErrInvalidState      = Failure{Kind: KindInvalidState}
	ErrAlreadyShipped    = Failure{Kind: KindAlreadyShipped}
	ErrNotShipped        = Failure{Kind: KindNotShipped}
	ErrAlreadyClosed     = Failure{Kind: KindAlreadyClosed}
	ErrAlreadyCanceled   = Failure{Kind: KindAlreadyCanceled}
	ErrInsufficientStock = Failure{Kind: KindInsufficientStock, LineIndex: -1}
	ErrOverReceived      = Failure{Kind: KindOverReceived}
	ErrCloseTooEarly     = Failure{Kind: KindCloseTooEarly}
	ErrRecoveryTooMuch   = Failure{Kind: KindRecoveryTooMuch}
	ErrNoShortage        = Failure{Kind: KindNoShortage}
)
