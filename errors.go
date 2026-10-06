package ontology

type ErrorCode int

const (
	OK ErrorCode = iota
	ErrInvalidArgument
	ErrClockRewound
	ErrNotFound
	ErrTimeWindow
	ErrIllegalState
	ErrQuietConflict
	ErrInsufficientDeposit
)

type Error struct {
	Code ErrorCode
	Msg  string
}

func (e Error) Error() string { return e.Msg }

func fail(code ErrorCode, msg string) Error { return Error{Code: code, Msg: msg} }
