package seal

import "errors"

type ErrorKind string

const (
	KindInvalidParameter   ErrorKind = "invalid_parameter"
	KindClockRollback      ErrorKind = "clock_rollback"
	KindNotFound           ErrorKind = "not_found"
	KindInvalidState       ErrorKind = "invalid_state"
	KindFrozen             ErrorKind = "frozen"
	KindAuthorization      ErrorKind = "authorization_invalid"
	KindLimitExceeded      ErrorKind = "category_or_amount_limit_exceeded"
	KindDailyLimitExceeded ErrorKind = "daily_limit_exceeded"
	KindPresenceRequired   ErrorKind = "presence_confirmation_required"
)

type Error struct {
	Kind ErrorKind
	Msg  string
}

func (e Error) Error() string {
	return string(e.Kind) + ": " + e.Msg
}

func newError(kind ErrorKind, msg string) Error {
	return Error{Kind: kind, Msg: msg}
}

func ErrorKindOf(err error) ErrorKind {
	var se Error
	if errors.As(err, &se) {
		return se.Kind
	}
	return ""
}
